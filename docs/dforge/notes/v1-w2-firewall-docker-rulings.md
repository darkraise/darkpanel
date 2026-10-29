# v1 wave 2 firewall docker — rulings and amendments

Every ledger line of the run that holds a ruling, then every plan amendment, taken before the workspace was deleted.

## Rulings I made

- Ruling: execution worktree .worktrees/v1-w2-firewall-docker on branch v1-w2-firewall-docker from local main 5f1d747 — origin/main is 220 commits behind, so the native tool's fresh-from-origin base was not used — if wrong, the branch lacks the plan
- Ruling: finish choice set to merge into main — owner standing preference (memory darkpanel-finish-merge-locally), no question asked — merge is local only
- Ruling: pre-flight — 13 verdicts, see preflight.md: 3 AMEND (A1 Task 32, A2 Task 35, A3 Header), 4 CONFIRMED-GAP (3 fixed by A3 alone, 1 serialize Task 34 then Task 35), 6 PARK (Task 31 Depends line, three near-duplicate foreign-rule helpers, Task 33 wording slip, test-helper duplicate dtTable/dtfTable, refusal wording of Tasks 42/47/48) — parked items are cosmetic or test-only, a final reviewer may ask for shared helpers
- Task 32: Ruling: amendment A1 — add target.go to the Step 5 git add line, the task modifies it for change.notes — none, staging a required file is always correct
- Task 35: Ruling: amendment A2 — exposureManualNotes calls Task 34's statusManualRoutes instead of copying it — Task 35 waits for Task 34 within its wave and tests gain an indirect dependency
- Ruling: serialize Task 34 and Task 35, Task 34 first (preflight-143) — Task 35 consumes Task 34's statusManualRoutes — wave takes one extra task's time
- Ruling: header amendment A3 confirmed by a second judge-opus — AMEND with corrections (prefixes also on methods added to foreign types; Tasks 17 and 34 prefixes named) — if wrong, the plan's Global Constraints carry a bad rule into every later task
- Ruling: amendment A3 (Header) — unnamed unexported top-level identifiers and methods carry the task's prefix so parallel tasks in one package cannot collide at merge — some production helpers carry unidiomatic prefixes, cosmetic
- Ruling: wave 1 width lowered from 3 to 2 — context-size --wave 3 said handoff (140k of 70k, guessed source), --wave 2 said ok — Task 3 starts when a slot frees, the wave takes longer
- Task 2: parked — cannot-verify: later tasks (10, 12, 13, 17, 21, 22, 31) consume these types as the Contracts describe — Ruling: forward-looking, the diff cannot satisfy it; Task 2's exports match the Contracts entry exactly — if a consumer finds a mismatch its own review reports it, a one-line fix
- Ruling: container dp-w2-cap-exp (privileged, sleep infinity, created 08:47 UTC, before this run began at 10:04 UTC) left running and not removed — it is not from Task 1 and unfamiliar containers are not mine to delete; the owner can run docker rm -f dp-w2-cap-exp — it holds no port or file this plan needs, but docker ps -a --filter name=dp-w2- will not print only a header
- Task 1: parked — cannot-verify: leftover container dp-w2-cap-exp and the header-only docker ps check — Ruling: Task 1 removed every container it started and may remove only those; the check does not apply to a container that predates the run; no later task runs it
- Task 1: Ruling: 360 wave-merge notes for files outside Task 1's Files — all under dockerfw/testdata/capture/, which its Files line names as capture/<slug>/<scenario>/** and the note checker does not expand — none
- Task 3: parked — cannot-verify: full-suite result quoted only as a summary — Ruling: the log holds 61 ok and 2 no-test-files lines, no FAIL, and belongs to commit 5787e97 by reflog timestamp — if wrong, a failure in the loaded package would surface at the wave check
- Ruling: wave clean kept worktree /root/repositories/darkpanel/.claude/worktrees/agent-a856f3abcf3407dde (Task 1's, git reports it locked) — clean never forces and the agent has finished; its HEAD f794599 is the reviewed and merged commit (adba8eb); uncommitted files there are unchecked — an owner can remove it with git worktree remove --force after unlocking
- Ruling: wave 2 runs Task 4 then Task 5 in the checkout, one at a time — parallel width does not fit the budget, serializing is always correct — the wave takes longer than two parallel tasks
- Task 4: parked — cannot-verify: whether Tasks 11 and 35 can group by Decided when it is empty — Ruling: Task 4 matches its contract; Accept carries a non-empty Decided, only hook-level Unknown is empty and Task 11 classes any Unknown as unknown before grouping; Task 35 only passes Decided through — a consumer mismatch would show in Task 11's own review
- Ruling: wave 3 runs Tasks 6, 7, 8 in the checkout one at a time, closing with the full Integration check — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 6: parked — cannot-verify: suite log and lint result belong to HEAD 3e60df1 — Ruling: the suite log post-dates the commit with 63 ok lines and no FAIL, and the controller's -race run at 3e60df1 passed; the 0 issues lint result rests on the implementer's report and wave 3's full Integration check re-runs golangci-lint — a lint finding would surface there and be local to atoms.go
- Task 7: parked — cannot-verify: no suite-run digest for 420d066 — Ruling: the plan asks only for pnpm lint, typecheck and test to pass, which the implementer ran (29 files, 303 tests); the wave's closing full check re-runs everything — a failure would surface there
- Task 7: parked — cannot-verify: the brief's separate RED typecheck run was skipped — Ruling: the Vitest RED run failed before the implementation and the post-implementation typecheck passed, so no requirement is unmet — none
- Task 8: parked — cannot-verify: no suite-run digest for b855520 — Ruling: settled by the controller's full Integration check at b855520 (suite pass, 63 pass 0 fail) which includes the web gates — none
- Task 8: parked — cannot-verify: Go-side global gates not shown in the report — Ruling: the same full check ran every Go gate at b855520, and the diff touches only two web files — none
- Ruling: wave 4 runs Tasks 9, 10, 11 in the checkout one at a time — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 9: parked — ParsePorts repeats OpenPorts' scan loop (plan-mandated, nmap.go:109) — Ruling: the duplication stands; OpenPorts filters by host and skips bad entries while ParsePorts reads every host and errors on a bad entry, so sharing the loop would change wave 1's M1 scan tests, which the brief's "OpenPorts stays unchanged" prevents; Tasks 52-54 depend only on ParsePorts — a future nmap output change must be fixed in two neighbouring functions
- Task 9: parked — cannot-verify: gate results paraphrased in the report — Ruling: the fix round's section quotes the harness, harness/release and harness/vm ok lines and 0 issues. verbatim and the post-commit suite log holds 63 ok with no FAIL — none
- Task 9: parked — cannot-verify: pebble and challtestsrv ports and pins — Ruling: both images are pinned in test/harness/images.json and pins.go; whether they serve the ports is a run-time check for Tasks 52-54 — a wrong port assumption surfaces in Task 52-54's harness run
- Task 10: parked — cannot-verify: full-suite gate quoted as a wrapper summary — Ruling: the suite log (started 2 s after commit 0b1fed9) holds 63 ok lines and no FAIL, --- or panic line, which meets the brief's every-line-ok-or-? requirement — none
- Task 10: parked — cannot-verify: RED evidence quotes two of five undefined identifiers — Ruling: the brief's RED step only describes the expected failure and does not require quoting all five; the report shows real build-failure lines — none
- Task 11: Ruling: amendment A4 — a wildcard target is probed at every address of the family on each probe interface, not only the first (verdict.go:356 under-reported exposure on secondary addresses) — Task 11 needs a fix round; Contracts "exposure" paragraph at plan line 383 goes stale and the brief's amended sentence supersedes it
- Task 11: Ruling: amendment A5 — internet excludes only the family's own in.Private entries plus loopback, multicast and unspecified; Tailscale and link-local sources stay in internet per spec 2.7 (verdict.go:323 evaluated them in no class) — same fix round
- Task 11: parked — cannot-verify: how Atoms treats exclude prefixes of the other family — Ruling: dropped by design (Task 6 text, atoms.go paSpans, an existing test), and A5 makes Task 11 filter in.Private by family itself — none
- Task 11: parked — cannot-verify: whether Task 35 passes Target.Addr unmapped and of in.Family — Ruling: Task 35's text already does (Unmap on host and local addresses, one Input per family) — Task 35's review would catch a regression
- Ruling: seat observation outside Task 11's items — Task 35 gives a :: listener a single IPv6 row, so a dual-stack socket's IPv4 exposure is never evaluated; not amended now — check at Task 35's review and the final review (exposure under-report risk)
- Ruling: wave 5 runs Tasks 12, 13 in the checkout one at a time — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 12: parked — every IPv4 endpoint held to a bridge raw rule whatever its driver (plan-mandated, detect.go:104) — Ruling: stands; Task 31 builds Endpoints only for bridge-driver networks and is the only caller of RawProtected, and the spec reports non-bridge networks as unprotected rather than refusing the host — a non-bridge endpoint reaching RawProtected would wrongly refuse a supported host
- Task 12: parked — cannot-verify: whether Task 31 fills Endpoint.Bridge for every endpoint — Ruling: Task 31 Step 3 item 4 sets it from the bridge name or dockerfw.Bridge, never empty, and TestDfDockerStateFacts pins the values — an empty Bridge would refuse the host rather than hide an exposure
- Task 31: Ruling: amendment A6 — Task 31 step 3 item 5 passes RawProtected a copy of the facts without endpoints on Internal networks, plus TestDfRawProtectionInternal (Docker 28.5.1 loads no raw drop rule for internal networks, so a container on an internal network refused a supported host) — Task 12 and Contracts unchanged
- Ruling: seat observation outside Task 12's items — the plan's driver-direct-routing check is stricter than the spec: a host whose Docker daemon has allow-direct-routing set would be refused although its localhost-port protection, all the spec requires, is loaded; not amended now — check at Task 31's review and the final review
- Task 13: Ruling: cannot-verify — settled by running TestDtf* -v at 2bb3a25: TestDtfCaptures and its 16 capture subtests, TestDtfPropose, TestDtfForeignDNAT, TestDtfCovered and TestDtfExemptionConflicts all pass — none
- Ruling: wave 6 width lowered from 3 to 2 — context-size --wave 3 said handoff (137k of 70k), --wave 2 said ok — Task 16 starts when a slot frees, the wave takes longer
- Task 14: Ruling: cannot-verify — settled by go build ./... and go vet ./internal/modules/firewall/... passing on the checkout after the merge at 5e25935 (the stream type satisfies the events interface and ReadVariant matches the Contracts signature, or neither would compile) — none
- Task 16: Ruling: review seat dforge:judge-opus wrote a malformed report (line 20 under Findings was neither a bullet nor an indented detail) — restored nothing to restore, deleted the report and re-dispatched once — none
- Task 15: Ruling: cannot-verify — settled by running docker-sources.test.tsx in the checkout after the merge at 66f0ca3 (15 of 15 pass, including the two tests that depend on FirewallChange rendering no dialog without input and naming itself by title) — none
- Task 16: Ruling: cannot-verify — settled by running the exposure test file alone in the checkout at 1492b89 (9 of 9 pass, no jsdom canvas notices from it, so they come from older test files); the missing suite-run digest is covered by the wave 6 full Integration check, which runs the gates through suite-run on the merged head — none
- Ruling: wave 7 width lowered from 3 to 2 — context-size --wave 3 said handoff (167k of 70k), --wave 2 said ok — Task 19 starts when a slot frees, the wave takes longer
- Task 17: parked — netfilter.Key output for a V6 bare -j RETURN could not be verified from the diff — Ruling: the requirement is met; Key returns exactly -j RETURN in both families (the ipv6 flag only changes the recent mask, icmp6 names and REJECT defaults), established by reading the code; the missing V6 test case is a coverage gap — if wrong, Foreign flags Docker's own default V6 rule as foreign and the in-sync check shows a false drift
- Task 17: parked — callers must supply CustomBridges sorted and BindingLine.HostAddr zero for a wildcard — Ruling: Task 21 rules 2 and 5 already require both and TestRzRealizeCustomBridges and TestRzRealizeHostAddress pin them; Task 21's review should confirm those two tests are present — if wrong, a wildcard HostAddr prints --ctorigdst 0.0.0.0 and the published port is silently closed
- Task 18: Ruling: review seat dforge:judge-opus wrote a malformed report (line 20 under Findings was a trailing '(1 more Minor left out)' sentence, not a bullet) — deleted the report and re-dispatched once with an instruction to list every Minor as its own bullet — none
- Task 18: parked — fw.run writing the block files after a restore's files and before the ufw commands, as the new doc comment at target.go:74-80 states — Ruling: Task 38 Step 3 item 1 and the Contracts paragraph already order fw.run that way and TestDruRunOrder pins files before the ufw commands; the after-a-restore's-files half is unreachable because no builder emits restore and files together — if wrong, only the Task 18 doc comment overstates a combination nothing produces
- Task 19: Ruling: cannot-verify cv-19-1 CONFIRMED-GAP — ContainerRef.name may carry Docker's leading slash, so no stored binding matches and the selector is invalid; fix round 1 strips one leading slash in container-actions.tsx and adds one test; wave 3a's plan must state ContainerRef.name is the container name without the slash — if left, Allow fails with an invalid-selector error and Deny is unusable for every container
- Task 19: Ruling: cannot-verify cv-19-2 PARK — firewall-change.tsx opens its dialog only for a non-null input and calls onClose at most once per dialog — a double onDone would only refresh the container page twice
- Task 20: parked — whether later tasks (26, 28, 31, 36) call writeDockerConfig and replaceContainerRules inside a db.Write transaction as the contract intends — Ruling: the plan text of Task 26 step 3.2, Task 30 step 3.2 and Task 37 step 4 already calls them inside one write and Task 36 uses only the foreign-rules table, so nothing needs re-checking when they land — if wrong, a later task would write the Docker settings outside a transaction and a failed insert could leave partial state
- Ruling: wave 9 runs Tasks 21, 22, 23 in the checkout one at a time, closing with the wave check-due check — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 21: Ruling: automated security review flagged rzIs6 in dockerfw/realize.go (an IPv4-mapped or zoned From prefix is classified IPv6 by Realize but may be classified differently by Render, so a deny rule could land in the wrong family) — same gap as Task 10's deferred minor (rule.go: From not checked for IPv4-mapped form); passed to the Task 21 review as an explicit check item, not acted on mid-task — if real and unfixed, a deny for a mapped source could be silently omitted from its intended family
- Task 21: Ruling: amendment A7 — rule 3's Unmap family test contradicts the merged Render (Is4), so the two can file one address under different families; Realize now uses Is4 and a new test TestRzFamilyMatchesRender pins it — cost if wrong: a mapped address is treated as IPv6 by both and Validate refuses one in From anyway
- Task 21: Ruling: amendment A8 — ContainerRule.Validate accepted an IPv4-mapped From, letting a deny inside a private range escape spec 2.6's refusal; Task 10 is merged so Task 21 takes the fix and its Files block gains rule.go and rule_test.go — cost if wrong: a mapped source is refused with a field error instead of stored, the operator writes the IPv4 form
- Task 31: Ruling: amendment A9 — no task unmapped the addresses Docker reports, and Render and RawProtected classify by Is4; Task 31 now unmaps the published host address and the endpoint's IPv4 address and drops an IPv4-mapped IPv6 endpoint address — cost if wrong: one Unmap call per field with no effect if Docker never emits the mapped form, no test pins it
- Task 34: Ruling: amendment A10 — Task 22's review could not verify that Task 34 fills HealthInput consistently; Task 34's text requires Expected, Loaded and Interfaces but no test fails when Interfaces is unset or Loaded lacks the nat table, so TestDstHealthInterfaces is added — cost if wrong: if its reason strings drift from dockerfw.Health, Task 34's Step 4 fails on that test and the implementer corrects the test
- Task 23: parked — CleanupInput's :DOCKER-USER redeclaration discards foreign DOCKER-USER rules, against the constraint that no automatic operation discards one — Ruling: Task 37 refuses with firewall.docker_user_in_use in refuse mode and in preserve mode reloads the foreign rules through Task 36's keepForeign (DockerUserInput) after cleanup, pinned by TestClRefuse, TestClPreserve, TestClFinishCleanups, TestClFailedLoadIsFinished and TestClCleanChainsWiring; a crash between the two restores leaves a record that finishCleanups restores from — if wrong, a foreign DOCKER-USER rule could be dropped during cleanup
- Task 23: parked — -X of the owned chains fails and -n rejects the whole input if a chain outside the input still jumps to one — Ruling: the finding's premise does not hold for Darkpanel's block (ufw-user-forward is a jump target of dp-b-*, never a jumper; the only jumps into the owned chains come from DOCKER-USER, which CleanupInput resets in the same transaction), commands.txt shows exit 0 for both restores on all four images, pinned by TestCapCleanup and Task 37's TestClCleanup — if wrong, the cleanup restore is rejected and the chains stay loaded
- Ruling: wave 10 runs Tasks 24, 25, 26 in the checkout one at a time, closing with the wave check-due check — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 24: Ruling: cannot-verify — the leftover container dp-w2-cap-exp is the one the ledger already ruled on (predates the run, left alone, owner may docker rm -f it; Task 1 parked the same item), and the suite log .dforge/suites/1793196943-3420877.log holds 65 ok-or-? lines and no FAIL, --- FAIL or panic line — none
- Task 25: BLOCKED report from impl-sonnet-medium (TestDcRealize failed on all four images: the brief's dcFacts gave an IPv4-only endpoint an Addr6 of 0.0.0.0, so Realize emitted a spurious -d 0.0.0.0/32 binding line); sent to the ruling seat as a blocked-plan item
- Task 25: Ruling: amendment A11 — the brief's test helper dcFacts set Endpoint.Addr6 to 0.0.0.0 for an IPv4-only endpoint, against the Contracts rule "zero when absent"; the endpoint addresses are now parsed with netip.ParseAddr and the error discarded so an empty one stays empty, and dcAddr stays for host addresses; the merged Realize needs no change — cost if wrong: TestDcRealize keeps failing on all four images and Task 25 stays blocked
- Task 25: Ruling: cannot-verify — the leftover container dp-w2-cap-exp is the one the ledger already ruled on (predates the run, left alone, owner may docker rm -f it) — none
- Task 25: parked — whether the 16 expected verdicts in TestDcDecisions and the noRaw modelling match spec 2.6 — Ruling: none of the 16 contradicts spec 2.6 and the spec says nothing about the raw table, so evaluating without it tests only the block's own chains; compared by reading, tests not run — if wrong, a verdict row would assert behaviour the spec does not give
- Task 25: complete (commits af1a2fd..812cc88, review clean, 1 parked, 5 minors deferred; scores spec 19 / scope 20 / verification 15 / quality 16, seat dforge:judge-opus (group 25)) — done: dockerfw captures test that checks the rendered block's decisions against the committed image captures (decisions_test.go); verified: TestDcRealize, TestDcPrediction and TestDcDecisions pass on all four images, each of the three Step 3 edits makes its test fail and was undone, race run on dockerfw, full suite 63 pass 0 fail at 812cc88, golangci-lint 0 issues; remaining: the parked item; discovered: the first attempt was BLOCKED by the brief's dcFacts giving an IPv4-only endpoint an Addr6 of 0.0.0.0, fixed by amendment A11; assumptions: none
- Task 26: parked — the Contracts entry "Docker effects" says the observe dispatch covers four nouns while the brief and code have three — Ruling: three is right; "four" counts the four resources, but the same sentence lists three noun constants, and Task 27's inverse adds one case for those three and needs no fourth — if wrong, Task 27's inverse would miss a noun
- Ruling: wave 11 runs Tasks 27, 28, 29 in the checkout one at a time, closing with the wave check-due check — parallel width does not fit the budget, serializing is always correct — the wave takes longer
- Task 27: parked — Task 37 must set cleanChains in productionFW or undoing a firewall.chains:docker effect whose Before is "absent" fails in production — Ruling: the plan already requires it (Task 37 lists wire.go in its Files and Step 3 item 2 sets f.d.cleanChains = f.cleanChainsPreserving) and TestClCleanChainsWiring (Task 37 Step 1) fails if productionFW leaves cleanChains nil; Task 37's review confirms that test is present and passing — if wrong, undoing a chain cleanup fails outside tests
- Task 28: parked — whether every capture caller runs under the firewall lock so the separate Docker reads agree with row writes — Ruling: the only production caller is mutation.Snapshot (handler.go:98), which the engine calls inside the apply under applyMu after locksFree confirms the firewall lock is free and before takeLocks, so no other firewall change can run then; fw.prepare, fw.run and repair do not call capture and no later task adds a caller — if wrong, a snapshot could read Docker settings and rows from different moments
- Task 28: parked — whether Tasks 30, 40 and 45 read DockerSettings == nil as write dockerfw.DefaultSettings — Ruling: Tasks 30 and 45 both do (Task 30 has TestDrsRestoreOfASnapshotFromBeforeTheIntegration; Task 45 states the rule without a test of its own) and Task 40 never reads DockerSettings — if wrong, restoring a pre-integration snapshot would write empty settings
- Task 29: parked — whether DockerStatus.health and DockerIssue.level are the closed unions HEALTH_BADGES and LEVEL_BADGES assume — Ruling: they are closed string-literal unions in types.ts and both badge maps are Record types over exactly those unions, so the compiler enforces the keys — if wrong, a new health or level value would render without a badge
- Task 29: parked — FirewallChange re-planning with manual: true after firewall.manual_required, and its effect on the "Repair and discard foreign rules" path — Ruling: the path plans first without manual, FirewallChange catches the refusal, asks the operator and re-plans with manual: true only on "Change it anyway", so foreign rules are never discarded without the confirmation; the server side (Task 47 refusing discardForeign without manual) is not built yet and was checked against the plan text only — if Task 47 does not implement it, the failure is safe: the web shows an error and discards nothing
- Task 30: parked — whether Task 40 decides its cleanup from the stored Docker flag, which Task 30 overwrites before any file write — Ruling: Task 40's text defines cleanup from the snapshot's afterwards files and the loaded chains (readLoaded), never from readDockerConfig, and TestKfwRestore's two no-block cases cannot pass with a flag-driven design — if wrong, Task 40 could skip or run a cleanup from a flag already overwritten
- Task 31: parked — whether Tasks 34 and 35 depend on dfState recomputing realized after the opts run — Ruling: neither task calls dfState (Task 34 builds states with dstState(), Task 35 with xvDocker()), so a mismatch would show as a compile error in that task, not a silent change — if wrong, a Task 34/35 test fails to compile and is fixed there
- Task 32: parked — whether the plan-diff line texts satisfy Tasks 41-48 — Ruling: the plan text and tests of Tasks 42, 43, 45, 46 and 48 use exactly the texts Task 32 emits and Tasks 41, 44 and 47 name none — if wrong, a later task's own plan test fails at its first run and its expected string is corrected there
- Task 34: Ruling: amendment A12 — TestDstResource asserts the fake's bridge network is listed (`"networks":[{"name":"bridge"`) instead of `"networks":[]`, because Networks is filled whenever Docker is available and only Issues is gated on Supported — if wrong, an unsupported host shows its network list and a later change must gate statusNetworks and one test line
- Task 33: parked — plan-conflict: prepare reads the stored Docker flag only when dockerUserInUse finds something to refuse (Task 32's test plans with env.DB = nil) — Ruling: spec 2.6 says nothing about when the flag is read, the lazy shape refuses identically in every case the brief and its tests list, and it keeps Task 32's test passing; no text change — if wrong, a store read error goes unreported when DOCKER-USER holds nothing to refuse
- Task 33: parked — whether Tasks 37, 47 and 49 assume prepare reads the flag on every call — Ruling: none does (Task 37 never calls prepare, Task 47's discard path skips the check in both shapes, Task 49 reads the flag itself) — if wrong, one of those tasks' tests fails on first run
- Task 34: parked — Task 50 registering f.dockerResource() in resources() — Ruling: Task 50 Step 3 item 2 already appends f.dockerResource() and f.exposureResource() — if wrong, Task 50's review catches the missing registration
- Task 34: parked — BlockOf return order assumed by docker_resource.go:232 — Ruling: matches the Contracts dockerfw block signature (block, ok, err) — if wrong, the build would already fail
- Task 35: Ruling: amendment A13 — a `[::]` listener also keys a `0.0.0.0` IPv4 row (same protocol and port) and TestXvRows gains a `[::]:3000` listener with no IPv4 twin plus its two expected rows, because Linux `[::]` sockets are dual-stack by default and spec 2.7 asks what each source can reach — if wrong, an IPv6-only listener (V6ONLY) shows a spurious IPv4 row
- Task 35: parked — RawProtected (dockerfw/detect.go, Task 12) is stricter than the spec, so a host running Docker's allow-direct-routing would be refused as unsupported — Ruling: the check fails closed with an explicit reason, the Task 1 captures cover only default daemons, and nothing downstream depends on it; the final review carries it as an open owner note — if wrong, hosts with allow-direct-routing are refused until the owner relaxes the check with a capture scenario
- Task 35: parked — whether Task 50 registers fw.exposureResource() — Ruling: Task 50 Step 3 item 2 and its resource-list test already require it — if wrong, Task 50's own test fails on its first run
- Task 36: Ruling: cannot-verify — the suite log 1793205518-3776215.log holds 63 ok lines and no FAIL, panic or warning — settled by the controller's grep of the log — none
- Task 36: Ruling: amendment A14 (Task 37) — Task 37 discards the interleaved result of keepForeign and finishForeign, because its cleanup leaves no block lines and the foreign rules reload in original order ahead of -j RETURN; Task 40 already turns the interleaved result into needs_repair at every call — if wrong, a revert that cleans an interleaved layout ends clean instead of needs_repair, no foreign rule is lost
- Task 37: Ruling: amendment A15 — the foreign record stores the after files and the held cleanup repeat goes through keepForeign (Task 37's Files gains docker_preserve.go; new test TestClLateForeignKept), because the repeat otherwise deletes a foreign DOCKER-USER rule added after a cut-off cleanup, which spec 2.6 and the Global Constraints forbid — if wrong, each record stores up to two whole after files and the new test's expected IPv4 order needs adjusting; no rule is lost
- Task 37: parked — whether A14 matches the discard text of brief step 4.5 — Ruling: A14's New text is word for word the step 4.5 text the reviewer checked — if wrong, none
- Task 37: parked — whether Task 40 can leave a record under a change id whose cleanup runs later — Ruling: Task 40 records under restore:/revert: owners that never equal a cleanup owner (the change id or uninstall) and Task 38 never calls keepForeign — if wrong, a cleanup repeat would reload -j RETURN in place of the restore's block and the next reconcile reports health broken; no foreign rule is lost
- Task 38: Ruling: amendment A16 — fw.run runs cleanupChains first, before a restore's files, so the cleanup still sees the block in the after files and recognises its own DOCKER-USER rules; druBlock and druLoaded gain a conditional DOCKER-USER line and TestDruRunOrder's expected order changes (pc-38-2 CONFIRMED-GAP carried by A16) — if wrong, docker.disable and every cleanup change refuse with firewall.docker_user_in_use on a real host, or preserve mode keeps the block's own lines as foreign
- Task 38: parked — whether deEditConfig's read through env.DB races with concurrent writers — Ruling: the store has a single writer connection and every write takes an immediate transaction, so the read inside deEditConfig happens under the write lock — if wrong, a lost update of the stored Docker settings
- Task 38: Ruling: cannot-verify — the suite log 1793207906-785985.log holds 63 ok lines and no FAIL or panic — settled by the controller's grep of the log — none
- Ruling: seat notes N1-N3 from the Task 38 ruling — N1 Task 40 restoreOver has the same file-before-cleanup defect (in preserve mode), N2 a reverted docker.disable must end with the block's chains loaded (check at Task 40's review or the final review), N3 Task 39 must run its uninstall cleanup before restoreBaseline strips the block — controller sends N1 and N3 to the ruling seat before Tasks 39 and 40 are dispatched — if left, Tasks 39 and 40 repeat the Task 38 defect
- Task 40: Ruling: amendment A17 — restoreOver calls d.cleanChains (and re-reads the loaded state) before it writes the snapshot's files, and TestKfwRestore checks the cleanup runs before the first file write, because the cleanup needs the block still in the files to recognise its own DOCKER-USER rules (the Task 38 defect); Tasks 45 and 46 restore through fw.run, which already cleans first — if wrong, a restore with cleanup reloads the block's own rules as foreign against chains that no longer exist
- Task 40: Ruling: amendment A18 — undoKeepingForeign runs one conditional reloadIfActive after undoNewestFirst, with a new TestKfwRevert subtest, because a revert otherwise reloads UFW before the file inverses put the block back (A16 does not fix this); the subtest relies on fake-host behaviour read but not run and may need adjusting in a fix round — if wrong, a reverted docker.disable ends with the block's chains not loaded
- Task 39: parked — whether Task 39's uninstall cleanup runs after restoreBaseline strips the block — Ruling: only rules=baseline rewrites the after files first, through restoreOver, which under A17 runs the cleanup while the files still hold the block; until Task 40 lands, a baseline uninstall refuses and no Task 39 test catches it — if wrong, uninstall with rules=baseline refuses with firewall.docker_user_in_use
- Task 39: Ruling: amendment A19 — the cleanup's UFW reload also requires /usr/sbin/ufw to exist (`&& f.regular(ufwPath) == nil` in docker_cleanup.go, Task 39's Files gains it), the "remove without UFW" case asserts no ufw command ran (RED first), and kfHost creates the ufw binary so Task 37's TestClCleanup keeps its expected reload (cv-39-1 CONFIRMED-GAP carried by A19: Env.Effects is nil on the uninstall path, internal/install/uninstall.go:576) — if wrong, uninstall on a host with UFW stopped or removed fails after the cleanup finished and leaves the uninstall intent row
- Task 39: parked — whether a repeated uninstall succeeds after a failed reload — Ruling: on the repeat nothing is left to change, so the cleanup never calls ufw and clears the leftover intent — if wrong, a repeated uninstall keeps failing
- Task 40: implementer impl-opus-high reported BLOCKED on amendment A18 only (A17 and the rest committed as b2daf12): the A18 subtest "inverses of a cleanup" fails inside undoNewestFirst before the added reload can run — the chains effect's inverse runs ufw reload while the files still lack the block, so the chains stay absent and inverse (inverse.go) stops the undo; sent to the ruling seat as a blocked-plan item
- Task 40: Ruling: amendment A20 (replaces A18) — new helper kfwUndoOrder in docker_revert.go moves a chains effect whose earlier state was "loaded" ahead of the cleanup's two file effects so the files get the block back before the reload; A18's extra reload is removed and its subtest kept; undoKeepingForeign calls finishRecorded when keepForeign fails (a new subtest covers the stale revert: record) — if wrong (spec 2.9 step 3 "reverse order" applies strictly within one cleanup), the fix moves to inverse.go, a larger change
- Task 40: Ruling: concern — a repair snapshot that captured a foreign rule always fails before-image verification when restored — carried to the final review as an Important finding — if wrong, restoring such a snapshot fails verification
- Task 40: Ruling: amendment A21 — restore.go:185 and inverse.go:157 pass a copy of env without the journal (Effects nil) to d.cleanChains, Task 40's Files gains inverse.go, new test TestKfwCleanupUnjournaled covers both paths, because a cleanup run inside a revert otherwise records new made effects in the change being reverted and the revert ends needs_repair (also for reverts by inverses of docker.enable, which Task 51's end-to-end tests expect to end reverted) — if wrong, every revert of docker.enable ends needs_repair
- Task 40: parked — the implementer's readings of `known` (a present file whose block does not parse is left out) and `loads` (enabled && (v4 || (ipv6 && v6))), and the failed-body finishRecorded reload — Ruling: both readings are recorded for Tasks 45 and 46 to use, and the reload concern needs a DOCKER-USER flush without the block's chains loaded, which does not happen on real UFW — if wrong, Tasks 45/46 read the ambiguous sentence differently or foreign rules stay unloaded until a later revert
- Task 40: parked — whether other effects sit between the cleanup's file effects and its chains effect for kfwUndoOrder — Ruling: fw.run runs the cleanup first and nothing records between them in any change carrying cleanup — if wrong, kfwUndoOrder misses a chains effect and the revert fails its Before check
- Task 41: Ruling: amendment A22 — Task 24's tests have no case with a delete followed by a non-zero Insert, so numbering against the pre-delete lists would pass; A22 adds a v4 insert after a v4 delete and a v6 insert after a v4 delete to ruleops_test.go (test only, no code change) — if wrong, two redundant table rows; if skipped, a container deny could land before its allow
- Task 41: Ruling: Task 24 is already complete, so A22's two test cases are not implemented by any pending task — carried to the final review as a test-only gap on internal/modules/firewall/dockerfw/ruleops_test.go (TestRoRuleOps) — if wrong, the delete-then-insert numbering stays unpinned until the final fix wave
- Task 41: parked — cv-41-2 (revert/recovery of an `insert` create) — Ruling: runUfw, observe and deleteAdded find a created rule by its id tag and recorded post state and never read the allow/deny verb in Args, so an insert create reverts like an add — if wrong, a reverted or crashed insert leaves a stray tagged container rule
- Task 42: Ruling: amendment A23 — Task 42 Step 3 item 5 made disable read the full dockerState, so a reachable but broken daemon refused firewall.docker.disable; Task 44 already uses a zero dockerState for state.disable; A23 has disable read only readDockerConfig — if wrong, disable refuses on a broken daemon
- Task 42: parked — cv-42-1 (blockFiles vs dockerFlagCheck's two-file loop) — Ruling: blockFiles always handles both after files and refuses the build if either is missing, so verify always matches what enable wrote — if wrong, none for verify
- Task 42: parked — cv-42-2 (suite log unopened) — Ruling: the log holds 63 ok lines and no FAIL, panic or --- lines — if wrong, none, the next wave's Integration check re-runs the suite
- Task 43: parked — cv-43-1 (container rule plan lines and firewall.container:<id> journal effects) — Ruling: Task 32's docker_diff tests and Task 38's run_docker and docker_effects tests pin the lines and effects, and TestCtrActions pins the plan lines and the created effect (the removed row's delete effect is covered by the Task 38 tests through the same rowDeleteOp) — if wrong, a container allow, deny or remove would show a wrong diff line or write a wrong journal effect
- Task 44: parked — cv-44-1 (nil env.DB in readDockerConfig for state.enable, state.disable and defaults.set) — Ruling: Engine.env copies the engine's Base and engine.go:153 rejects a nil Base.DB, so every production Env has a database; a nil-DB guard would hide a wiring error — if wrong, planning one of the three actions without a database panics
- Task 44: parked — cv-44-2 (cleanup before the transition on disable) — Ruling: fw.run (handler.go:212) runs the cleanup first, TestDruRunOrder and TestDruRunFiles pin that order for a disable-shaped change, integrationChange sets cleanup = !enable and TestDshIntegrationChange asserts it; no Task 44 test runs disable end to end, the harness steps in Tasks 53/54 do — if wrong, disable would clean after the transition
- Task 45: parked — cv-45-1 (Docker effects on the inactive-UFW restore path) — Ruling: fw.run (handler.go:212-248) runs the cleanup, writeRestored and runLogical before the UFW transition without testing whether UFW is active, and the spec orders a restore that way; no test pins the inactive case — if wrong, a restore on an inactive-UFW host would leave the Docker flag, settings or rows unrestored
- Task 45: Ruling: amendment A24 — Task 46's fixture drpCustom leaves ForwardExemptions nil so TestDrpRestoreAppliesState would fail as Task 45's dsrCustom did (sourcesOp stores nil lists as empty); A24 makes it an empty list — if wrong, Task 46 stalls on a fixture failure and rediscovers this
- Task 46: parked — cv-46-1 (dockerRestoreChange refusal and cleanup) — Ruling: dockerRestoreChange calls refuseOpenIncoming(want.cfg.Enabled, final) with the snapshot's flag and configuration before reading any state, and withDockerRestore sets c.cleanup = now.cfg.Enabled && !want.cfg.Enabled on both restore paths (actions_snapshot.go:45, actions_repair.go:107) — if wrong, a restore or repair could open incoming traffic or leave Docker chains loaded after turning the integration off
- Ruling: wave 24 (Tasks 47-48) runs one task after another in the checkout, in index order — a parallel wave would exhaust the session budget mid-wave — if wrong, the wave takes longer wall-clock and nothing else
- Task 47: parked — cv-47-1 (fw.prepare skipping dockerUserInUse under discardForeign, predictTarget modelling the flush, plan-level test) — Ruling: plan.go:68 guards the refusal and TestDrefPrepare covers it, predictTarget overlays the block files and takes the reload branch so the foreign rules are gone from the predicted state (TestDtgPredictsTheBlock); the plan-level test of a successful repair listing the discarded rules can wait, since Task 51's TestDeeForeignRuleRefusesMutations plans and applies firewall.docker.repair with discardForeign and manual and asserts the diff lists the rule (the final review checks it survives) — if wrong, a repair with discardForeign is refused or its plan hides the discarded rules
- Task 48: parked — cv-48-1 (private sources plan line and revert of firewall.setting:sources) — Ruling: Task 32's dockerDiff tests assert the "private sources:" lines and TestDsoSourcesSetApplied checks fw.plan emits one for this action; Task 27's TestDiInverseSettings undoes a firewall.setting:sources effect and checks the prior lists return, and Task 48 writes the same effect through sourcesOp; no Task 49-54 test reverts firewall.docker.set-sources, which the earlier tests make unnecessary — if wrong, a revert of set-sources would leave the new lists in place
- Task 49: parked — cv-49-1 (firewall.docker.repair accepts every difference reconcile routes to it) — Ruling: dockerfw.Owned is a subset of the chains firewallChain covers and confined in sync.go uses firewallChain, so the repair plan guard never refuses a routed difference — if wrong, reconcile passes would fail the repair guard on every run
- Task 49: parked — cv-49-2 (containers.apply with an empty ops list rewrites stale after files) — Ruling: containerChange in actions_container.go (Task 43's file) sets the files and the reload for any after file that differs even with no ops, pinned by the subtest "a change that restores a lost realization is made" — if wrong, a pass that finds stale files would keep applying a no-op change
- Task 49: parked — cv-49-3 (registration of reconcileTask and the actor system:firewall.reconcile) — Ruling: Task 50 Step 3 item 3 registers reconcileTask through module.BackgroundTasks and TestRegReconcileTask pins it; the runtime builds the actor as "system:"+task ID and ApplySystem accepts any non-irreversible action of the task's own module — if wrong, the task is never registered or its actions are refused
- Ruling: header amendment A25 confirmed by a second judge-opus — AMEND with one correction (the tag only, without the register-row comment) — if wrong, the plan's Contracts carry a bad rule into every later task
- Task 50: Ruling: amendments A25 (Header), A26, A27 — the Contracts entry gave ExposureRow.Container without jsonschema:"nullable", Task 35 copied it, and registering firewall.exposure makes TestPointerOutputsAreNullable fail so the suite is red at e6d6d12; A25 tags the Contracts field, A26 and A27 put exposure_resource.go and schemas/firewall.exposure/v1.json into Task 50's Files block and commit line; the code change runs as fix round 1 — if wrong, Task 51 starts from a red base
- Task 50: parked — cv-50-1 (tagged field passes the guard) — Ruling: the fix round's own run of ./internal/modules/ and ./schemas/ settles it — if wrong, the guard still fails after the fix
- Task 50: parked — cv-50-2 (other untagged pointer outputs) — Ruling: the guard checks every registered output and reported only rows[].container; the other pointers in Contracts are input fields — if wrong, the fix round's run of ./internal/modules/ names them
- Ruling: wave 27 (Tasks 51-52) runs one task after another in the checkout, in index order — the budget leaves no room for two tasks at once — if wrong, the wave takes longer wall-clock and nothing else
- Task 51: Ruling: amendments A28-A30 — Task 51 reported NEEDS_CONTEXT on three fixture problems: the base IPv6 captures lack the ufw6-user-limit REJECT/ACCEPT rules, the captured Docker 29.9.1 DOCKER-USER is empty while the spec-mandated cleanup leaves -j RETURN, and the brief's recreated address 172.17.0.9 is outside docker0 172.18.0.0/16; A28 completes the in-memory IPv6 kernel and records the forwarding sysctls in deeNewHost, A29 lets deeKernelDiffs tolerate one extra -j RETURN in DOCKER-USER in either family (the before-state stays empty), A30 derives the recreated address from web's own — no production or capture change — if wrong, an empty-chain mishandling would block Task 51 again on a real production bug
- Task 51: parked — cv-51-1 (Task 1 capture's IPv6 limit-chain rules missing in all four images) — Ruling: the capture stays unchanged and the gap is recorded for the final review; Tasks 52-54 read no dockerfw capture file and only dockerfw capture_test.go, decisions_test.go and Task 51's host fixture read them, the fixture working around the gap (A28) — if wrong, a later test asserting the base IPv6 save is in sync with UFW sees a false mismatch
- Task 51: parked — cv-51-2 (repair snapshot that captured a foreign DOCKER-USER rule fails before-image verification) — Ruling: stays carried to the final review as Important; verifyRestore runs only from restoreOver on revert and restore, and Tasks 52-54 never revert a repair snapshot holding a foreign rule — if wrong, a harness scenario reverting such a repair fails on a VM
- Task 51: parked — cv-51-3 (suite log not opened) — Ruling: .dforge/suites/1793247719-1548894.log holds 63 ok package lines and 2 no-test-files lines and no FAIL, panic, race or warning lines — if wrong, none
- Task 52: parked — cv-52-1 (scenario 7 requires exactly one -j RETURN in DOCKER-USER after firewall.state.disable) — Ruling: spec 2.6 says the cleanup leaves DOCKER-USER holding only -j RETURN and state.disable with the flag on runs it, so wantReturn stays true; the final review reads scenario 7 in the gate-7 log and relaxing to false is acceptable only if it shows zero -j RETURN lines after UFW stops — if wrong, scenario 7 fails on a VM
- Task 52: parked — cv-52-2 (real iptables-nft-save text of the exemption and DNAT gate) — Ruling: routed to the gate-7 run; the final review looks for m3dxDockExemptionLine and m3dxDockBlockLoaded failures — if wrong, those helpers fail on a VM
- Task 52: parked — cv-52-3 (scenario 10's wrapper path and stall-file race) — Ruling: the plan restricts the agent to calling /usr/sbin/<tool>-restore -n by path so the wrapper assumption holds; a stall-file race would show as a timeout in scenario 10 — if wrong, scenario 10 times out on a VM
- Task 52: parked — cv-52-4 (none of the eleven scenarios has run) — Ruling: the plan routes the gate-7 run (header line 42, Task 54 Step 6, gates.md gate 7); harness.yml is dispatched once after Task 54 is committed and the branch is pushed, since only Task 54 adds the TestM3Docker workflow step — if wrong, an earlier run would not exercise Task 52
- Task 53: Ruling: reviewed alone although task-step hold held it for a group review — hold and review ran back to back, the individual review package already exists and Task 54 is the only other member of a would-be group — if wrong, Task 53 loses a shared group read but gets a full judge-opus review of its own
- Task 53: parked — cv-53-1 (60-minute TestM3Docker deadline) — Ruling: the deadline stays; Task 54 owns the workflow (-timeout 70m, 280-minute job limit) and checks it against m3dDockDeadline so it can raise the deadline if the gate-7 run needs more time, and the 90-second waits only run to their limit when a scenario is already failing — if wrong, the gate-7 run times out and Task 54 raises the deadline
- Task 53: parked — cv-53-2 (scenarios pass on real VMs) — Ruling: they run only in gate 7, routed after Task 54 is committed and the branch pushed (Task 52 ruling) — if wrong, a scenario fails on a VM
- Task 53: parked — cv-53-3 (same source-B address added by two scenarios) — Ruling: m3dAddSource registers its removal on the subtest's own t, so the second add does not collide — if wrong, the second add collides
- Task 53: parked — cv-53-4 (release server needed for the outbound connection to scannerIPv4:8000) — Ruling: serveReleases runs before the steps (m3_docker_test.go:25) — if wrong, the outbound connection scenario fails
- Task 54: Ruling: amendment A31 — m3dExpoRead called t.Fatal inside the four eventually checks, so one transient firewall.exposure read ended a scenario instead of being retried; A31 makes it return (raw, view, error) and each check return the error; adjacent: Task 53's m3dTrafficExpect calls m3dScan, which is fatal on an nmap or SSH failure inside eventually, left for the final review — if wrong, one extra error return in three scenarios, no behaviour change on a passing run
- Task 54: parked — cv-54-1 (TestM3Docker 60-minute deadline over 23 steps) — Ruling: the waits are upper limits only a failing step reaches; the first gate-7 run is the check, and if TestM3Docker takes more than 45 minutes on any image the final review raises the deadline to 90 minutes, the step's -timeout to 100m and the job limit to 300 minutes in one commit — if wrong, the gate-7 run times out
- Task 54: parked — cv-54-2 (expected exposure verdicts for docker-proxy IPv6 rows and 18302 partial) — Ruling: they follow from the spec's rules for docker-proxy ports and the rows Task 35's TestXvRows checks; 18302 is partial because the scanner's addresses are outside the default private ranges; gate 7 confirms — if wrong, the scenario fails on a VM
- Ruling: final review Codex round skipped — the Codex review surface is off for this session (codex-gate review=false) — the final review runs on one list, every finding enters the fix wave and the fixer triages — if wrong, no independent second reader sees the branch
- Ruling: final-residual fr-1 CONFIRMED-GAP — revert.go:104 returns an error before any write when endChangeCleanup's finishForeign fails, skipping the lockout check and emergency restore and refusing a revert for foreign rules (spec 2.6, 2.5 step 4); smallest fix: on that failure move the cleanup record to the revert's own revert:<change> owner so the revert's existing foreign-rule handling loads it, continue the revert, and let a DB-write failure end the change needs_repair through the lockout check; add TestKfwEndCleanupReloadFails (two subtests) — if wrong, a failed cleanup reload leaves published container ports unprotected until reconcile
- Ruling: final-residual fr-2 PARK — skipping the readConfig/readLoaded reads when the integration is off would bring back the flush bug the fix closed (reverting a disable puts the block back while the current flag is off); finishing's report states that a revert by inverses whose readConfig or readLoaded fails ends needs_repair or does an emergency whole restore on any host, and that the readConfig branch has no test — if wrong, a revert of a docker.disable on a host with the integration now off is refused when the reads fail
- Ruling: final follow-up 484de36 (fr-1) — the follow-up's subtest 2 keeps loaded-state reads failing up to the lockout guard's own read (an always-failing readLoaded makes the emergency restore fail too), and the revert's interrupted-restore branch (revert.go:120-124) ignores a cleanup error, so a DB-write failure there could end the change reverted with the cleanup intent still recorded; no second fix wave runs — both are named in finishing's report — if wrong, the cleanup could run again over a restored block after a DB write failure
- Ruling: finishing stopped at Step 1 — gate 7 (harness.yml run 40827705616 on 484de36) is red on all four images: 15 of 21 TestM3Docker scenarios pass; gate 6 (CI run 40827703941) is green; gates 1-5 are green at 484de36 — diagnosis in harness-diagnosis.md: (1) production defect: endChangeCleanup (docker_revert.go:249) reloads DOCKER-USER from the cleanup's own record, which predates the foreign rule 192.0.2.80, so recovery of an interrupted docker.disable empties the chain and loses the rule (scenario foreign-in-interrupted-cleanup); (2) test defect: m3dDockTry (m3_docker_lifecycle_test.go:401-410) tries disable once and gives up on 423 lock.held (the reconcile's change holds the firewall lock), leaving the integration on for traffic-deny-keeps-allow, traffic-proxied-ipv6, traffic-recreate-under-scan, exposure-equals-scan and exposure-private-class — nothing merged, branch pushed to origin as v1-w2-firewall-docker — if wrong, the fixes below chase the wrong cause
- Ruling: harness-red fix 484de36..26473c7 (371fee3 finishForeign merges the live DOCKER-USER chain with the record, 26473c7 m3dDockTry retries while 423 lock.held for at most 2 minutes) — reviewed by a fresh judge-opus (final-rereview-3-report.md): addressed, 0 open, 4 Minor; harness.yml re-dispatched on 26473c7 as run 40832952378 — if wrong, gate 7 stays red
- Ruling: finishing stopped again at Step 1 — gate 7 on 26473c7 (harness run 40832952378) is red again with different per-image failures (CI run 40832952970 green); harness-diagnosis-2.md: mostly TEST defects and cascades — 423 holders are fresh changes that free the lock in 2-4 s and the retry then stops on a plan taken mid `ufw reload` (not the capture gap), a stopped cleanup leaves the integration enabled for all later scenarios, ubuntu-24.04 has three separate test defects (a probe-count threshold, the scanner picking the added IPv6 address as source, a binding view listed only for bindings with rules), and on debian-12 the recovery revert briefly loads DOCKER-USER without the block's gate (an Important production hazard); first fix: m3dDockEnable disables a still-enabled integration (retrying any refusal for 2 minutes) before waiting — nothing merged; branch v1-w2-firewall-docker (26473c7) is pushed — if wrong, the next harness run keeps failing for the same reasons
- Ruling: harness-red fix 2, 26473c7..721e40b (7 commits: scenario isolation, three ubuntu-24.04 test defects, 07c1c21 keeps the DNAT gate on the cleanup end of a revert, 721e40b the debian-12 scenario awaits recovery) — reviewed by a fresh judge-opus (final-rereview-4-report.md): addressed, 0 open, 3 Minor; harness.yml re-dispatched on 721e40b as run 40838876699, CI as run 40838877039 — if wrong, gate 7 stays red

## Amendments made

## A1 — Task 32
Reason: the Files block modifies `target.go` (the `notes` field), but the commit does not stage it, so the committed tree does not build.
Cost if wrong: none; the task must change that file anyway.
### Old
````text
git add internal/modules/firewall/docker_diff.go internal/modules/firewall/plan.go internal/modules/firewall/docker_diff_test.go
````
### New
````text
git add internal/modules/firewall/docker_diff.go internal/modules/firewall/plan.go internal/modules/firewall/target.go internal/modules/firewall/docker_diff_test.go
````

## A2 — Task 35
Reason: the task mandates a verbatim copy of Task 34's `statusManualRoutes` logic; reuse that function instead (with Task 34 serialized first, preflight-143).
Cost if wrong: Task 35 waits for Task 34 inside wave 13.
### Old
````text
7. `func exposureManualNotes(cfg, facts) []string`: for each rule of `model.Merge(cfg.Rules4, cfg.Rules6)` whose `Origin` is manual, whose `Direction` is route, whose `Raw` is empty and whose `To` is not `any`, parse `To` as a prefix (an address becomes a host prefix; an unparsable value is skipped); when the prefix overlaps a subnet of any network in `facts`, the note `<ruleText(rule)>: no effect without a Darkpanel binding` (spec 2.6 "Manual route rules"). Task 34 applies the same test to `DockerStatus.ManualRoutes` in its own function; the two tasks share a wave, so each declares its own copy.
````
### New
````text
7. `func exposureManualNotes(cfg, facts) []string`: for each text that Task 34's `statusManualRoutes(cfg, facts)` (`docker_resource.go`) returns, in its order, the note `<text>: no effect without a Darkpanel binding` (spec 2.6 "Manual route rules"); nil when it returns none. Do not repeat that function's selection logic: Task 34 runs and merges before this task, and an `undefined: statusManualRoutes` means it is missing from the base, so stop and report it.
````

## A3 — Header
Reason: same-wave tasks in one Go package (21–23, 31–32) must write helpers they do not name and get no prefix rule, so parallel implementers can declare the same function or the same method on a shared type.
Cost if wrong: some production helpers and methods on shared types carry a task prefix; this is cosmetic.
### Old
````text
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from the table in Contracts ("Test prefixes"), except the shared helpers that table names, which only their owning task declares.
````
### New
````text
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from the table in Contracts ("Test prefixes"), except the shared helpers that table names, which only their owning task declares. Outside its test files, every unexported function, type, variable or constant a task adds, and every unexported method it adds to a type it did not declare itself (`fw`, `module`, `change`, `deps`, or a type of another task such as `dockerfw.Network` or `Facts`), that neither Contracts nor the task's own text names, starts with the prefix the task's own text sets (Task 11 `ev`, Task 12 `det`, Task 13 `dtf`, Task 17 `block`, Task 34 `status`) or, when its text sets none, with the task's prefix from that table. Methods on a type the task itself declares need no prefix.
````

## A4 — Task 11
Reason: probing only the interface's first address misses `-d` rules on its other addresses, so a wildcard row under-reports exposure (spec 2.7 asks for the real verdict and is silent on the destination).
Cost if wrong: a partial decided only by destination lists all the class's sources as allowed; there are more probes; destinations owned by other interfaces are still not probed.
### Old
````text
   - Destination: `t.Addr` when it is valid and not unspecified; otherwise the probe interface's first address of the family (`Is4` for IPv4, `Is6` and not `Is4In6` for IPv6). An interface with no address of the family yields no probe.
   - A probe is a `fwstate.Packet` with `Family` `in.Family`, `Path` `input`, `InIface` the probe interface, the source and destination above, `SrcPort` the port atom's `From`, `DstPort` `t.Port`, `Proto` `t.Proto` lower-cased and `CtState` `NEW`.
   - `localhost` has one probe per port atom: `Path` `output`, `OutIface` `lo`, and the same address as both `Src` and `Dst`: `t.Addr` when it is valid and not unspecified, otherwise `127.0.0.1` (IPv4) or `::1` (IPv6). Its source set is that one address.
   - Probes are ordered by atom, then interface, then port atom.
   - Before any probe is evaluated: more than `in.MaxProbes` probes (`DefaultMaxProbes` when it is 0), counted as atoms times interfaces times port atoms, makes the class `unknown` with `Note` "too many partitions"; a class that ends up with no probe makes it `unknown` with `Note` "no interface to probe".
````
### New
````text
   - Destination: `t.Addr` when it is valid and not unspecified; otherwise every address of the family on the probe interface (`Is4` for IPv4, `Is6` and not `Is4In6` for IPv6), in `Addrs` order and without duplicates. Each (interface, destination) pair is one probe point. An interface with no address of the family yields no probe point. This supersedes the Contracts "exposure" sentence "A wildcard target's destination is the probe interface's first address of the family".
   - A probe is a `fwstate.Packet` with `Family` `in.Family`, `Path` `input`, `InIface` the probe interface, the source and destination above, `SrcPort` the port atom's `From`, `DstPort` `t.Port`, `Proto` `t.Proto` lower-cased and `CtState` `NEW`.
   - `localhost` has one probe per port atom: `Path` `output`, `OutIface` `lo`, and the same address as both `Src` and `Dst`: `t.Addr` when it is valid and not unspecified, otherwise `127.0.0.1` (IPv4) or `::1` (IPv6). Its source set is that one address.
   - Probes are ordered by atom, then interface, then destination, then port atom.
   - Before any probe is evaluated: more than `in.MaxProbes` probes (`DefaultMaxProbes` when it is 0), counted as atoms times probe points times port atoms, makes the class `unknown` with `Note` "too many partitions"; a class that ends up with no probe makes it `unknown` with `Note` "no interface to probe".
   - Item 7's `Ifaces` is nil when the group's distinct interface names are all the distinct interface names the class probed. Count interface names, not probe points.
   - Step 1's test file gains this row in `TestExEvaluate`'s table, directly after the row named "a wildcard uses each probe interface's own address". The file is otherwise exactly Step 1's content:

     ```go
     		{
     			name: "a wildcard probes every address of the interface", lines: "-A ufw-user-input -d 192.0.2.11/32 -p tcp -m tcp --dport 22 -j ACCEPT\n",
     			edit: func(in *Input) {
     				for i := range in.Ifaces {
     					if in.Ifaces[i].Name == "eth0" {
     						in.Ifaces[i].Addrs = append(in.Ifaces[i].Addrs, netip.MustParseAddr("192.0.2.11"))
     					}
     				}
     			},
     			want: exWant("partial", "tailnet", "filtered", "localhost", "open"),
     			check: func(t *testing.T, got map[string]Verdict) {
     				if v := got["internet"]; len(v.Allowed) != 1 || v.Allowed[0].Ifaces != nil || v.Allowed[0].SrcPorts != nil ||
     					v.Allowed[0].DecidedBy != "filter/ufw-user-input#1" {
     					t.Errorf("internet allowed = %+v", v.Allowed)
     				}
     			},
     		},
     ```
````

## A5 — Task 11
Reason: spec 2.7 defines internet as every address outside `privateSources`, so the plan's Tailscale-range and link-local exclusions leave those sources unevaluated on non-Tailscale interfaces.
Cost if wrong: an operator who narrows `privateSources` sees two extra internet atoms (link-local, the CGNAT range) that may make a row `partial`, which over-reports.
### Old
````text
   - Source atoms: `internet` is `Atoms([]netip.Prefix{the family's whole address space}, exclude, srcs)` where `exclude` is the family's entries of `in.Private`, the family's Tailscale range (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`), loopback (`127.0.0.0/8`, `::1/128`), link-local (`169.254.0.0/16`, `fe80::/10`), multicast (`224.0.0.0/4`, `ff00::/8`) and the unspecified address (`0.0.0.0/32`, `::/128`); `private` is `Atoms([]netip.Prefix{Class.Range}, nil, srcs)`; `tailnet` is `Atoms([]netip.Prefix{the family's Tailscale range}, nil, srcs)`. A probe's source address is its atom's `From`. `srcs` is the first result of `Cuts`.
````
### New
````text
   - Source atoms: `internet` is `Atoms([]netip.Prefix{the family's whole address space}, exclude, srcs)` where `exclude` is the entries of `in.Private` of the family (filter on `Addr().Is4()`, so the result does not rely on `Atoms` ignoring the other family), loopback (`127.0.0.0/8`, `::1/128`), multicast (`224.0.0.0/4`, `ff00::/8`) and the unspecified address (`0.0.0.0/32`, `::/128`). The Tailscale range and link-local are not excluded. When `in.Private` does not cover them, they stay in `internet`, as spec 2.7 defines it ("addresses outside every `privateSources` range"). This supersedes the Contracts "exposure" paragraph's exclusion of Tailscale's ranges and link-local from internet. `private` is `Atoms([]netip.Prefix{Class.Range}, nil, srcs)`; `tailnet` is `Atoms([]netip.Prefix{the family's Tailscale range}, nil, srcs)`. A probe's source address is its atom's `From`. `srcs` is the first result of `Cuts`.
   - Step 1's test file gains this row in `TestExEvaluate`'s table, directly before the row named "a target bound to loopback". The file is otherwise exactly Step 1's content:

     ```go
     		{
     			name: "link-local and Tailscale-range sources outside privateSources are internet",
     			lines: "-A ufw-user-input -s 169.254.0.0/16 -p tcp -m tcp --dport 22 -j ACCEPT\n" +
     				"-A ufw-user-input -s 100.64.0.0/10 -p tcp -m tcp --dport 22 -j ACCEPT\n",
     			edit: func(in *Input) {
     				in.Private = slices.DeleteFunc(in.Private, func(p netip.Prefix) bool { return p == netip.MustParsePrefix("100.64.0.0/10") })
     			},
     			want: exWithout(exWant("filtered", "internet", "partial", "tailnet", "open", "localhost", "open"), "private 100.64.0.0/10"),
     			check: func(t *testing.T, got map[string]Verdict) {
     				want := []Allowed{
     					{Sources: []string{"169.254.0.0/16"}, DecidedBy: "filter/ufw-user-input#1"},
     					{Sources: []string{"100.64.0.0/10"}, DecidedBy: "filter/ufw-user-input#2"},
     				}
     				if v := got["internet"]; !slices.EqualFunc(v.Allowed, want, exAllowedEqual) {
     					t.Errorf("internet allowed = %+v, want %+v", v.Allowed, want)
     				}
     			},
     		},
     ```
````

## A6 — Task 31
Reason: Docker loads no raw DROP for internal-network endpoints, so passing the full facts to RawProtected refuses supported hosts.
Cost if wrong: internal-network endpoints are not checked for a raw rule; the change touches only Task 31's raw-protection input and one test.
### Old
````text
5. Detection: `Version` is `Info`'s `ServerVersion`; `Driver` is `FirewallBackend.Driver`, `iptables` when `FirewallBackend` is nil or its driver is empty; `UFWVariant` is `cur.Variant`. `DockerVariant` comes from `f.d.readVariant(ctx, env, fwstate.V4, variant, []string{"filter"})` for `"nft"` and for `"legacy"`: a variant holds Docker's chains when the call returned a ruleset whose `filter` table is non-nil and satisfies `dockerfw.DockerChains`. A nil ruleset (the variant cannot be read safely) does not hold them. One variant gives its name, both give `both`, none gives the empty string. A read error is returned as a `firewall:` error. Only when exactly one variant holds the chains, read its `raw` table the same way (`[]string{"raw"}`) and set `RawProtection` to `dockerfw.RawProtected(table, facts)` when the table is non-nil; in every other case `RawProtection` stays false and no raw read is made.
````
### New
````text
5. Detection: `Version` is `Info`'s `ServerVersion`; `Driver` is `FirewallBackend.Driver`, `iptables` when `FirewallBackend` is nil or its driver is empty; `UFWVariant` is `cur.Variant`. `DockerVariant` comes from `f.d.readVariant(ctx, env, fwstate.V4, variant, []string{"filter"})` for `"nft"` and for `"legacy"`: a variant holds Docker's chains when the call returned a ruleset whose `filter` table is non-nil and satisfies `dockerfw.DockerChains`. A nil ruleset (the variant cannot be read safely) does not hold them. One variant gives its name, both give `both`, none gives the empty string. A read error is returned as a `firewall:` error. Only when exactly one variant holds the chains, read its `raw` table the same way (`[]string{"raw"}`) and set `RawProtection` to `dockerfw.RawProtected(table, rawFacts)` when the table is non-nil; in every other case `RawProtection` stays false and no raw read is made.
   - `rawFacts` is a copy of `facts` in which each container leaves out every endpoint whose network the `ListNetworks` answer reports with `Internal` true. It shares no `Endpoints` slice with `facts`, and `facts` itself (so `st.facts` and `realized`) keeps those endpoints. Reason: Docker's bridge driver loads no raw drop rule for an internal network's endpoints (moby v28.5.1 `libnetwork/drivers/bridge/internal/iptabler/endpoint.go` returns early for an internal network), so holding them to one would refuse a supported host.
   - Step 1's test file gains this test directly after `TestDfDockerStateDetection`. The file is otherwise exactly Step 1's content:

     ```go
     // TestDfRawProtectionInternal: Docker loads no raw drop rule for an
     // endpoint on an internal network, so the raw protection does not expect
     // one; the facts still list that endpoint.
     func TestDfRawProtectionInternal(t *testing.T) {
     	const raw = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
     		"-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP\nCOMMIT\n"
     	for _, internal := range []bool{false, true} {
     		h := dfNewHost(t)
     		h.v.raw = map[string]string{"nft": raw}
     		back := dfNetwork("back", 'i', "bridge", false, nil, "172.31.0.0/24")
     		back.Internal = internal
     		h.d.networks = []dockerapi.Network{dfNetwork("bridge", 'b', "bridge", false, nil, "172.17.0.0/16"), back}
     		h.d.containers = []dockerapi.ContainerJSON{dfContainer("web", nil, nil, map[string]*dockerapi.EndpointSettings{
     			"bridge": dfEndpoint("172.17.0.2", ""), "back": dfEndpoint("172.31.0.2", "")})}
     		st, err := h.f.dockerState(context.Background(), h.env, dfCur)
     		if err != nil {
     			t.Fatal(err)
     		}
     		if st.det.RawProtection != internal {
     			t.Errorf("internal %v: RawProtection %v, want %v", internal, st.det.RawProtection, internal)
     		}
     		if c := st.facts.Containers; len(c) != 1 || len(c[0].Endpoints) != 2 {
     			t.Errorf("internal %v: containers %+v, want web with both endpoints", internal, c)
     		}
     	}
     }
     ```
````

## A7 — Task 21
Reason: rule 3's `Unmap` family test contradicts the merged `Render` (`Is4`), so the two can file one address under different families.
Cost if wrong: a mapped address is treated as IPv6 by both; pc-21-2 refuses one in `From` anyway.
### Old
````text
3. **Family and address of a family.** An address belongs to IPv4 when `Unmap` makes it IPv4, otherwise to IPv6. An allow row applies to a family when its `From` is the zero prefix (any source) or an address of that family; a deny row applies to a family only when `From` is an address of that family.
````
### New
````text
3. **Family and address of a family.** An address belongs to IPv4 when `Is4()` holds, otherwise to IPv6: the same test as Task 17's `blockFamilyOf`, so `Render` files every line and chain entry under the family Realize chose (an IPv4-mapped address is IPv6 here; `ContainerRule.Validate` refuses one in `From`, and Task 31 unmaps Docker's addresses). An allow row applies to a family when its `From` is the zero prefix (any source) or an address of that family; a deny row applies to a family only when `From` is an address of that family.
   - The family helper is `func rzIs6(a netip.Addr) bool { return !a.Is4() }`. Step 1's test file gains this test at its end and adds `"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"` to its imports; the file is otherwise exactly Step 1's content:

     ```go
     // TestRzFamilyMatchesRender: Realize and Render must file every address
     // under the same family, or a line or a deny lands in a family whose
     // chain never lists it.
     func TestRzFamilyMatchesRender(t *testing.T) {
     	for _, s := range []string{"192.0.2.1", "::ffff:192.0.2.1", "2001:db8::1", "0.0.0.0", "::"} {
     		a := netip.MustParseAddr(s)
     		if got, want := rzIs6(a), blockFamilyOf(a) == fwstate.V6; got != want {
     			t.Errorf("rzIs6(%s) = %v, want %v (Render's family)", s, got, want)
     		}
     	}
     }
     ```
````

## A8 — Task 21
Reason: `ContainerRule.Validate` accepts an IPv4-mapped `From`, so a deny inside a private range escapes spec 2.6's refusal and reaches Realize's family mismatch; Task 10 is merged, so Task 21 takes the fix.
Cost if wrong: a mapped source is refused with a field error instead of stored; the operator writes the IPv4 form.
### Old
````text
**Files:**
- Create: `internal/modules/firewall/dockerfw/realize.go`
- Test: `internal/modules/firewall/dockerfw/realize_test.go`
````
### New
````text
**Files:**
- Create: `internal/modules/firewall/dockerfw/realize.go`
- Modify: `internal/modules/firewall/dockerfw/rule.go`
- Test: `internal/modules/firewall/dockerfw/realize_test.go`, `internal/modules/firewall/dockerfw/rule_test.go` (modify)

**Refusing an IPv4-mapped source.** Spec 2.6 refuses a deny that lies inside a `privateSources` range, and `::ffff:10.0.0.0/104` lies inside `10.0.0.0/8`, so `ContainerRule.Validate` (`rule.go`, Task 10's) refuses an IPv4-mapped `From`, as `Binding.HostAddr` and `privateSources` already do. In its `/from` switch, add this case directly after `case set && !r.From.IsValid():` and its `add` line, before the masked check:

```go
	case set && r.From.Addr().Is4In6():
		add("/from", "is an IPv4-mapped IPv6 range; write it as an IPv4 range")
```

In `rule_test.go`, add these two rows to `TestBdRuleValidate`'s table directly after the row named "an invalid prefix"; change nothing else in the file:

```go
		{"an IPv4-mapped allowance", nil, func(r *ContainerRule) { r.From = netip.MustParsePrefix("::ffff:203.0.113.0/120") }, []string{"/from"}, "IPv4"},
		{"an IPv4-mapped deny of a private range", nil, deny("::ffff:10.0.0.0/104"), []string{"/from"}, "IPv4"},
```

Write the rows first and see them fail (`Validate() = <nil>`) with `go test -count=1 -run TestBdRuleValidate ./internal/modules/firewall/dockerfw/`, then add the case. Step 5's commit also stages these two files: `git add internal/modules/firewall/dockerfw/realize.go internal/modules/firewall/dockerfw/realize_test.go internal/modules/firewall/dockerfw/rule.go internal/modules/firewall/dockerfw/rule_test.go`.
````

## A9 — Task 31
Reason: no task unmaps Docker's addresses, and the merged `Render` and `RawProtected` classify by `Is4`, so an IPv4-mapped facts address would land in the wrong family.
Cost if wrong: one `Unmap` call per field with no effect if Docker never emits the mapped form; no test pins it.
### Old
````text
   - `Ports` come from `NetworkSettings.Ports`, keyed `<container port>/<proto>`: only the protocols `tcp` and `udp`, only keys with at least one binding, and only bindings whose host port parses (`strconv.ParseUint` with bit size 16). A binding's `HostIP` parses with `netip.ParseAddr`; an empty one means `0.0.0.0`; one that does not parse is skipped. Ports are sorted by container port, protocol, host port, then host address (`netip.Addr.Compare`, IPv4 first).
   - `Endpoints` come from `NetworkSettings.Networks`, only for entries that are non-nil and name a network of the list whose driver is `bridge`: `Network` the name, `Bridge` that network's bridge, `Addr4` the endpoint's `IPAddress` and `Addr6` its `GlobalIPv6Address` (zero when absent). Endpoints are sorted by network name.
````
### New
````text
   - `Ports` come from `NetworkSettings.Ports`, keyed `<container port>/<proto>`: only the protocols `tcp` and `udp`, only keys with at least one binding, and only bindings whose host port parses (`strconv.ParseUint` with bit size 16). A binding's `HostIP` parses with `netip.ParseAddr` and is then unmapped (`Unmap`), so `::ffff:a.b.c.d` becomes the IPv4 address; an empty one means `0.0.0.0`; one that does not parse is skipped. Ports are sorted by container port, protocol, host port, then host address (`netip.Addr.Compare`, IPv4 first).
   - `Endpoints` come from `NetworkSettings.Networks`, only for entries that are non-nil and name a network of the list whose driver is `bridge`: `Network` the name, `Bridge` that network's bridge, `Addr4` the endpoint's `IPAddress` unmapped (`Unmap`) and `Addr6` its `GlobalIPv6Address`, zero when absent or IPv4-mapped (`Is4In6`). Endpoints are sorted by network name. Every address in the facts is therefore plain IPv4 or IPv6, because `dockerfw.Realize`, `Render` and `RawProtected` decide the family with `Is4`.
````

## A10 — Task 34
Reason: Task 22's review could not verify that Task 34 fills `HealthInput` consistently. The text of Task 34 Step 3 requires `Expected`, `Loaded` and `Interfaces`, but no test fails when `Interfaces` is left unset, or when `Loaded` lacks the nat table, because `dstHealthWant` omits `Interfaces` and no health test uses `dstNat`. This adds a test that does.
Cost if wrong: if the added test is wrong (for example the reason strings drift from `dockerfw.Health`), Task 34's Step 4 fails on `TestDstHealthInterfaces` and the implementer must correct the test to Task 22's reason strings, which are `interface <name> is a br-* bridge that is not a Docker network's bridge` and `foreign DNAT forward has no exemption (IPv4): <text>`. If the amendment is not applied, `Interfaces` and nat wiring stay unpinned.

### Old
````text
func TestDstForeignRules(t *testing.T) {
````

### New
````text
// TestDstHealthInterfaces: the host's interfaces and the loaded nat table
// reach health, so a br-* link that is no Docker network's bridge and a
// foreign DNAT forward without an exemption are both reported.
func TestDstHealthInterfaces(t *testing.T) {
	st := dstState()
	st.cfg.Enabled = true
	st.interfaces = []netstat.Interface{{Index: 1, Name: "lo"}, {Index: 7, Name: "br-lan"}}
	ds := dockerStatusOf(dstConfig(t, true), dstLoaded(t, "", dstNat), st)
	wantBridge := dockerfw.Issue{Level: dockerfw.Degraded, Reason: "interface br-lan is a br-* bridge that is not a Docker network's bridge"}
	var bridge, forward bool
	for _, is := range ds.Issues {
		bridge = bridge || is == wantBridge
		forward = forward || (is.Level == dockerfw.Degraded && strings.HasPrefix(is.Reason, "foreign DNAT forward has no exemption (IPv4)"))
	}
	if !bridge || !forward {
		t.Errorf("issues %+v, want the br-lan bridge issue (%t) and the foreign DNAT issue (%t)", ds.Issues, bridge, forward)
	}
}

func TestDstForeignRules(t *testing.T) {
````

## A11 — Task 25
Reason: dcFacts sets Endpoint.Addr6 to 0.0.0.0 for an IPv4-only endpoint, against the Contracts rule "zero when absent", so Realize emits a spurious binding line.
Cost if wrong: TestDcRealize keeps failing on all four images and Task 25 stays blocked.
### Old
````text
			if bridge, ok := bridges[name]; ok {
				ct.Endpoints = append(ct.Endpoints, Endpoint{Network: name, Bridge: bridge, Addr4: dcAddr(e.IPAddress), Addr6: dcAddr(e.GlobalIPv6Address)})
			}
````
### New
````text
			if bridge, ok := bridges[name]; ok {
				// An endpoint without an address in a family has the zero
				// address there, unlike a host address, where empty means all.
				a4, _ := netip.ParseAddr(e.IPAddress)
				a6, _ := netip.ParseAddr(e.GlobalIPv6Address)
				ct.Endpoints = append(ct.Endpoints, Endpoint{Network: name, Bridge: bridge, Addr4: a4, Addr6: a6})
			}
````

## A12 — Task 34
Reason: the unsupported-host test expects no networks, but Step 3 and the fake Docker (which always has the bridge network) require the bridge network to be listed whenever Docker is available.
Cost if wrong: if networks should be hidden on an unsupported host, one test line and the `statusNetworks` gate change later; the web already renders either shape.
### Old
````text
	for _, want := range []string{`"networks":[]`, `"rules":[]`, `"bindings":[]`} {
````
### New
````text
	for _, want := range []string{`"networks":[{"name":"bridge"`, `"rules":[]`, `"bindings":[]`} {
````

## A13 — Task 35
Reason: a `[::]` socket is dual-stack on Linux by default, so one IPv6 row leaves its IPv4 exposure unevaluated, against spec 2.7's per-source reachability and its two-family harness check.
Cost if wrong: an `IPV6_V6ONLY` `::` socket with no IPv4 socket shows an IPv4 row that over-reports; the fix is one key per `::` socket plus three test lines.
### Old
````text
   - Sockets: only `Listening()` sockets with a local port other than 0. The key uses the local address unmapped, so an IPv4-mapped IPv6 socket is an IPv4 row; a `::` listener is one IPv6 row. `Process` is the distinct `Comm` values of its processes in order, joined with `, `, except that `docker-proxy` is dropped when a published port has the same key (the row then shows its container); a `docker-proxy` socket with no published port stays a process.
````
### New
````text
   - Sockets: only `Listening()` sockets with a local port other than 0. The key uses the local address unmapped, so an IPv4-mapped IPv6 socket is an IPv4 row. A socket whose unmapped local address is the IPv6 unspecified address `::` gives two keys: the `::` IPv6 key and a `0.0.0.0` IPv4 key with the same protocol and port. A socket bound to `[::]` accepts IPv4 too unless it sets `IPV6_V6ONLY` (`net.ipv6.bindv6only` is 0 by default, and Go, Node and Java listeners bind this way), and spec 2.7 asks what each source can reach. When a `0.0.0.0` socket or a published port already has that IPv4 key, they merge into one row. `Process` is the distinct `Comm` values of its processes in order, joined with `, `, except that `docker-proxy` is dropped when a published port has the same key (the row then shows its container); a `docker-proxy` socket with no published port stays a process. Both keys of a `::` socket follow this rule, each against its own key.
   - Step 1's test file gains one socket and two expected rows, and is otherwise exactly Step 1's content. In `xvSockets`, directly after the line `xvSocket(netstat.ProtoUDP, "0.0.0.0:123", "chronyd"),`, add:

     ```go
     		xvSocket(netstat.ProtoTCP, "[::]:3000", "node"),
     ```

     In `TestXvRows`'s `want`, directly after the line that begins `` `udp 0.0.0.0:123 v4 ``, add:

     ```go
     		`tcp 0.0.0.0:3000 v4 proc="node" ctr=- internet=filtered localhost=open`,
     		`tcp :::3000 v6 proc="node" ctr=- internet=filtered localhost=open`,
     ```

     Write these first and see TestXvRows fail on the missing `tcp 0.0.0.0:3000 v4` row, then change `exposureRows`.
````

## A14 — Task 37
Reason: Task 37 calls keepForeign/finishForeign without saying what to do with their interleaved result; with no block left it carries no misplacement and must be discarded.
Cost if wrong: a cleanup inside a revert of an interleaved layout completes instead of ending needs_repair; no foreign rule is lost.
### Old
````text
   5. `steps` is a closure over `cleanupSteps(ctx, env, cfg, cur)`. When a record is held, an earlier attempt of this owner was cut off after it read DOCKER-USER, and that record, not the chain, says what is foreign: the files no longer hold the block, so reading the chain again would take block lines still loaded for foreign rules. Run `steps`, set the record's `Block` to nil (the cleanup leaves `-j RETURN`; the record's block lines belong to a caller that restores a state with the block), store it again with `putForeign` and call `finishForeign`. Otherwise, when either family has a chain `cleanupOwned` lists, call `keepForeign(ctx, env, owner, known, nil, steps)` (its `next` is nil: DOCKER-USER ends as `-j RETURN` and the foreign rules). Otherwise call `steps` alone;
````
### New
````text
   5. `steps` is a closure over `cleanupSteps(ctx, env, cfg, cur)`. When a record is held, an earlier attempt of this owner was cut off after it read DOCKER-USER, and that record, not the chain, says what is foreign: the files no longer hold the block, so reading the chain again would take block lines still loaded for foreign rules. Run `steps`, set the record's `Block` to nil (the cleanup leaves `-j RETURN`; the record's block lines belong to a caller that restores a state with the block), store it again with `putForeign` and call `finishForeign`. Otherwise, when either family has a chain `cleanupOwned` lists, call `keepForeign(ctx, env, owner, known, nil, steps)` (its `next` is nil: DOCKER-USER ends as `-j RETURN` and the foreign rules). Otherwise call `steps` alone. In both calls discard the `interleaved` result and keep only the error: with no block line left, `Before` holds every foreign rule in its original chain order ahead of `-j RETURN`, so nothing is misplaced and the cleanup never ends a change `needs_repair` for it (a restore or revert that reloads the block reports interleaving itself);
````

## A15 — Task 37
Reason: the held-record repeat trusts only the recorded layout, so a foreign DOCKER-USER rule added after a cut-off cleanup is deleted (spec 2.6, Global Constraints).
Cost if wrong: each record carries the after files (a few KB); the IPv4 order in the new test may need adjusting if LayoutOf places blockless foreign rules differently.
### Old
````text
**Files:**
- Create: `internal/modules/firewall/docker_cleanup.go`
- Modify: `internal/modules/firewall/wire.go` (`productionFW`)
- Test: `internal/modules/firewall/docker_cleanup_test.go`
````
### New
````text
**Files:**
- Create: `internal/modules/firewall/docker_cleanup.go`
- Modify: `internal/modules/firewall/wire.go` (`productionFW`), `internal/modules/firewall/docker_preserve.go` (`foreignRecord`, `keepForeign`)
- Test: `internal/modules/firewall/docker_cleanup_test.go`

**Repeating a cleanup keeps rules added since the first attempt.** Spec 2.6 says no automatic operation discards a foreign rule, and its harness adds one during an interrupted cleanup. A repeat therefore reads `DOCKER-USER` again. To tell the block's leftover lines from new foreign rules, it judges the chain against the files the first attempt knew, which the record now keeps. This supersedes the held branch of Step 3 item 4.5 and the two-field `foreignRecord` of Contracts "Foreign-rule preservation":

1. `docker_preserve.go`: `foreignRecord` gains `Known [][]byte` with the JSON tag `known,omitempty`. Its comment: these are the after files whose block lines count as Darkpanel's, kept because a repeat may find the files on disk without the block. In `keepForeign`, read `foreignOf(owner)` before `blockUserRules`. `files` is `known` followed by each entry of the earlier record's `Known` that is not `bytes.Equal` to an entry already in `files`. Compute `rules` with `blockUserRules(files)` in place of `blockUserRules(known)`, and store `foreignRecord{Layouts: layouts, Block: next, Known: files}`. Nothing else in the file changes: `mergeLayouts`, `finishForeign` and `reloadDockerUser` stay as they are, and every `TestKf…` test must still pass unchanged.
2. `runCleanup` step 4.5 becomes this. When a record is held, set `rules` to `blockUserRules(known followed by rec.Known)` (failure: `firewall: cleanup: <cause>`); `verifyCleanup` uses this `rules`. When a record is held, or either family has a chain `cleanupOwned` lists, call `keepForeign(ctx, env, owner, known, nil, steps)`; otherwise call `steps` alone. The held branch no longer runs `steps`, sets `Block` to nil, calls `putForeign` or calls `finishForeign` itself. `keepForeign` merges the recorded layout with what the chain holds now, judged against the recorded files, and stores `Block` nil because `next` is nil. Discard the `interleaved` result, keep only the error, as before. Update the code comment on the held case to match.
3. In Step 1's `TestClFinishCleanups`, add `known := r.known()` directly after `r.standalone()` (before the files are rewritten), and add the field `Known: known,` to the `foreignRecord` literal after `Block: kfNext(t),`. Change nothing else in that test.
4. Add this test to `docker_cleanup_test.go` directly after `TestClFailedLoadIsFinished`. Write it first and see it fail on the IPv4 or IPv6 `DOCKER-USER` comparison:

```go
// TestClLateForeignKept: foreign rules added after a cut-off cleanup, one
// in a family already cleaned and one in a family still holding the
// block's lines, are kept by the repeat, after the rule the first attempt
// recorded, and the block's lines are not taken for foreign rules.
func TestClLateForeignKept(t *testing.T) {
	ctx := context.Background()
	const late6 = "-A DOCKER-USER -s 2001:db8::/32 -j DROP"
	r := kfHost(t, kfJoin(kfOne(kfForeign1), kfLines(t, fwstate.V4)), nil)
	r.standalone()
	inner, broken := r.h.onRun, true
	r.h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if broken && cmd.Path == "/usr/sbin/ip6tables-nft-restore" {
			return core.CmdResult{ExitCode: 1, Stderr: []byte("ip6tables-restore: resource problem")}
		}
		return inner(h, cmd)
	}
	if err := r.f.cleanupChains(ctx, r.env, "c1", cleanupPreserve); err == nil {
		t.Fatal("cleanupChains = nil, want the failed IPv6 load")
	}
	broken = false
	r.flush(t, fwstate.V4, kfJoin(kfOne(kfForeign2), r.loadedLines(fwstate.V4)))
	r.flush(t, fwstate.V6, kfJoin(kfOne(late6), r.loadedLines(fwstate.V6)))
	if err := r.f.finishCleanups(ctx, r.env); err != nil {
		t.Fatal(err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), kfOne(kfForeign2), kfOne(kfReturn)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if got, want := r.loadedLines(fwstate.V6), kfJoin(kfOne(late6), kfOne(kfReturn)); !slices.Equal(got, want) {
		t.Errorf("IPv6 DOCKER-USER = %q, want %q", got, want)
	}
	if got := clChains(r); len(got) != 0 {
		t.Errorf("chains %q are still loaded", got)
	}
	if got := clPending(t, r); len(got) != 0 || kfRecordHeld(t, r, "c1") {
		t.Errorf("intents %q or a record left", got)
	}
}
```

Step 4 also expects `TestClLateForeignKept` to pass. Step 6's commit also stages the extra file: `git add internal/modules/firewall/docker_cleanup.go internal/modules/firewall/wire.go internal/modules/firewall/docker_preserve.go internal/modules/firewall/docker_cleanup_test.go`.
````

## A16 — Task 38
Reason: the contract order strips the block from the after files before the cleanup reads them, so every cleanup change refuses on a real host; the cleanup must run first.
Cost if wrong: a restore that has `cleanup` set journals the cleanup before the restore's files; option (a), passing `cfg0`'s files into `cleanupChains`, would then be needed.
### Old
````text
1. `fw.run` (`handler.go`) runs these steps in this order and stops at the first error: a restore's files (`writeRestored`, unchanged); `f.runLogical(ctx, env, p.c)`; `f.writeBlockFiles(ctx, env, p.c.files)`; the change's ufw commands (the existing loop over `p.c.ops` with `runUfw`); when `p.c.cleanup`, `f.cleanupChains(ctx, env, env.ChangeID, cleanupRefuse)`; the transition commands (the existing loop over `p.tg.cmds`); a restore's sysctl values (`restoreSysctl`, unchanged). Rewrite `run`'s doc comment to list this order. A change with none of the Docker fields runs exactly as before.
````
### New
````text
1. `fw.run` (`handler.go`) runs these steps in this order and stops at the first error: when `p.c.cleanup`, `f.cleanupChains(ctx, env, env.ChangeID, cleanupRefuse)`; a restore's files (`writeRestored`, unchanged); `f.runLogical(ctx, env, p.c)`; `f.writeBlockFiles(ctx, env, p.c.files)`; the change's ufw commands (the existing loop over `p.c.ops` with `runUfw`); the transition commands (the existing loop over `p.tg.cmds`); a restore's sysctl values (`restoreSysctl`, unchanged). Rewrite `run`'s doc comment to list this order. A change with none of the Docker fields runs exactly as before.
   - Why the cleanup comes first: `runCleanup` learns which `DOCKER-USER` rules are the block's from the after files on disk, and it removes the block from those files itself as `firewall.file:` effects. If it ran after a write that already removed the block (`writeBlockFiles` for `firewall.docker.disable` and `firewall.state.disable`, `writeRestored` for a `firewall.snapshot.restore` that turns the integration off), it would take every block line for a foreign rule and refuse with `firewall.docker_user_in_use` on every real host. Run first, it also refuses before anything is stored, and `writeBlockFiles` then skips files the cleanup already left as wanted. This supersedes the order in Contracts "change and target" ("the `files` writes (`writeFileEffect`); the ufw ops; the cleanup's chain effect; …"). Give this reason in one code comment in `run`, without naming a task or the spec. Do not change `docker_cleanup.go` or `docker_preserve.go`.
   - Step 1's test file changes in the places below and is otherwise exactly Step 1's content. In the `const` block, `druBlock` and `druLoaded` each gain a conditional `DOCKER-USER` line, which `dockerfw.Foreign` reports unless the files still hold the block. Replace their definitions with:

     ```go
     	druBlock  = "# BEGIN DARKPANEL docker\n*filter\n:DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
     		"-A DOCKER-USER -o docker0 -j darkpanel-docker-deny\n-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n# END DARKPANEL docker\n"
     	// druLoaded is a loaded filter table that holds the block's chains.
     	druLoaded = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
     		":DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
     		"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n" +
     		"-A DOCKER-USER -o docker0 -j darkpanel-docker-deny\n-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"
     ```

     Replace `TestDruRunOrder`'s three-line doc comment with:

     ```go
     // TestDruRunOrder: a change that carries the Docker fields runs the cleanup
     // of the loaded chains first, while the after files still hold the block,
     // then the logical rows and settings, the block files, the ufw commands and
     // the transition.
     ```

     Replace its `want` literal with:

     ```go
     	want := []string{
     		"firewall.file:/etc/ufw/after.rules", "firewall.file:/etc/ufw/after6.rules", "firewall.chains:docker",
     		"firewall.setting:docker", "firewall.setting:sources", "firewall.container:" + druOld.ID, "firewall.container:" + druNew.ID,
     		"firewall.rule:" + rule.ID, "firewall.state:enabled",
     	}
     ```

     Keep the `file, del := …` and `clean, reload := …` lines, and replace the `if` that follows them with:

     ```go
     	if file < 0 || clean <= file || del <= clean || reload <= del {
     		t.Errorf("calls %q: want the block file, the chain cleanup, the ufw rule delete and the reload in that order", h.calls)
     	}
     ```

   - In a fix round, change the test file first and run Step 2's command. At the current head, `TestDruRunOrder` must fail with `firewall.docker_user_in_use` naming `-o docker0`. Then change `run` and its doc comment; Step 4's command passes, and Step 5 runs as written.
````

## A17 — Task 40
Reason: the cleanup learns the block's DOCKER-USER lines from the after files, so it must run before the snapshot's block-free files are written, as A16 did for fw.run.
Cost if wrong: one extra `ufw reload` outside a change, against the current files, before the restore's own transition.
### Old
````text
3. `restoreOver` (`restore.go`). Use `fw{d: d}` for the fw methods. The owner is `"restore:" + snap.ID`. In this order: after the restore record is settled and **before** `readLoaded` reads `before`, set `rep.Interleaved` from `finishRecorded` (a leftover record's reload must be in the loaded state `before` holds, or the plan would not see the foreign rules). Keep the IPv6 refusal. Before the first file write, build the plan: the files on disk now come from `f.d.readConfig(f.d.root)` (an unreadable configuration counts as no files), the files afterwards from the snapshot's present entries, enabled from `snap.State.Enabled`, IPv6 from `snapshotStart(snap)` (an error counts as off), flush true. Write the entries as before. Wrap the existing transition (the before-image load, or the `ufw` transition command, with its errors and `rep.Excluded` unchanged) in a closure, then: when `cleanup`, call `d.cleanChains(ctx, env, owner)` with the owner `env.ChangeID`, or `"uninstall"` when it is empty (a nil `d.cleanChains` is the error `firewall: restore: the loaded Docker chains need a cleanup, and none is configured`), re-read the loaded state into `before`, then run the closure; when `keep`, call `f.keepForeign(ctx, env, "restore:"+snap.ID, known, next, closure)` and, when it reports interleaved, set `rep.Interleaved` to the plan's `order`; otherwise just run the closure. Verification calls `verifyRestore` with `without(before)` and `without(after)`. Nothing else in `restoreOver` changes, and a restore whose host holds no foreign rule and no loaded owned chain runs no extra program.
````
### New
````text
3. `restoreOver` (`restore.go`). Use `fw{d: d}` for the fw methods. The owner is `"restore:" + snap.ID`. In this order: after the restore record is settled and **before** `readLoaded` reads `before`, set `rep.Interleaved` from `finishRecorded` (a leftover record's reload must be in the loaded state `before` holds, or the plan would not see the foreign rules). Keep the IPv6 refusal. Before the first file write, build the plan: the files on disk now come from `f.d.readConfig(f.d.root)` (an unreadable configuration counts as no files), the files afterwards from the snapshot's present entries, enabled from `snap.State.Enabled`, IPv6 from `snapshotStart(snap)` (an error counts as off), flush true. Then, still before the first file write, when `cleanup`, call `d.cleanChains(ctx, env, owner)` with the owner `env.ChangeID`, or `"uninstall"` when it is empty (a nil `d.cleanChains` is the error `firewall: restore: the loaded Docker chains need a cleanup, and none is configured`), and re-read the loaded state into `before`. Give the reason in one code comment, without naming a task or the spec: the cleanup learns which `DOCKER-USER` rules are the block's from the after files on disk, so run after the snapshot's block-free files are written it would keep the block's own lines as foreign rules and reload jumps to the chains it has just deleted; run first, it also leaves the snapshot's files, block or not, to be written by the restore itself. Write the entries as before. Wrap the existing transition (the before-image load, or the `ufw` transition command, with its errors and `rep.Excluded` unchanged) in a closure, then: when `keep`, call `f.keepForeign(ctx, env, "restore:"+snap.ID, known, next, closure)` and, when it reports interleaved, set `rep.Interleaved` to the plan's `order`; otherwise (including when `cleanup`) just run the closure. Verification calls `verifyRestore` with `without(before)` and `without(after)`. Nothing else in `restoreOver` changes, and a restore whose host holds no foreign rule and no loaded owned chain runs no extra program.
   - Step 1's test file changes in one place and is otherwise exactly Step 1's content. In `TestKfwRestore`, replace the `if` that checks `"clean c1"` against `"run /usr/sbin/ufw reload"` (three lines) with:

     ```go
     			if c.wantClean && (kfwIndex(h.calls, "clean c1") > kfwIndex(h.calls, "run /usr/sbin/ufw reload") ||
     				kfwIndex(h.calls, "clean c1") > kfwIndex(h.calls, "restore /etc/")) {
     				t.Errorf("calls %q, want the cleanup before the first file is written and before the transition", h.calls)
     			}
     ```

     With the cleanup placed after the file writes, the case "the snapshot has no block and the chains are loaded" fails on this check.
````

## A18 — Task 40
Reason: a revert by inverses undoes the chains effect (`ufw reload`) before the file effects that put the block back, so nothing loads the block afterwards unless a rule, default or logging effect was also undone.
Cost if wrong: one extra `ufw reload` in a revert that undoes the chains effect; if the new subtest's fake-host assumptions differ, a fix round adjusts the subtest.
### Old
````text
4. `revert` (`revert.go`). Right after the snapshot is read and attached, call `finishRecorded(ctx, env, "revert:"+env.ChangeID)`. In the whole-restore branch, when `restore` succeeds with a non-empty `rep.Interleaved`, treat it as the failure `interleavedError(rep.Interleaved)` (it flows into `writeErr`, so the snapshot is pinned, the connectivity exception still applies and the change becomes `needs_repair`); with an empty one the branch returns as before. Move the inverses branch's body (`undoNewestFirst`, then for a before-image snapshot the changed-files check and `loadVerifiedImage`) into `undoKeepingForeign(ctx, env, snap, made, conflicts)` on `fw`, which builds a plan and wraps that body. It reads the configuration and loaded state (when either cannot be read it logs a warning and just runs the body). The files afterwards are the files now, except that a made, non-conflicting `firewall.file:<path>` effect for an after file makes that file the snapshot's entry; UFW is enabled afterwards as now, except that a made, non-conflicting `firewall.state:enabled` effect makes it `snap.State.Enabled`; the transition rewrites `DOCKER-USER` when any made, non-conflicting effect has the noun `firewall.rule`, `firewall.default`, `firewall.logging`, `firewall.state` or `firewall.chains`; IPv6 is the configuration's. When the plan is not `keep` it runs the body; otherwise `f.keepForeign(ctx, env, "revert:"+env.ChangeID, known, next, body)`, and an interleaved result becomes `interleavedError(order)` when the body did not fail.
````
### New
````text
4. `revert` (`revert.go`). Right after the snapshot is read and attached, call `finishRecorded(ctx, env, "revert:"+env.ChangeID)`. In the whole-restore branch, when `restore` succeeds with a non-empty `rep.Interleaved`, treat it as the failure `interleavedError(rep.Interleaved)` (it flows into `writeErr`, so the snapshot is pinned, the connectivity exception still applies and the change becomes `needs_repair`); with an empty one the branch returns as before. Move the inverses branch's body (`undoNewestFirst`, then for a before-image snapshot the changed-files check and `loadVerifiedImage`) into `undoKeepingForeign(ctx, env, snap, made, conflicts)` on `fw`, which builds a plan and wraps that body. It reads the configuration and loaded state (when either cannot be read it logs a warning and just runs the body). The files afterwards are the files now, except that a made, non-conflicting `firewall.file:<path>` effect for an after file makes that file the snapshot's entry; UFW is enabled afterwards as now, except that a made, non-conflicting `firewall.state:enabled` effect makes it `snap.State.Enabled`; the transition rewrites `DOCKER-USER` when any made, non-conflicting effect has the noun `firewall.rule`, `firewall.default`, `firewall.logging`, `firewall.state` or `firewall.chains`; IPv6 is the configuration's. When the plan is not `keep` it runs the body; otherwise `f.keepForeign(ctx, env, "revert:"+env.ChangeID, known, next, body)`, and an interleaved result becomes `interleavedError(order)` when the body did not fail.
   - The body, right after `undoNewestFirst` returns nil and before the before-image steps, calls wave 1's `reloadIfActive(ctx, env, f.d)` when a made, non-conflicting effect has the resource `firewall.chains:docker` and no made, non-conflicting effect satisfies `ufwEffect` (`undoNewestFirst` has then reloaded last already). Give the reason in one code comment, without naming a task or the spec: the cleanup journals its file writes before the chains effect, so undoing newest first runs the chains effect's `ufw reload` while the files still lack the block, and only the file inverses after it put the block back. The reload is inside the body, so `keepForeign` reloads `DOCKER-USER` after the block's chains exist again. Do not change `undoNewestFirst`, `ufwEffect` or `docker_cleanup.go`.
   - Step 1's test file gains this subtest in `TestKfwRevert`, directly before the subtest named "an interrupted restore, finished", and is otherwise exactly Step 1's content. Without the reload it fails on `DOCKER-USER holds ["-A DOCKER-USER -j RETURN"]`:

     ```go
     	t.Run("inverses of a cleanup", func(t *testing.T) {
     		h, d := kfwHost(t, true)
     		env := h.env()
     		raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, false)
     		if err != nil {
     			t.Fatal(err)
     		}
     		if _, err := attach(ctx, env); err != nil {
     			t.Fatal(err)
     		}
     		load := h.onRun
     		h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
     			if strings.Contains(string(cmd.Stdin), "-X darkpanel-docker-deny") {
     				kfwSet(t, h, []string{kfwReturn}, false)
     				return core.CmdResult{}
     			}
     			return load(h, cmd)
     		}
     		if err := (fw{d: d}).cleanupChains(ctx, env, "c1", cleanupPreserve); err != nil {
     			t.Fatal(err)
     		}
     		if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
     			t.Fatal(err)
     		}
     		h.calls, h.stdin = nil, map[string][]byte{}
     		rvTestManualEdit(h)
     		out, err := revert(ctx, h.env(), d, []byte(kfwSnapshotID(t, h)))
     		if err != nil || len(out.Effects) == 0 {
     			t.Fatalf("revert = %+v, %v, want the cleanup's effects undone", out, err)
     		}
     		for _, e := range out.Effects {
     			if e.State != core.EffectUndone {
     				t.Errorf("effect %+v, want it undone", e)
     			}
     		}
     		owned := h.loaded.V4.Tables[0].Chain("darkpanel-docker-deny") != nil
     		if got := kfwChain(h); !slices.Equal(got, kfwBlockLines) || !owned {
     			t.Errorf("DOCKER-USER holds %q (block chain loaded: %v), want the block's lines with its chain loaded again", got, owned)
     		}
     	})
     ```
````

## A19 — Task 39
Reason: Step 3 item 5 assigns the reload decision to the cleanup, but the cleanup runs `ufw reload` on a host without the ufw binary whenever UFW's chains are still loaded, so the uninstall fails there.
Cost if wrong: on a host that has a non-regular `/usr/sbin/ufw` while UFW is active, the cleanup skips its reload outside a change; `Uninstall` already refuses that host.
### Old
````text
**Files:**
- Modify: `internal/modules/firewall/uninstall.go` (`module.Uninstall` and its doc comment)
- Test: `internal/modules/firewall/uninstall_docker_test.go`
````
### New
````text
**Files:**
- Modify: `internal/modules/firewall/uninstall.go` (`module.Uninstall` and its doc comment), `internal/modules/firewall/docker_cleanup.go` (`cleanupSteps`, the reload condition only)
- Test: `internal/modules/firewall/uninstall_docker_test.go`, `internal/modules/firewall/docker_preserve_test.go` (`kfHost`, one line)

**The cleanup reloads UFW only when its program exists.** UFW's chains stay loaded after a stop, and after the package is removed until a reboot. So `ufwRunning`, which reads only the loaded state, can be true on a host without `/usr/sbin/ufw`. The uninstall builds its `core.Env` without `Effects`, so `cleanupSteps` reloads there, and that exec fails after the cleanup has finished. Step 3 item 5 still holds for `uninstall.go`: its new code calls no ufw and adds no `cleanupChains` parameter. The reload decision stays in the cleanup, and the cleanup also checks that the program exists:

1. In `uninstall_docker_test.go`, in `TestDunRunsCleanup`, directly after the closing brace of the `if deleted != c.wantDelete { … }` block, add:

   ```go
   			if c.noUFW && len(unRan(h, 0)) > 0 {
   				t.Errorf("ufw ran %q on a host without UFW", unRan(h, 0))
   			}
   ```

   Run `TMPDIR=/root/.cache/w2-tmp/39 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestDun' ./internal/modules/firewall/` and see `TestDunRunsCleanup/remove_without_UFW` fail with `ufw ran ["reload"] on a host without UFW`.
2. In `docker_cleanup.go`, `cleanupSteps`, change the reload condition to `if changed && env.Effects == nil && ufwRunning(before) && f.regular(ufwPath) == nil {`, and leave its body unchanged. Directly above it, add one code comment that names no task or spec: UFW's chains stay loaded after its package is removed, until a reboot, so the loaded state alone does not show that its program exists. Change nothing else in the file.
3. In `docker_preserve_test.go`, in `kfHost`, directly after the line `h.ufw["reload"] = func(*fhState) int { return 0 }`, add `h.writeFile(ufwPath, []byte("#!/bin/sh\n"))`. The rig models a host with UFW installed. Without this line, `TestClCleanup` loses its expected `run /usr/sbin/ufw reload`. Change nothing else in that file.
4. Then run `TMPDIR=/root/.cache/w2-tmp/39 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestDun|TestUninstall|TestCl|TestKf' ./internal/modules/firewall/`. Every test passes, and `TestClCleanup` still expects the reload. Step 5 runs as written. Its commit also stages `internal/modules/firewall/docker_cleanup.go` and `internal/modules/firewall/docker_preserve_test.go`.
````

## A20 — Task 40
Reason: undoing newest first runs the chains effect's reload before the cleanup's file inverses restore the block, so its own check fails and A18's later reload never runs; a failed body also strands the foreign rules in the revert's record.
Cost if wrong: if spec 2.9's "reverse order" is binding even within one cleanup step, inverse.go's "loaded" case must instead load the block's chains itself (Task 27's file), and kfwUndoOrder is dropped.
### Old
````text
   - The body, right after `undoNewestFirst` returns nil and before the before-image steps, calls wave 1's `reloadIfActive(ctx, env, f.d)` when a made, non-conflicting effect has the resource `firewall.chains:docker` and no made, non-conflicting effect satisfies `ufwEffect` (`undoNewestFirst` has then reloaded last already). Give the reason in one code comment, without naming a task or the spec: the cleanup journals its file writes before the chains effect, so undoing newest first runs the chains effect's `ufw reload` while the files still lack the block, and only the file inverses after it put the block back. The reload is inside the body, so `keepForeign` reloads `DOCKER-USER` after the block's chains exist again. Do not change `undoNewestFirst`, `ufwEffect` or `docker_cleanup.go`.
   - Step 1's test file gains this subtest in `TestKfwRevert`, directly before the subtest named "an interrupted restore, finished", and is otherwise exactly Step 1's content. Without the reload it fails on `DOCKER-USER holds ["-A DOCKER-USER -j RETURN"]`:
````
### New
````text
   - The body passes `undoNewestFirst` the slice `kfwUndoOrder(made)` instead of `made`. `kfwUndoOrder(made []core.Effect) []core.Effect` (in `docker_revert.go`) returns a new slice and never modifies its argument. In that slice, each `firewall.chains:docker` effect whose Before decodes with `beforeValue` to `"loaded"` moves ahead of the run of `firewall.file:/etc/ufw/after.rules` and `firewall.file:/etc/ufw/after6.rules` effects directly before it in `made`. Every other effect, including a `firewall.chains:docker` effect whose Before is `"absent"`, keeps its place. Undone newest first, the cleanup's file inverses then put the block back before the chains inverse's `ufw reload` loads its chains. Give the reason in one code comment, without naming a task or the spec: a cleanup journals its file writes before its chains effect, so undone in plain reverse order the chains effect's reload runs while the files still lack the block, and the inverse's own check of the loaded chains fails. A chains effect that loaded the block keeps its place because its inverse, the cleanup, reads the block from the files. The body calls no extra `reloadIfActive`. The plan (`undone`, flush, the files afterwards) is computed from `made` as before; only the order handed to `undoNewestFirst` changes. Do not change `undoNewestFirst`, `ufwEffect`, `inverse.go` or `docker_cleanup.go`.
   - When `keepForeign` returns an error, `undoKeepingForeign` calls `f.finishRecorded(ctx, env, "revert:"+env.ChangeID)` before it returns that error. Give the reason in one code comment, without naming a task: a failed body leaves the flushed foreign rules in this owner's record, which only a later revert of the same change would replay, and the emergency restore and a repair's restore keep foreign rules under their own owner, so they would not see these.
   - Step 1's test file gains two subtests in `TestKfwRevert`, directly before the subtest named "an interrupted restore, finished", in this order. Apart from these subtests and item 3's change, it is exactly Step 1's content. The first subtest fails on `the revert:c1 record is left` without the `finishRecorded` call:

     ```go
     	t.Run("a failed inverse loads the foreign rules back", func(t *testing.T) {
     		h, d, _ := kfwRevertHost(t)
     		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
     		rvTestManualEdit(h)
     		h.ufw["default allow outgoing"] = func(*fhState) int { return 1 }
     		if _, err := revert(ctx, h.env(), d, []byte(kfwSnapshotID(t, h))); err == nil {
     			t.Fatal("revert succeeded, want the failed inverse reported")
     		}
     		if _, ok, err := foreignOf(ctx, h.db, "revert:c1"); err != nil || ok {
     			t.Errorf("the revert:c1 record is left (%v, %v)", ok, err)
     		}
     		if got, want := kfwChain(h), append(slices.Clone(kfwBlockLines), kfwForeign); !slices.Equal(got, want) {
     			t.Errorf("DOCKER-USER holds %q, want %q", got, want)
     		}
     	})
     ```

     The second subtest fails without `kfwUndoOrder`, with `undo effect 3 (firewall.chains:docker) left "absent", want "loaded"`:
````

## A21 — Task 40
Reason: a cleanup run inside a revert journals new effects in the change being reverted, so every such revert ends needs_repair.
Cost if wrong: the cleanup inside a revert is recorded only by its intent row; one extra `ufw reload` of the current files per such revert.
### Old
````text
**Files:**
- Create: `internal/modules/firewall/docker_revert.go`
- Modify: `internal/modules/firewall/restore.go` (`restoreReport`, `restoreOver`), `internal/modules/firewall/revert.go` (`overrideDetail`, `revert`, `finishRestore`, `emergencyRestore`)
- Test: `internal/modules/firewall/docker_revert_test.go`
````
### New
````text
**Files:**
- Create: `internal/modules/firewall/docker_revert.go`
- Modify: `internal/modules/firewall/restore.go` (`restoreReport`, `restoreOver`), `internal/modules/firewall/revert.go` (`overrideDetail`, `revert`, `finishRestore`, `emergencyRestore`), `internal/modules/firewall/inverse.go` (`undoDocker`, the `"absent"` case only)
- Test: `internal/modules/firewall/docker_revert_test.go`

**A cleanup inside a revert journals nothing.** The engine's revert settles only the effects it read before it began. Any effect the owned-chain cleanup journals during the revert stays `made`, and the change ends `needs_repair` with "the revert left effects in place". Both callers of `d.cleanChains` run inside a revert, so each one passes a copy of `env` whose `Effects` is nil. With `Effects` nil, `cleanupSteps` writes the files through `env.Files.Restore`, reloads an active UFW itself, and the cleanup's recorded intent still makes a cut-off run repeatable. This supersedes "call `d.cleanChains(ctx, env, owner)`" in Step 3 item 3, and the words "`inverse.go` or" in Step 3 item 4's "Do not change …" sentence, for this one edit only. `docker_cleanup.go`, `undoNewestFirst`, `ufwEffect` and `kfwUndoOrder` stay unchanged.

1. `restore.go`, `restoreOver`: in the `kp.cleanup` branch, build `clean := env` and `clean.Effects = nil`, and call `d.cleanChains(ctx, clean, cleanOwner)`. Everything else in the branch (the owner, the nil-field error, the re-read of `before` with `env`) is unchanged. Keep the existing comment and add one sentence to it: a revert settles only the effects it read before it began, so an effect journaled here would stay made and leave the change needing repair.
2. `inverse.go`, `undoDocker`, `case "absent":`: build `clean := env` and `clean.Effects = nil`, and return `d.cleanChains(ctx, clean, env.ChangeID)`. Add a one-line comment with the same reason. Nothing else in `inverse.go` changes.
3. Add to `docker_revert_test.go`, after `TestKfwEmergencyOrder`. Write it first and see it fail on `the journal holds … effects` in both subtests. If a fake-host detail stops a subtest before its assertions, change only the setup lines, never the assertions, and say so in the report:

```go
// TestKfwCleanupUnjournaled: the owned-chain cleanup that a restore and the
// undo of a chains effect run inside a revert journals nothing in the change
// being reverted, removes the block's chains and keeps the foreign rule.
func TestKfwCleanupUnjournaled(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, h *fakeHost, env core.Env, n int) {
		t.Helper()
		if got := kfwJournaled(t, env); got != n {
			t.Errorf("the journal holds %d effects after the cleanup, want %d", got, n)
		}
		if got := kfwChain(h); !slices.Contains(got, kfwForeign) || h.loaded.V4.Tables[0].Chain("darkpanel-docker-deny") != nil {
			t.Errorf("DOCKER-USER holds %q, want the foreign rule kept and the block's chains removed", got)
		}
	}
	t.Run("a restore", func(t *testing.T) {
		h, d := kfwHost(t, false)
		snap := kfwSnapshot(t, h, d)
		kfwFiles(h, true)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		rvTestManualEdit(h)
		kfwCleanReal(t, h, &d)
		env := h.env()
		n := kfwJournaled(t, env)
		if _, err := restore(ctx, env, d, snap); err != nil {
			t.Fatal(err)
		}
		check(t, h, env, n)
	})
	t.Run("the undo of a chains effect", func(t *testing.T) {
		h, d := kfwHost(t, true)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		kfwCleanReal(t, h, &d)
		env := h.env()
		n := kfwJournaled(t, env)
		e := core.Effect{Seq: 1, Kind: core.EffectModify, Resource: deResourceChains,
			Before: core.StateDoc{State: json.RawMessage(`"absent"`)}}
		if err := undoDocker(ctx, env, d, nounChains, "docker", e); err != nil {
			t.Fatal(err)
		}
		check(t, h, env, n)
	})
}

// kfwCleanReal gives d the production cleanup and makes the fake host drop
// the block's chains, leaving -j RETURN, when the cleanup's input deletes
// them.
func kfwCleanReal(t *testing.T, h *fakeHost, d *deps) {
	t.Helper()
	load := h.onRun
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if strings.Contains(string(cmd.Stdin), "-X darkpanel-docker-deny") {
			kfwSet(t, h, []string{kfwReturn}, false)
			return core.CmdResult{}
		}
		return load(h, cmd)
	}
	d.cleanChains = fw{d: *d}.cleanChainsPreserving
}

// kfwJournaled counts the effects env's journal holds.
func kfwJournaled(t *testing.T, env core.Env) int {
	t.Helper()
	effects, err := env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(effects)
}
```

Step 4's command covers the new test. In Step 5, the `git add` line also stages `internal/modules/firewall/inverse.go`. The fix round commits with `fix(firewall): run revert cleanups unjournaled`.
````

## A22 — Task 24
Reason: No test case runs a delete before a non-zero Insert, so the post-delete numbering of step 3.5 and 3.6 is not pinned.
Cost if wrong: Two extra table rows in ruleops_test.go; skipping them leaves the delete-then-insert numbering untested.
### Old
````text
		{
			name:    "an IPv4 rule inserted ahead of IPv6 rules keeps its IPv4 number",
			current: []model.Rule{roManual("22"), roHave(roRule6(model.ActionAllow, model.Any, 7), 107)},
			desired: []model.Rule{allow, roRule6(model.ActionAllow, model.Any, 7)},
			want:    []RuleOp{roAdd(allow, 500, 0)},
		},
````
### New
````text
		{
			name:    "an IPv4 rule inserted ahead of IPv6 rules keeps its IPv4 number",
			current: []model.Rule{roManual("22"), roHave(roRule6(model.ActionAllow, model.Any, 7), 107)},
			desired: []model.Rule{allow, roRule6(model.ActionAllow, model.Any, 7)},
			want:    []RuleOp{roAdd(allow, 500, 0)},
		},
		{
			name:    "an insert is numbered after the deletes before it have run",
			current: []model.Rule{roManual("22"), roHave(allow, 101), roHave(deny, 103)},
			desired: []model.Rule{other, deny},
			want:    []RuleOp{roDel(101), roAdd(other, 500, 2)},
		},
		{
			name: "an IPv6 insert counts the IPv4 rules left after the deletes",
			current: []model.Rule{
				roManual("22"), roHave(allow, 101),
				roHave(roRule6(model.ActionAllow, model.Any, 7), 107),
			},
			desired: []model.Rule{roRule6(model.ActionDeny, "2001:db8::/32", 9), roRule6(model.ActionAllow, model.Any, 7)},
			want:    []RuleOp{roDel(101), roAdd(roRule6(model.ActionDeny, "2001:db8::/32", 9), 500, 2)},
		},
````

## A23 — Task 42
Reason: disable reads the full Docker state, so a reachable but failing daemon refuses the integration's only way out, against Task 44's own rule that turning off must not depend on the daemon.
Cost if wrong: disable no longer reports a daemon failure; it never used the daemon's data.
### Old
````text
5. `func (f fw) dockerEnable() core.ActionDef` is `defineMutation(core.ActionSpec{Name: "firewall.docker.enable", Summary: "Enable the Docker integration."}, mutation[DockerEnableInput]{…})` with `f: f`, `guarded` returning `in.Guarded`, no `manual`, `build` returning a `buildFunc` that calls `f.dockerState(ctx, env, cur)` with the `ctx` and `env` of `build` and the `cur` it is given, returns that error as it is, and otherwise `dockerEnableChange(env, cfg, cur, st)`, and `check` calling `dockerFlagCheck(ctx, env, cfg, true)`. `dockerDisable` is the same with the name `firewall.docker.disable`, summary `Disable the Docker integration.`, `dockerDisableChange(env, cfg, st)` and `dockerFlagCheck(…, false)`.
````
### New
````text
5. `func (f fw) dockerEnable() core.ActionDef` is `defineMutation(core.ActionSpec{Name: "firewall.docker.enable", Summary: "Enable the Docker integration."}, mutation[DockerEnableInput]{…})` with `f: f`, `guarded` returning `in.Guarded`, no `manual`, `build` returning a `buildFunc` that calls `f.dockerState(ctx, env, cur)` with the `ctx` and `env` of `build` and the `cur` it is given, returns that error as it is, and otherwise `dockerEnableChange(env, cfg, cur, st)`, and `check` calling `dockerFlagCheck(ctx, env, cfg, true)`. `dockerDisable` is the same with the name `firewall.docker.disable`, summary `Disable the Docker integration.` and `dockerFlagCheck(…, false)`, except that its `buildFunc` never calls `f.dockerState`. It reads `dc, err := readDockerConfig(ctx, env.DB)`, returns that error as it is, and otherwise returns `dockerDisableChange(env, cfg, dockerState{cfg: dc})`. Its `cur` parameter is `_`. One code comment, naming no task or spec, gives the reason: removing the block needs no facts, and turning the integration off must not depend on the daemon answering, so a daemon that fails without being unreachable does not refuse it.
   - Step 1's test file changes in one line and is otherwise exactly Step 1's content. In `TestDenDisablePlan`, the line `d.err = fmt.Errorf("dial unix /var/run/docker.sock: %w", dockerapi.ErrUnavailable)` becomes `d.err = fmt.Errorf("docker: status %d", 500)`. A daemon that answers with an error other than unreachable must not refuse the disable. With `f.dockerState` in the build, the test fails with `firewall: docker: docker: status 500`.
````

## A24 — Task 46
Reason: `drpCustom` leaves `ForwardExemptions` nil, but the restore stores settings through `sourcesOp`, which makes nil lists empty, so `TestDrpRestoreAppliesState`'s `reflect.DeepEqual(cfg.Settings, drpCustom)` cannot pass; Task 45 hit the same failure and fixed its `dsrCustom` the same way.
Cost if wrong: Task 46's Step 4 fails on the settings assertion and the implementer has to apply this one-line fixture change anyway.
### Old
````text
var drpCustom = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}
````
### New
````text
// ForwardExemptions is empty, not nil: a restore stores settings with nil
// lists made empty, so a nil list would not read back equal.
var drpCustom = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ForwardExemptions: []dockerfw.Exemption{}}
````

## A25 — Header
Reason: Contracts gives `ExposureRow.Container` as an untagged pointer output, which `TestPointerOutputsAreNullable` rejects once Task 50 registers `firewall.exposure`; the tag is added without a comment so that Task 35's "exactly as Contracts" copy matches the code.
Cost if wrong: the published schema of `firewall.exposure` accepts a null the server never emits; the emitted JSON is unchanged.
### Old
````text
	Container *ExposureContainer  `json:"container,omitempty"`
````
### New
````text
	Container *ExposureContainer  `json:"container,omitempty" jsonschema:"nullable"`
````

## A26 — Task 50
Reason: registering `firewall.exposure` exposes Task 35's untagged pointer to the nullable guard. The fix (the tag and the regenerated snapshot) lands in Task 50, so its Files block must name the file.
Cost if wrong: a scope reviewer flags a one-line edit outside the Files block.
### Old
````text
- Modify: `internal/modules/firewall/firewall.go` (`actions`, `resources`, and a new `BackgroundTasks` method on `module`)
````
### New
````text
- Modify: `internal/modules/firewall/firewall.go` (`actions`, `resources`, and a new `BackgroundTasks` method on `module`)
- Modify: `internal/modules/firewall/exposure_resource.go` (only the `ExposureRow.Container` field's tag, which gains `jsonschema:"nullable"` as Contracts gives it; register row 70's `TestPointerOutputsAreNullable` checks it once `firewall.exposure` is registered). `schemas/firewall.exposure/v1.json` is generated after the tag is added. In a fix round where that snapshot is already committed, `git status --short schemas` lists only it as modified.
````

## A27 — Task 50
Reason: Step 5's commit must stage the tagged file together with the regenerated snapshot, or the committed tree fails `TestPointerOutputsAreNullable` or `TestActionHashesMatchTheEmbeddedSnapshots`.
Cost if wrong: none. Staging a required file is always correct.
### Old
````text
git add internal/modules/firewall/firewall.go internal/modules/firewall/firewall_test.go internal/modules/firewall/register_test.go schemas
````
### New
````text
git add internal/modules/firewall/firewall.go internal/modules/firewall/firewall_test.go internal/modules/firewall/register_test.go internal/modules/firewall/exposure_resource.go schemas
````

## A28 — Task 51
Reason: A Docker host forwards packets, and UFW loads the two IPv6 limit-chain rules that `user6.rules` declares. The capture's IPv6 save lacks those rules, so the in-memory host completes them; the capture files stay unchanged.
Cost if wrong: If a regenerated capture holds the rules, the guard skips them; if it holds them in another text form, the host shows a duplicate and the lockout guard reports it.
### Old
````text
		h.write("/"+rel, deeFile(t, "files/"+rel))
	}
	h.loaded = fwstate.Loaded{Variant: "nft", V4: deeSave(t, "v4"), V6: deeSave(t, "v6")}
````
### New
````text
		h.write("/"+rel, deeFile(t, "files/"+rel))
	}
	// A Docker host forwards packets; the golden scenario's values do not.
	for key := range h.sysctl {
		if key == "net/ipv4/ip_forward" || (strings.HasPrefix(key, "net/ipv6/conf/") && strings.HasSuffix(key, "/forwarding")) {
			h.sysctl[key] = "1"
		}
	}
	h.loaded = fwstate.Loaded{Variant: "nft", V4: deeSave(t, "v4"), V6: deeSave(t, "v6")}
	// The capture's IPv6 save lacks the last rule of ufw6-user-limit and of
	// ufw6-user-limit-accept, which user6.rules declares and UFW loads on a
	// working host; without them the host reads as out of sync with UFW.
	for _, line := range []string{"-A ufw6-user-limit -j REJECT --reject-with icmp6-port-unreachable", "-A ufw6-user-limit-accept -j ACCEPT"} {
		if !slices.Contains(deeTexts(h, fwstate.V6, strings.Fields(line)[1]), line) {
			if res := h.restore(fwstate.V6, []byte("*filter\n"+line+"\nCOMMIT\n")); res.ExitCode != 0 {
				t.Fatalf("add %q: %s", line, res.Stderr)
			}
		}
	}
````

## A29 — Task 51
Reason: Docker 29.9.1 creates `DOCKER-USER` empty, while spec 2.6 has the cleanup and an integration-off revert leave exactly `-j RETURN`. The two are equivalent for packets, so the comparison tolerates the difference in both families, as it already did for IPv6.
Cost if wrong: A spurious extra `-j RETURN` in `DOCKER-USER` after a revert is no longer caught. The cleanup's own verification (spec step 5) still pins the chain's content.
### Old
````text
// deeKernelDiffs compares the kernel's chains in two loaded states as a
// restore verifies them, except that an IPv6 DOCKER-USER chain holding only
// -j RETURN may exist in the later state alone: Docker makes none without
// IPv6 rules, and the block declares it.
func deeKernelDiffs(before, after fwstate.Loaded) []fwstate.ChainDiff {
	var out []fwstate.ChainDiff
	for _, d := range loaded.Compare(before, after, restoreKeep) {
		if d.Family == fwstate.V6 && d.Table == "filter" && d.Chain == "DOCKER-USER" && len(d.OnlyA) == 0 && slices.Equal(d.OnlyB, []string{"-j RETURN"}) {
			continue
		}
		out = append(out, d)
	}
	return out
}
````
### New
````text
// deeKernelDiffs compares the kernel's chains in two loaded states as a
// restore verifies them, except that DOCKER-USER, in either family, may hold
// one -j RETURN in the later state that the earlier one lacks: Docker 28 and
// later create the chain empty (and make none for IPv6 without IPv6 rules),
// while the block and the cleanup declare it ending in -j RETURN, and an
// empty chain returns as well.
func deeKernelDiffs(before, after fwstate.Loaded) []fwstate.ChainDiff {
	var out []fwstate.ChainDiff
	for _, d := range loaded.Compare(before, after, restoreKeep) {
		if d.Table == "filter" && d.Chain == "DOCKER-USER" && !slices.Contains(d.OnlyA, "-j RETURN") {
			if i := slices.Index(d.OnlyB, "-j RETURN"); i >= 0 {
				d.OnlyB = slices.Delete(slices.Clone(d.OnlyB), i, i+1)
			}
			if len(d.OnlyA) == 0 && len(d.OnlyB) == 0 {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}
````

## A30 — Task 51
Reason: 172.17.0.9 is in no subnet of the capture (docker0 is 172.18.0.0/16), so the recreated container is routed out of eth0 and the port reads `filtered`. The new address now comes from web's own address.
Cost if wrong: None as long as web's network is wider than a /29; the capture uses a /16.
### Old
````text
	next := netip.MustParseAddr("172.17.0.9")
````
### New
````text
	a := old.As4()
	next := netip.AddrFrom4([4]byte{a[0], a[1], a[2], 9})
````

## A31 — Task 54
Reason: E.1's fatal read is called only inside `eventually` checks, so one failed read ends the scenario instead of being retried.
Cost if wrong: none on a passing run; the three scenarios return one more error inside their checks.
### Old
````text
1. `m3dExpoRead(ctx, t, f) ([]byte, m3dExpoView)` reads `firewall.exposure` of the agent into a `json.RawMessage` with `f.api.Resource`, decodes it with `m3dExpoDecode` and fails the test on an error.
````
### New
````text
1. `m3dExpoRead(ctx, f) ([]byte, m3dExpoView, error)` reads `firewall.exposure` of the agent into a `json.RawMessage` with `f.api.Resource` and decodes it with `m3dExpoDecode`. It does not fail the test: a read error is returned as `harness: firewall.exposure: <cause>`, a decode error as `m3dExpoDecode` returns it. Every caller is an `eventually` check, which returns that error so the loop retries a read that fails while the agent reconciles; the scenario fails only through the `t.Fatalf` on `eventually`'s result. No `eventually` check calls `t.Fatal` or `t.Fatalf`.
````
