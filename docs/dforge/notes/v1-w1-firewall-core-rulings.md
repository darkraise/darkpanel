# v1 wave 1 firewall core — rulings and amendments

The executing sessions made the rulings below on the owner's behalf while running
`docs/dforge/plans/2026-10-24-v1-w1-firewall-core.md`, in the order they were
made, each with what it costs if wrong. The amendments follow in full. The run's
ledger and workspace are deleted with the worktree, so this file is the lasting
record.

Merged from branch `v1-w1-firewall-core` (merge base c7b795e, final head 2e07a58).
The final session's rulings are at the end of the list: clearing red gates 6 and 7
(CI and the real-VM harness), the final whole-branch review, and its fix wave.

## Rulings (ledger lines containing `Ruling:`)

```text
Ruling: worktree created from local main (c7b795e) at .worktrees/v1-w1-firewall-core, branch v1-w1-firewall-core, instead of the native tool's default origin/main base — origin/main lacks the 7 local plan commits — if wrong, the branch would lack the plan and spec
Ruling: finish choice merge into main recorded without asking — owner standing instruction of 2026-10-19 (memory darkpanel-finish-merge-locally) — none
Ruling: pre-flight — 2 findings, both PARK (predict.sortPorts beside model.sortPorts; Task 71 unprefixed yes/no), see preflight.md — none
Task 1: Ruling: preflight-108 — PARK; Task 46 keeps its own sortPorts — a missed paired change shows as a golden or prediction_mismatch failure
Task 71: Ruling: preflight-179 — PARK; unprefixed top-level yes/no in target_test.go stands — a later clashing declaration would fail to compile
Ruling: wave 1 width 2 not 3 — context-size --wave 3 said handoff (147k of 70k), --wave 2 said ok — Task 3 starts when a slot frees; none otherwise
Task 1: Ruling: cannot-verify (dispatcher writes Complete; guard treats false as unknown) — PARK: the rules belong to Tasks 15, 23, 28 and part G; check them in those reviews — if a later task skips one, a zero-value context reads as a known empty topology
Task 3: Ruling: cannot-verify (40 fixture checksums; no dp-w1- containers left) — settled by the controller running sha256sum -c on the task tree (40 OK, 0 other) and docker ps -a --filter name=dp-w1- (header only), not by a seat — mechanical commands, no judgment — none
Task 3: Ruling: wave merge printed 40 notes, one per generated fixture under testdata/ufw — logged as the single minor below instead of 40 lines; the review already judged scope clean — none
Task 2: Ruling: cannot-verify (surgical test edit vs the brief's whole-file replacement) — settled by the controller comparing test function names: the brief's parse_test.go and the task's file declare the same seven Test functions — mechanical comparison, no judgment — a differing test body inside one function would go unseen
Ruling: wave 2 width 2 not 3 — context-size --wave 3 said handoff, --wave 2 said ok (89%) — Task 6 starts when a slot frees; none otherwise
Task 4: Ruling: cannot-verify (no other package calls Table.Sockets yet; consumers must treat ErrNoSocketTables as a hard failure) — PARK: consumer behaviour belongs to the loaded and guard tasks, check it in their reviews — a consumer that swallowed the error would read a missing proc mount as no sessions
Task 4: Ruling: cannot-verify (report's suite, race, lint and tidy results not re-run by the reviewer) — PARK: the wave's integration check on the merged head is the gate — a late failure there becomes a finding against Task 4 or 5
Task 5: Ruling: cannot-verify (report's worktree path differs from the review checkout) — PARK: task ref rev-parse printed 29fca5bdd506247de6705e86ce4604326d6b535f, the reviewed commit, before merge — none
Task 6: Ruling: cannot-verify (server reads confirmTimeoutSeconds in whole seconds; bounds 10 s and 900 s) — settled by the controller reading the code: lifecycle/apply.go has MinConfirmTimeout 10s and MaxConfirmTimeout 15min, agent/wire/wire.go and controller/api/actions.go read whole seconds — a code lookup, no judgment — none
Ruling: wave 3 serialized — context-size --wave 2 said handoff (221k of 210k) while the plain context-size said ok (63%); Tasks 7 and 8 run one after the other in the checkout — none: serializing is always correct
Task 7: Ruling: cannot-verify (callers pass revertible: true) — PARK: FirewallChange in Task 14 sets revertible: true (plan lines 157, 7749); check it in Task 14's review — if Task 14 omitted it, firewall applies would send no confirmTimeoutSeconds and the operator could not set the timeout from the web
Task 8: Ruling: cannot-verify (nft binary lands on Ubuntu 22.04 and Debian 13 targets) — PARK: only the harness run (gate 7 on the pushed branch) settles it; the controller dispatches harness.yml at the final gate — a failure shows as every target host unsupported, fixed by a one-line edit to the install line
Task 8: Ruling: cannot-verify (nftables.service stays disabled after install) — PARK: inference only, confirm with systemctl is-enabled nftables during the gate 7 harness dispatch — if enabled, add systemctl disable nftables to provision.go with a TestTargetScript assertion; a harness target that reboots would lose its ruleset
Task 9: Ruling: cannot-verify (lifecycle engine runs a self-repairing revertible action correctly) — PARK: Task 16 owns it (repairOutcomeOf, TestRevertibleRepairReportsItsOutcome); check it in Task 16's review — if Task 16 is skipped or reworked, a self-repairing revertible change would record kept whatever the repair reports
Task 10: Ruling: cannot-verify (Task 103 drops a manual rule's comment starting with -) — PARK: Task 103's withComment guards it and TestMxCommentArgs pins -x and --force; the gap between Tasks 25 and 103 is acceptable (only reinsert writes a manual rule, UFW 0.36.2 takes a dash-leading comment as ordinary); confirm the -x and --force vector cases pass in Task 103's review — if Task 103 were dropped, a journalled manual rule with a dash-leading comment could reach a ufw vector during a revert
Ruling: wave 6 serialized without a base-proof run — context-size at 81% cannot fit two parallel tasks, and each serialized task's implementer runs the full suite; the end-of-wave integration check still runs — a base fault would surface one task later instead of at the wave opening
Task 11: Ruling: cannot-verify (new unprefixed names in package netfilter do not collide with other tasks) — settled by the controller searching the plan: builtinChains, parseAddr, protoNames, canonicalProto, implicitModules, restoreParser are declared only by Task 11 in package netfilter; the other matches are in packages firewall and predict — mechanical search, no judgment — a later netfilter task redeclaring one would fail to compile
Task 11: Ruling: cannot-verify (save-syntax Parse behaviour unchanged) — settled mechanically: git diff shows Task 11 left parse_test.go untouched and the suite-run full suite passes with it (55 pass, 0 fail) — none
Task 12: Ruling: cannot-verify (10 to 900 bounds and the 0 never sent rule live in Task 6's code) — settled by the reviewer reading mutate.go (confirmTimeout returns the asserted usagef string; apiCalls omits the field at 0), which is merged Task 6 code — mechanical read, no judgment — none
Task 13: Ruling: plan-conflict pc-13 — CONFIRMED-GAP, different from the reviewer's: UFW 0.36 loads a bare in_ or out_ as a plain rule (the parser already matches) and uses the same unanchored match, but a route field with more than one ! (in_a!b!out_c) is read differently; refuse it in parseIfaces and add one TestParseTupleErrors row — a hand-edited line UFW would load is reported unsupported instead of read
Task 13: Ruling: cannot-verify cv-13a (underscore in an interface name) — PARK: UFW 0.36 uses partition('_')[2], so in_br_lan gives br_lan — none
Task 13: Ruling: cannot-verify cv-13b (comment failing commentPattern counts as representable) — PARK: Task 103's withComment owns it, confirm in Task 103's review that it drops any comment failing commentRe, not only a dash-leading one — if omitted, a hand-edited comment could reach a ufw vector during a revert
Task 7: Ruling: cannot-verify closure — Task 14's reviewer confirmed FirewallChange's target contains revertible: true (firewall-change.tsx:30) and a test shows the Revert after (seconds) field in the repair dialog; the parked Task 7 check is satisfied — none
Task 14: Ruling: cannot-verify (ChangeTarget.revertible exists as Task 7 promised; the Planned changes label belongs to ChangeDialog) — settled by the controller reading the code: change-dialog.tsx has revertible?: boolean (line 32) driving the timeout field and the confirmTimeoutSeconds option, and aria-label "Planned changes" is at change-dialog.tsx:316 — mechanical lookup, no judgment — none
Task 15: Ruling: resumed after a session cut — the first dispatch left commit d1219a9 and no report, so the same agent was dispatched fresh to verify the commit and write the report — none (the commit was already complete)
Task 15: Ruling: cannot-verify cv-15 (Topology.Complete and Dropped are filled by the server topology) — PARK: filling them is Task 23's work (read() sets Complete once web, runtime, agent and peers are registered; boundAddrs and topologyOf fill Dropped for the web and agent listeners) and Task 28's (runtime and session-registry drops plus Warn logs); Task 15's dispatcher rule stands — until Task 23 lands every guarded plan fails as unknown, which fails closed on this branch only
Task 16: Ruling: cannot-verify (Task 9's registry rule is on the base) — settled by the controller: git merge-base --is-ancestor a65243b 1b7082e succeeds and Task 9 has its complete line — none
Task 16: Ruling: Task 9 item cv-9 closed — Task 16's tests cover a revertible target repaired by its own action (restored, kept, no outcome with a Warn) and the compensable regression, as the cv-9 PARK required — none
Task 17: Ruling: cannot-verify cv-17 (the manifest declares the controller unit and the rp_filter owned path) — PARK: Task 70 declares both (external unit {darkpanel-controller.service, [state]}, owned sysctl net/ipv4/conf/<iface>/rp_filter with ^[012]$) and its tests check them; nothing runs the live guard in production before Task 81 — if Task 70 dropped either, every mutating firewall action would fail closed at plan time, which Task 70's manifest test catches
Ruling: wave 11 runs two tasks at once, not three — scripts/context-size --wave 3 says handoff (158k of 70k), --wave 2 says ok (158k of 210k); Task 20 starts when a slot frees — a slower wave if the estimate is too cautious
Task 18: Ruling: cannot-verify (model.Tuple and core.RestoreEntry exist at the base) — settled by the controller: rg finds type RestoreEntry in internal/core/contract.go and type Tuple in internal/modules/firewall/model/tuple.go, and the report's go build ./... and suite pass at 8d7bd28 compile the fwstate imports — none
Task 19: Ruling: plan-conflict pc-19 — CONFIRMED-GAP: the defect is real (deleting the host's last rule refetches rules as [], the early return at rules-table.tsx:185 unmounts FirewallChange and its Confirm and Revert controls, and the stale deleting state reopens the dialog later); fix in Task 19's two files, no plan amendment: render the empty state inside the fragment next to ManualConsent and FirewallChange, and add a test that pushes an empty rules array through the query client while the delete dialog is open — without the fix an operator can never confirm deleting the last rule and the server reverts it every time
Task 19: Ruling: cannot-verify cv-19 (RulesTable consumers pass hostId and rules) — PARK: Tasks 20, 21, 22 and 27 never mention RulesTable; the only call site is Task 34's firewall-page.tsx (<RulesTable hostId={hostId} rules={s.rules} />), which matches the signature — Task 34's review must confirm s.rules is Rule[] (pnpm -C web typecheck catches a mismatch)
Task 20: Ruling: cannot-verify (Task 14's Rule, RuleAction, RuleDirection, RuleFamily, RuleLog, RuleProto and FirewallChange exports exist; the report's test counts are unquoted) — settled by the controller: rg finds every export in web/src/modules/firewall/types.ts and firewall-change.tsx on the task head, and the wave's integration check re-runs the full web suite — none
Ruling: wave 12 runs one task after another — scripts/context-size --wave 2 says handoff (218k of 210k), plain context-size says ok (219k of 350k); Task 21 then Task 22 run in the checkout through the ordinary loop — a slower wave, and the integration check still runs after Task 22
Task 21: Ruling: cannot-verify (FirewallChange and the status types come from Task 14) — settled by the reviewer's own reading of firewall-change.tsx:9,23 (input null keeps the dialog closed) and types.ts:33-40 (Policy, logLevel: string), both on the branch — none
Task 22: Ruling: plan-conflict pc-22 — CONFIRMED-GAP: the defect is real (Cancel calls onCancel without clearing manual, RestoreConsent stays mounted, so the next Restore opens with the manual-rules consent ticked); smallest fix in Task 22's two files, no plan amendment: Cancel's onClick becomes () => { setManual(false); onCancel() }, plus a Cancel-then-reopen test (not keying RestoreConsent on the snapshot id) — otherwise a restore can remove or re-add manual UFW rules without the user agreeing, which no current test catches
Task 22: Ruling: cannot-verify (Task 14's snapshotsQueryKey, FIREWALL_MODULE, SnapshotInfo, SnapshotList, fwSnapshot and FirewallChange exist) — settled by the controller: rg finds each export in web/src/modules/firewall/types.ts and fixtures.ts, and the report's web lint, typecheck and suite pass at 1b39e2f — none
Task 23: Ruling: cannot-verify (the dispatcher sets GuardContext.Complete = Topology.Complete && no drops and logs them) — settled by the controller: internal/controller/dispatch/guard.go:45 on the branch holds that rule, from Task 15's complete line — none
Task 23: Ruling: reviewer minor 1 (the topology reads complete with an empty runtime list between setRuntime in startServices and runtimeListeners.Start) — Task 102 owns it: plan line 47015 rewrites it so the runtime source counts only after Runtime.Start succeeds and an unbound open listener leaves the topology incomplete; Task 102's review must check both — until then the guard misses runtime listener ports during startup only
Task 24: Ruling: plan-conflict pc-24 — CONFIRMED-GAP: the defect is real and no later task owns it (markValue's strconv.ParseUint base 0 accepts 0o1 and 1_0, which iptables refuses, so Key maps an unloadable rule onto a loadable one; Task 92's parseCNumber is an ICMP helper capped at 2^20 and the wrong home); fix inside Task 24's key.go and key_test.go, no plan amendment: an unexported markNumber accepting only ^(0[xX][0-9a-fA-F]+|0[0-7]*|[1-9][0-9]*)$ (so 0b is refused too, glibc before 2.38 stops at the b), called for the value and the mask, plus trailing TestKeyDistinguishes rows for 0o1, 0O1, 1_0, 0x1_0, 0b1 and 1/0o7 kept as written and 017 keyed 0xf — if wrong, a build that accepts 0b reads a hand-edited 0b mark as out of sync, a rare false alarm that fails safe
Task 24: Ruling: cannot-verify (goto -g and fragment -f rules are opaque in parse.go) — settled by the controller: parse.go:350 maps g and f and parse.go:422 appends goto and fragment match to Unsupported, so such rules are kept opaque — none
Task 25: parked — a manual rule's comment starting with - reaches ufw as an option through AddArgs and InsertArgs — Ruling: PARK; the defect is real but Task 103 owns the fix (withComment leaves the comment off when it is empty, starts with - or fails commentRe, drops a failing Darkpanel label, adds TestMxCommentArgs rows -x, --force, it's and a 65-character comment and folds them into TestArgsNoOptionLikeValue); callers before Task 103 (Task 63's reinsert, Task 96's test fake) sit on this branch, which merges to main only when the plan finishes, so the gap never ships — if Task 103 were dropped, a revert re-adding a hand-edited manual rule with a leading-dash comment hands ufw an option-like argument
Task 25: Ruling: cannot-verify cv-25 (the tuple fixtures are real ufw 0.36.2 output) — PARK: the plan records at line 1892 that every argsTestRules vector was run on 2026-10-24 in a privileged ubuntu:24.04 container with ufw 0.36.2-6, TestArgsRoundTrip pins that Merge gives back the input rules, and Tasks 3, 13 and 33 plus the Task 84 harness check the grammar against real UFW (the Task 84 part is inference) — a wrong fixture would pin the wrong grammar until the harness run fails
Task 26: Ruling: cannot-verify cv-26a (the Apply? prompt stays off standard output under --json) — PARK: askApply (mutate.go:311) writes through terminal.line to t.out, which NewRoot (root.go:81) sets to Stderr; planAndApply sends the plan and hints to stderr under --json and firewallInfo picks stderr under --json, so stdout holds only the change JSON — no cost found, only the revert path under --json lacks a test
Task 26: Ruling: cannot-verify cv-26b (the agent socket accepts Revert with an empty core.Principal) — PARK: the existing local commands pass core.Principal{} to Read, Resource, Plan and Apply (local.go), api.Client.Revert builds the same request as Apply, and the socket handler replaces header parsing with socketPrincipal so the caller is always local root and the empty principal is never read — if socketAllow refused revert where it allows apply, a break-glass revert on an un-enrolled host would be refused (fails closed; the Task 84 harness run would show it)
Task 27: Ruling: plan-conflict pc-27 — AMEND: the implementer's surgical edits stand and HEAD needs no change; Task 27's whole-file replacement of snapshots-panel.tsx still carried the pre-fix Cancel button, which would undo Task 22's consent fix (e668b0a) on a rerun, so the Cancel button in Task 27's text now clears manual before onCancel(); no later task replaces snapshots-panel.tsx or its test, and the two test files hold 12 tests where Step 4 says 11 (12 is correct) — if the amendment were left out only Task 22's test would catch a rerun's regression
Task 27: Ruling: amendment A1 — Task 27's snapshots-panel.tsx replacement text gets the Task 22 Cancel fix (setManual(false) before onCancel()) — the whole-file text predated Task 22's fix round — none to the built code, HEAD already matches
Task 27: Ruling: cannot-verify (text-success is a real class) — settled by the controller: rg finds text-success in the existing web/src/components/change-dialog.tsx as well as snapshot-compare.tsx — none
Task 28: Ruling: cannot-verify (the dispatcher clears GuardContext.Complete when Dropped is not empty) — settled by the controller: internal/controller/dispatch/guard.go:45 holds g.Complete = t.Complete && len(t.Dropped) == 0 (Task 15's complete line, checked at Task 23) — none
Task 28: Ruling: the reviewer judged the implementer's three test-strength gaps (Warn level and addr checked on separate substrings, no combined drop-order test, logger swapped after the registry is built) Minor, not Important — the mutation check shows both Warn tests fail without the Warn calls, and the dispatcher uses Dropped only for len() and per-value logging — a wrong order would change nothing that breaks
Task 29: Ruling: plan-conflict pc-29a — CONFIRMED-GAP: parseShell cuts a line at the first space and ignores export, && and trailing content, so IPV6=yes MANAGE_BUILTINS=yes or export MANAGE_BUILTINS=yes enable the built-in-chain flush with no unsupported reason (the spec 2.1 case that must be unsupported); fix in conf.go and conf_test.go: a strict line check (blank, comment, or column-0 ^([A-Z_][A-Z0-9_]*)=(unquoted|"double-quoted")[ \t]*$ with no trailing comment) applied to ufw.conf and /etc/default/ufw, one reason per other line, new reader.shell method, ten new TestReadUnsupported rows plus TestReadQuotedList; no later task re-parses these files for reasons (Task 88's replace blocks stay clear if the doc comments and line counts above TestReadUnsupported are kept) — if the rule is too strict a harmless hand-edited file reads as unsupported (fails closed), if left out a host that flushes its built-in chains passes the lockout guard
Task 29: Ruling: plan-conflict pc-29b — AMEND (a Header amendment, to be confirmed by a second judge-opus before plan-amend): the parser/Python-half disagreement is fixed by pc-29a's strict check and its fixture change (single-quoted values now unsupported, decided now, nothing waits on Task 33); the Part F2 contract's reason list at plan line 599 gains the new first reason — otherwise the contract and the code disagree about the set and order of reasons
Task 29: Ruling: plan-conflict pc-29c — CONFIRMED-GAP: the before.init/after.init loop swallowed every stat error; fix: errors.Is(err, fs.ErrNotExist) skips, any other stat error returns conf: stat ..., a directory with an execute bit counts as executable, plus TestReadErrors row (self-symlink, ELOOP) and TestReadUnsupported row (after.init directory) — if too strict an unreadable init script stops every firewall read with an error (fails closed, unlikely under root)
Task 29: Ruling: cannot-verify (the brief promises Python/shell disagreement reasons that its contract and code did not deliver) — folded into pc-29b's amendment: the reason list now carries the strict-line reason, so the brief's claim becomes true
Ruling: amendment A2 (Header) — the Part F2 contract's Unsupported reasons list (plan line 599) gains a first reason for every line of ufw.conf and /etc/default/ufw that is not blank, a comment or a column-0 upper-case KEY=value (unquoted or double-quoted, regular expression stated, split on \n only) — the contract must state the exact accepted forms because the fix's tests reject a trailing \r and value characters outside the set — if wrong, a looser reading lets a re-implementation accept lines the shell and Python read differently (a lockout-safety gap)
Ruling: header amendment A2 confirmed by a second judge-opus — AMEND, same substance as the first seat but the New text tightened to state the exact rule (the first seat's "only white space after it" would allow a trailing \r and put no limit on value characters); the first attempt's Old fragment was rejected because Old must be whole lines, so the entry carries all of plan line 599 — if wrong, the plan's Global Constraints or Contracts carry a bad rule into every later task
Task 29: Ruling: adjacent finding from the confirming judge — the Task 29 body in the plan (about plan lines 13790-13791, 14104 and 14267-14270) still holds the pre-fix parser and the single-quote fixture, so a rerun of Task 29 from the plan would bring the old parser back (the hazard A1 fixed for Task 27); left unamended for now — a rerun is the only way it bites, and the fix round's tests would catch it
Task 30: Ruling: plan-conflict pc-30a — AMEND: protoMatch treats protocol 0 ("0" or "ip") as every protocol and protoNumbers gains "ip": 0 and "icmp6": 58, in eval.go and eval_test.go only (netfilter stays untouched); the unresolvable-name Unknown case is parked because header returns bool and guard packets are tcp or udp — if wrong, a -p 0 -j DROP rule in a predicted ruleset is skipped and a later rule or the policy can accept a packet the kernel drops
Task 30: Ruling: amendment A3 — Task 30's eval.go text gets the protoMatch protocol-0 rule and the ip and icmp6 map entries — the plan's Step 3 code skipped -p 0 rules — none to the built code once the fix lands; a rerun from the plan keeps the fix but needs the two new tests (TestProtoMatch, TestEvaluateProtoZero) added by hand
Task 30: Ruling: cannot-verify cv-30a (Evaluate matches guard.Input.Eval) — PARK: Evaluate is func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result, the type at plan lines 271, 902 and 15281; the compiler checks it when Task 31 wires Eval: eval.Evaluate — none
Task 30: Ruling: cannot-verify cv-30b (eval.Unmodelled agrees with decides and target) — PARK: Unmodelled belongs to Task 99, whose TestUnmodelledAgreesWithEvaluate checks it — none
Task 31: Ruling: plan-conflict pc-31a — AMEND (on Task 105, not Task 31): tailscaled's flows must cover both address families because spec 2.3 flow 6 is its egress to any destination; Task 31's IPv4-only code stands, because Tasks 53 and 105 edit exactly those IPv4 lines and Task 105 replaces the whole of flows_test.go, so the fix goes after Task 105's own edits — if wrong, a dual-stack change that cuts only tailscaled's IPv6 egress gets an overridable guard failure (a false positive), cheaper than the false negative on IPv6-only hosts
Task 31: Ruling: amendment A4 — Task 105's Step 3 gains the IPv6 stand-in pair for tailscaled (flows.go loop over both families, flows_test.go gets the two IPv6 cases) — the guard would otherwise leave tailscaled unguarded on IPv6-only hosts — a rerun of Task 105 keeps the fix
Task 31: Ruling: cannot-verify cv-31a (spec 2.3 wording on tailscaled's traffic; how Tasks 36, 43 and 53 use standIn and unknown flows) — PARK: settled by the seat — Task 36's probes turns ErrNoRoute into nothing to protect for either family and returns unknown flows before routing; Tasks 43 and 53 add no tailscale-specific handling — a later task asserting an exact tailscale flow list would fail its own tests at once
Task 32: Ruling: cannot-verify cv-32a (container proof that *-restore -n resets a built-in policy and leaves undeclared chains alone) — PARK: the plan's Assumptions bullet "Before-image scope and loading" (plan lines 2043-2045) records the 2026-10-24 privileged ubuntu:24.04 run with iptables 1.8.10 nf_tables where planImage's output loaded with exit 0, the INPUT policy went back to DROP and fw-foreign stayed; no committed test re-proves it, which the plan accepts at line 2192 — if wrong, a repair revert could leave a built-in policy or foreign chain in the wrong state with no test catching it
Task 32: Ruling: cannot-verify cv-32b (chainRef.String() format depends on fwstate.Family) — PARK: Family is type Family string with V4 = "v4" and V6 = "v6" in fwstate.go and has no String method, and image_test.go compares chainRef values by reflect.DeepEqual — if wrong, only the wording of log or report text changes
Ruling: Wave 23 serialized — an Agent call with isolation worktree is refused for a task needing this checkout's git state, which wave rules name as a serialize trigger; Task 34's isolated run, already dispatched, finishes and merges as the rules say if it can, and the rest run through the ordinary task loop in index order — cost if wrong: none (serial execution is the sequential run)
Task 34: Ruling: the isolated implementer worked in .claude/worktrees/agent-ab045e96fb36f4603 on task ref sdd/2026-10-24-v1-w1-firewall-core/task-34 (6f9afb5, one commit on base a8b198c, confirmed with git rev-parse), and its report was copied into the workspace by hand because sdd-workspace refused to run there — none (the review runs from the task ref and wave merge takes the ref)
Task 33: Ruling: the controller checked the reviewer's open items itself — at HEAD 79a1806 the golden-tree command prints f43bf0607cf5019a9e41b73b5db0515de863c3ced469861b164d29faf7e338be, the file count is 736, git diff --stat 5d5a310 -- internal/modules/firewall/testdata/golden is empty, and docker ps -a --filter name=dp-w1- printed only its header — none
Task 33: Ruling: plan-conflict pc-33a — AMEND (Header and Task 33): the plan's expected golden-tree hash 206dc9fb… is wrong; the drafted tree (5d5a310), a fresh four-image capture and the committed tree all hash to f43bf0607cf5019a9e41b73b5db0515de863c3ced469861b164d29faf7e338be (736 files), and only plan lines 1896 and 16520 mentioned either hash — if wrong, a different authoritative tree would show up in the later golden-value tests; left unchanged, a rerun of Task 33 would fail its own hash check even through the fallback
Ruling: header amendment A5 confirmed by a second judge-opus — AMEND, same substance: replace the hash on the header's Assumptions line "Golden captures were produced on 2026-10-24…" and nothing else — if wrong, the plan's Global Constraints or Contracts carry a bad rule into every later task (here only a documented hash)
Task 33: Ruling: amendment A5 (Header) and A6 (Task 33) — the golden-tree hash in the header's Assumptions bullet and in Task 33 Step 3's Expected line becomes f43bf060… — the recorded hash matched no tree anyone produced — none to the built code, since the committed tree equals 5d5a310
Task 35: Ruling: cannot-verify cv-35a (SrcPort 0 as an unknown source port) — PARK: Task 42 (plan lines 21008-21176) adds the w.pkt.SrcPort == 0 check to portOption's sport branch with tests, and Task 53 gives the guard's new-connection flows source port 0 — until Task 42 lands a guard flow could decide a --sport rule on a port nobody chose, a short window already specified
Task 35: Ruling: cannot-verify cv-35b (address types inferred from the interface) — PARK: no guard flow in flows.go builds a packet from one of this host's addresses on a non-lo interface (the host-sourced flows drop host sources through fromHere, the proxy and NPM flows use non-host sources, tunnel egress is forward-path, agent and tailscaled egress is output-path) and Task 53 changes only the source ports — if wrong, a misreported ProxyAddr equal to a host address would be evaluated with --src-type LOCAL false
Task 35: Ruling: cannot-verify cv-35c (w.dnat never set by Task 35) — PARK: Task 41's natTarget sets w.dnat (plan line 20883) and its tests match a DNATed NEW packet against --ctstate DNAT in two cases (plan lines 20549-20555 and 20570-20574); negation flips every decided result in one place (match.go:76-78), so a negated DNAT test would cover no new path — if wrong, a later task giving negated conntrack options their own path could leave ! --ctstate DNAT untested
Task 36: parked — plan-conflict pc-36a (a forwarded stand-in flow is silently skipped when live forwarding is off, because the in-interface lookup answers ErrNoRoute) — Ruling: PARK: viaFlows builds the tunnel flows only when the guarded request came through that tunnel, so a working tunnel means live forwarding is on and the in-interface lookup succeeds; the skip fires only when the tunnel is already down and no session exists to lose; if the owner wants more strictness the cheap fix is to apply the stand-in tolerance only after a destination-only RouteQuery{Dst} also returns ErrNoRoute — cost if wrong: one unevaluated egress flow that is not working before the change
Task 36: Ruling: plan-conflict pc-36b (per-interface forwarding switch never checked) — PARK: spec 2.1 makes net.ipv4.conf.*.forwarding in UFW's sysctl file unsupported, writing ip_forward resets the per-interface values, and the live lookup respects live forwarding — if wrong, a host with an unusual per-interface setting is judged from the live lookup (accuracy, not lockout safety)
Task 36: Ruling: plan-conflict pc-36c (loose rp_filter counts interfaces the packet cannot arrive on) — PARK: over-conservative only, since a flow must pass on every eligible interface and an extra probe can add a drop or unknown verdict but never hide one — if wrong, an occasional spurious guard failure the operator can override
Task 36: Ruling: cannot-verify cv-36a (localAddrs includes down interfaces) — PARK: a down interface loses its local routes so a flow to its address cannot be working live, and leaving the addresses out would mislabel an up but not running interface such as docker0 — if wrong, a wrong verdict on a flow that does not work before the change
Task 36: Ruling: cannot-verify cv-36b (Task 43 calls newRouter with the predicted Loaded) — PARK: Task 43's Checker.Check calls newRouter(h, f, st) at plan line 21618 with the checked state and Task 113 keeps it (plan line 51268) — if wrong, Task 43's review would catch rp_filter and forwarding read from the live state
Task 37: parked — plan-conflict pc-37a (retention protects only pending, applying and needs_repair, not interrupted, undoing, recovering, awaiting_confirmation or quarantined) — Ruling: PARK: spec 2.5 line 2945 protects snapshots "referenced by an unconfirmed or needs_repair change" and spec 1.3 defines unconfirmed revertible as applying or pending, every firewall action is revertible, recovery reverts such a change rather than marking it interrupted, and the other states belong to compensable, task, composite or controller-quarantine changes; snapshots are saved only under the firewall lock — cost if wrong: if a later wave lets a firewall change reach quarantined or interrupted while its snapshot is still needed, it could be pruned after 50 newer saves (10 for system changes); the fix is a one-line state NOT IN list plus one retention test
Task 37: Ruling: plan-conflict pc-37b (stored beforeImage not validated before planImage, Task 32's deferred minor) — PARK: the only writer of the state column is capture (Task 45) from parsed *-save output, so a newline can only come from altering the root-owned agent database, which already allows running iptables-restore as root; Tasks 62 and 119 consume the image and none validates it — cost if wrong: if the agent database can be imported from a less-trusted source (an agent backup restore, not checked), injected restore lines could flush or replace chains during a revert or repair; the fix is a check in planFamily refusing \n or \r in Name, Policy and Text plus one test
Task 37: Ruling: cannot-verify cv-37a (attachNewest assumes no other snapshot is captured between capture and attach) — PARK: the lifecycle engine holds applyMu across begin (apply.go lines 69-71, 181-183, 209, 231), so capture, the free-lock check and the intent run serialised, and Task 64's revert attaches by the change's own sealed snapshot id and attachSnapshot refuses a row attached elsewhere — cost if wrong: two engine processes on one database (a --local break-glass command beside the agent, not checked) could attach the other's orphan snapshot, which fails visibly as needs_repair
Ruling: Wave 27 serialized — the budget cannot fit two parallel tasks, which wave rules name as a serialize trigger; Tasks 38 and 39 run through the ordinary task loop in index order — cost if wrong: none (serial execution is the sequential run)
Task 38: Ruling: cannot-verify (the Global Constraints cleanup step) — settled by the controller: /root/.cache/w1-tmp/38 does not exist and the checkout is clean — none
Task 39: Ruling: cannot-verify (163 cases pass against the committed goldens) — settled by the controller: go test -count=1 -run TestUFWGolden -v ./internal/modules/firewall/eval/ printed 163 PASS lines and no FAIL lines, and go test ./internal/modules/firewall/eval/ → ok; the scratch TMPDIR was removed — none
Task 40: Ruling: plan-conflict pc-40a — CONFIRMED-GAP plus AMEND: on a host where iptables is legacy and ip6tables is nft, read runs ip6tables-nft-save without consulting NFTablesLoaded, which makes the kernel load nf_tables and breaks the Part C host-safety constraint; Tasks 56 and 60 replace read.go wholesale and would not gate it either; fix in the built read.go: inside the mixed-variants branch, after the Unsupported reason, skip the IPv6 save (V6 stays nil) when the variant is nft and nf_tables is not loaded, using r.NFTablesLoaded or an inline os.Stat("/sys/module/nf_tables") closure (no named function, Task 56 declares nfTablesLoaded in foreign.go), plus TestReadLegacyWithNFTIPv6 in read_test.go — if wrong, a mixed-variant host makes Darkpanel load nf_tables; if the gate is wrong, one missing V6 ruleset on an already-unsupported host
Task 40: Ruling: implementer's second point (both families nft with nf_tables not loaded) — intended, not a gap: spec 2.1 reads the loaded state with the save tools of the variant UFW uses and the constraint bans only *-nft-save under the legacy variant and nft itself; UFW loads nf_tables when it starts under nft — none
Task 40: Ruling: cannot-verify cv-40a (Task 56 gates the IPv6 nft save) — CONFIRMED-GAP: neither Task 56 (plan lines 29007-29060) nor Task 60 (31563-31610) gates it; carried by pc-40a's fix and amendments — same cost as pc-40a
Task 40: Ruling: amendments A7 (Task 40), A8 (Task 56) and A9 (Task 60) — the mixed-variants branch of read gains the nf_tables gate in the plan text of all three tasks that write read.go, so a rerun or Tasks 56 and 60 do not remove the fix — the test TestReadLegacyWithNFTIPv6 lives in read_test.go, which Tasks 56 and 60 do not replace, and is not in the amendment
Task 41: Ruling: cannot-verify (evalPacket defaults) — settled by the controller: evalPacket() in eval_test.go sets Family V4, Path input, InIface eth0, Dst 192.0.2.1, Proto tcp and CtState NEW, which the expectations need — none
Task 42: Ruling: cannot-verify (the guard's later use of SrcPort 0 for connections that do not exist yet, Task 53) — settled by the ruling seat's earlier verdict cv-35a (Task 53 gives the guard's new-connection flows source port 0, Assumptions at plan line 2007; Task 42 is the matching evaluator change) — Task 53's review should confirm it; if wrong, a guard flow could decide a --sport rule on a port nobody chose
Task 43: Ruling: cannot-verify (r.route matches the fwstate.Router type, r.st as Task 36 declared it) — settled by the controller: go build ./..., go vet and the 11 guard tests pass at 8133204 (reported by the implementer and by suite-run 61 pass 0 fail), and a mismatch would not compile — none
Task 43: Ruling: cannot-verify (the parked Task 36 stand-in skip when live forwarding is off) — already ruled: Task 36 pc-36a PARK in this ledger (the tunnel flows exist only when the guarded request came through the tunnel, so live forwarding is on then); Check uses router.probes unchanged — see pc-36a for the cost if wrong
Task 44: Ruling: plan-conflict pc-44a — CONFIRMED-GAP plus AMEND: the tailnet shortcut fails open because spec 2.3's premise (tailscaled's ts-input drops such sources on every other interface) is false for IPv6 (Task 55 fixture v1.102-v6.save: addBase6 has no CGNAT rules) and for --netfilter-mode=off or nodivert, and 100.64.0.0/10 is also RFC 6598 carrier-grade NAT space; fromTailnet now returns true only when the unmapped source is in tailnetRanges, tailscale0 is up and RouteGet{Dst: src} succeeds with OutIface tailscale0 (a failed lookup is not tailnet) — if wrong, a real tailnet peer whose route back is not through tailscale0 gets the normal rule (an overridable spurious refusal); the owner should correct spec 2.3 lines 2782-2785
Task 44: Ruling: amendments A10-A16 (Task 44: the task description, the tsPeer4/cgnat4/cgnat6/noRoute6 packets, the table rows, the route keys, the two comments, the fromTailnet body) and A17, A18, A19 (Header: the Part contract for fromTailnet, the contract bullet for tailnet eligibility, and the controller ruling's premise) — the plan text kept the old rule in four places, so a rerun or a later reviewer would restore or flag the fix — none to the built code
Ruling: header amendments A17-A19 confirmed by a second judge-opus — CONFIRMED-GAP, same substance as the first seat's entry, plus two further Header lines (the contract bullet and the controller ruling, found at plan lines 111 and 1997) that stated the old rule — if wrong, the plan's Global Constraints or Contracts carry a bad rule into every later task
Task 44: Ruling: cannot-verify cv-44a (other readers of facts.ifaces after a vanished interface is dropped) — PARK: the readers are localAddrs (flows.go buildFlows and newRouter) and the interface loops in eligible and fromTailnet; a vanished interface's addresses are no longer local to the kernel, spec 2.3 line 2785 says an interface that disappears while the facts are read is not eligible, and every effect checks more flows or skips an address that no longer exists — if wrong, an address of a just-removed interface is treated as remote, which no working session can use
Task 45: parked — cv-45a capture stores readLoaded's Sysctl unfiltered — Ruling: leave the code; capture stores what readLoaded returns and the production reader is Task 60 (loaded/sysctl_read.go, a fixed key set the plan wires as readLoaded in a later task), so supported-keys-only holds there and Task 60's review checks it; if wrong, an extra key in a snapshot would be written back by a revert (outside spec 2.1) and Task 60's own tests would catch it
Task 45: parked — cv-45b attachNewest picks the newest unattached row, safe only without concurrent firewall captures — Ruling: leave the code; lifecycle apply.go holds applyMu from the lock checks through persisting the intent, locksFree runs before Snapshot, and the locks are taken in the change-insert transaction, so two captures cannot interleave and repair and recovery stay safe; if wrong (a future engine change that captures outside applyMu), a change could attach another change's snapshot and revert to the wrong state, and the fix would be attach by change id in the engine
Task 46: parked — cv-46a Task 38 tupleLine and multiPort behaviour, model.Protos includes any — Ruling: leave the code; the seat read model/rule.go (Protos returns tcp, udp, any, ah, esp, gre, ipv6, igmp) and predict/render.go (multiPort, tupleLine) and both are consumed as parseRuleVector assumes; if wrong, TestParseVectorRules would already fail in the suite, and only a later render.go change could reopen it
Task 47: parked — cv-47a trace values such as nat/DOCKER#2 against what Evaluate produces — Ruling: leave the code; TestDocker28 compares verdict and the full trace through evalCheck with no skip path, the suite log holds 61 ok lines at e052627, and hooks_test.go uses the same nat/DOCKER#2 form; if wrong, the eval package would fail in the next full-suite run, and nothing downstream consumes the values
Task 47: parked — cv-47b cases with trace nil or reason empty depend on evalCheck (Task 41) — Ruling: leave the code; evalCheck skips the trace comparison only when trace is nil, every docker28 case has a non-nil trace, and an empty reason checks nothing by design; if wrong, a case asserts verdict and exact trace only, which is not a false pass
Task 48: parked — cv-48a name validation and quoting of bad lines belong to Task 56 — Ruling: leave the code; Task 56's plan text requires nftSafeName and nftFamilies validation (reason table name not readable safely) and builds the reason with %q and truncate from Task 40, so parseNFTTables returning raw bad lines is correct by contract; if wrong, a Task 56 implementer who drops %q or truncate sends oversized or control-character nft output into user-visible Unsupported reasons and the existing test would not catch the missing truncate
Task 49: parked — cv-49a golden rules and sysctl scenarios differ only in the keys the report names — Ruling: leave the code; the brief asks only that the golden test predicts the sysctl scenario's values from the rules scenario's on all four images, and TestPredictSysctlGolden does that with maps.Equal over TransEnable and TransReload on every image (and checks before and after differ), with the suite green at 9c68b93; if wrong, the golden tree changes only through capture.sh and the same test would catch drift on any image
Task 50: parked — cv-50a callers must check Loaded.Unsupported before trusting Compare — Ruling: leave the code; the plan already carries the gate: Task 72 fw.prepare refuses on any configured or loaded Unsupported reason before syncDiffs or any apply or verify (TestPlanRefusals), Task 69 status sets Supported false (TestStatusOutOfSyncAndUnsupported), Task 109 hostUnsupported replaces both inline checks, and loaded.Read (Task 56) turns Table.Incompatible into an Unsupported reason; if wrong, an incompatible table first appearing during the apply (a non-iptables writer racing it) would verify as an empty table, and the fix is for verify to fail on a non-empty after-apply Unsupported list (a small addition to Task 108)
Task 51: parked — cv-51a netfilter.Tables matches the golden save captures — Ruling: leave the code; Tables returns knownTables (filter, nat, raw, mangle, security) and with v4 and v6 that is exactly the ten nft-<family>-<table>.txt files in every golden save directory (320 files), predictLoaded opens each and fails the test on a missing file, so no capture is skipped; if wrong, a sixth table added without captures would fail the golden test with a missing-file error rather than pass
Task 52: Ruling: plan-conflict pc-52a (undecidable CONNMARK restore or save kept the old val) — AMEND: uncertainCopy absorbs the rule only when running it leaves the known value of the mark it writes unchanged, with saved and restored helpers shared with markTarget, plus a regression test for both forms (A20 description, A21 markTarget, A22 uncertainCopy, A23 test) — if wrong, a fail-open survives for copies whose source bits come from an earlier packet (non-NEW packets the guard never evaluates)
Task 52: Ruling: plan-conflict pc-52b (connmark in the raw table modelled as if a connection existed) — AMEND: in the raw table -m connmark is undecidable and a CONNMARK target gives Unknown, with a test (A24 module, A25 target, A26 test) — if wrong, an over-cautious Unknown on raw-table connmark rulesets that UFW, Docker and tailscaled do not generate
Task 52: Ruling: plan-conflict pc-52c (connmark on UNTRACKED or INVALID packets) — AMEND: noConn helper, connmark misses negated or not, CONNMARK does nothing, with a test (A27 markMatch and noConn, A28 markTarget, A29 test) — if wrong, a connmark ACCEPT on such packets is judged a miss, which leans toward drop
Task 52: parked — pc-52d reroute skipped when only ext changed — Ruling: keep reroute as written; after pc-52a both outcomes of an absorbed copy agree on val, a NEW packet's connection mark starts at {0,0} so a restore cannot widen mark.ext, and the reviewer's fix would turn the mandated restored-mark-no-match-reads case into Unknown and change Task 55's 1.102 established-forward golden; if wrong, for an ESTABLISHED or RELATED packet whose restored connection-mark bits feed policy routing, filter rules matching -o may be judged against the wrong out-interface (guard verdicts are not affected)
Task 52: parked — cv-52a Task 55 goldens depending on the changed behaviour — Ruling: leave Task 55 and the later tasks unchanged; the seat traced Task 55's 1.102 goldens, Task 99's TestUnmodelledAgreesWithEvaluate and every fixture's raw table through the amended code and found no expected verdict that moves; only Task 99's Unmodelled doc comment text drifts; if wrong, a guard fixture the seat did not open that puts connmark rules in the raw table or uses a connectionless state would flip to Unknown or Drop (fail closed) and its review would catch it
Task 53: Ruling: the plan's end-of-task go clean -testcache was skipped on my instruction — it clears the Go test cache for every session on this host, not only this task's, and the per-task TMPDIR was still removed — if wrong, leftover test-cache entries cost disk and nothing else
Task 54: parked — cv-54a production ReadSysctl wraps ErrNotExist for a missing per-interface key — Ruling: leave the code; readSysctl reaches openSysctl then openBeneath, which returns the raw ENOENT from openat2 wrapped with %w, so errors.Is(err, fs.ErrNotExist) holds and a vanished interface reads as null on a real host (plan line 1920, captured at line 21897); if wrong, observing a vanished interface's sysctl fails loudly instead of reporting null
Task 55: parked — cv-55a Step 5 cleanup — Ruling: leave as is; /root/.cache/w1-tmp/55 is gone (controller ls), every test run used -count=1 or -race so no stale cached result was relied on, and go clean -testcache was skipped on the Task 53 controller ruling because it clears the Go test cache for every session on this host; if wrong, a stale cache entry could mask a failure in a later non-count=1 run
Task 55: parked — cv-55b assertion wording against the evaluator — Ruling: leave as is; no t.Skip, build tag, Short or Parallel in the eval directory, evalCheck compares reasons with strings.Contains, and the asserted fragments match what eval.go (policy DROP, drops, ref plus reason) and mark.go (the match on mark ... needs bits) produce; 35 subtests plus 2 parents equal the report's 37 PASS lines; if wrong, wording drift fails the next go test and no task builds on these strings
Task 56: Ruling: blocked-plan bp-56a (NEEDS_CONTEXT: Task 56's foreign reads break the pc-40a nf_tables loaded subtest of TestReadLegacyWithNFTIPv6 in read_test.go, outside Task 56's Files) — AMEND A30: add read_test.go to Task 56's Files and edit only that subtest (an empty answer for nft list tables and the command count 2+2*len(netfilter.Tables())+1, 13 commands, checked against read.go and foreign.go); the uncommitted foreign.go, foreign_test.go and read.go stand — if wrong, one more count edit if a later task changes the read sequence again
Task 56: Ruling: cannot-verify (parseNFTTables splits on whitespace only, so the control characters in TestForeignNativeQuoted stay inside fields) — settled by the controller running go test -count=1 -run TestForeignNativeQuoted -v ./internal/modules/firewall/loaded/ → PASS at 5b556de; it asserts the exact quoted strings including the ESC and \x01 cases, so the assumption holds — if wrong, the test would fail in the suite
Task 57: Ruling: cannot-verify (error strings of parseVector and checkRenderable start with predict: when Edit returns them unwrapped) — settled by the controller: render.go has no error constructed without the predict: prefix (rg over errors.New and fmt.Errorf), and vector.go's unprefixed strings are inner errors that parseVector wraps at its public boundary, which Task 46's TestParseVectorRefuses checks for the predict: ufw prefix (Task 46 review); if wrong, an Edit error would lack the prefix, which is cosmetic
Task 58: parked — pc-58a built-in filter rules from before.rules and after.rules dropped on a reload or an enable after a disable — Ruling: leave the code as committed at 95ab8fd; the defect is real but the plan already schedules the fix as Task 104 (Predict built-in rules on reload: jumpedChains in translate.go and restoreText, the startFamily continue branch replaced by c.Rules[min(len(jumpedChains), len(c.Rules)):] plus loadChain, and TestPxBuiltinRules expecting 1, 2, 2 and 3 copies), whose Step 2 expects the test to fail against today's code and whose Step 3 edit replaces the exact block now at predict.go:169-172, so fixing it now would break both; Task 104 must stay in the plan; if wrong (Task 104 dropped or skipped), a host whose before.rules or after.rules adds a built-in filter rule fails verify with firewall.prediction_mismatch on every reload and the panel reverts each time (nothing wrong is applied, but permanently for that configuration)
Task 58: parked — cv-58a whether real UFW re-appends built-in filter rules on every start — Ruling: leave the code; Task 104's description (plan line 47921) already states that ufw_start adds the six jumps only when it creates the primary chains and runs iptables-restore --noflush on before.rules and after.rules on every start, with a test following real UFW 0.36.2 on debian:13, so the requirement belongs to Task 104; if wrong, a wrong model surfaces as firewall.prediction_mismatch and a revert in the harness or golden scenarios (a safe, findable failure)
Task 59: Ruling: amendment A31 — runUfw journaled a ufw add that replaces an existing rule (same specification, other action, log type or comment) as a pure creation with a null Before, so Task 63's inverse (delete of Post) would lose the replaced rule; runUfw now refuses a creation whose predicted config grows a family's tuple list by other than the new id's tagged tuples, before Intend — cost if wrong: adds matching an existing rule fail at apply time instead of being journaled as modifications; nothing is changed
Task 59: Ruling: cv-59a — PARK: Part A2's fw.plan (Task 72) is not built; with A31 an add that would replace a rule is refused at apply whatever the plan does — cost if wrong: the operator sees the refusal at apply instead of plan time; no rule is lost
Task 59: Ruling: cv-59b — PARK: Files.WriteSysctl (internal/platform/files_sysctl.go) checks sysctlDeclared and the owned-path pattern before opening /proc/sys, so writeSysctlEffect needs no duplicate check — cost if wrong: unknown, depends on sysctlDeclared accepting undeclared keys (seat did not read its body)
Task 60: Ruling: amendment A32 — pc-60a: the platform's openProcSys wraps its openat2 error with %w so a host without /proc/sys reads as every key missing; files_sysctl.go is not frozen (spec 1.12 names files.go, exec.go and systemd.go), so Task 93 (next task that rewrites sysctl_read.go) gains a separate platform commit with TestSysctlRootMissing and a %v wrap carrying a nolint:errorlint directive; Task 60's code and its gone test stand, and the optional ip_forward safeguard is declined — cost if wrong: if Task 93 is skipped, a host without /proc/sys reads an empty sysctl state and restores report success while writing nothing
Task 60: Ruling: cv-60a — PARK: PredictSysctl skips configured keys the current map lacks and Compare never compares Sysctl, so an lo key cannot leave the host out of sync; the handoff's note for Task 93 stands — cost if wrong: before Task 93, lo values are neither predicted nor reverted
Task 60: Ruling: cv-60b — PARK: Task 70's manifest and its tests declare exactly the five sysctl key patterns, and Task 70's review checks them — cost if wrong: every production Read would fail loudly, fail-closed
Task 61: Ruling: cv-61a — PARK: filterBuiltins, predictTestChain, predictLoaded and predictConfig exist in package predict with the shapes refusal.go and its test use, and Tasks 38, 51 and 58 are complete — cost if wrong: a mismatch would stop the predict package compiling, so the suite fails before any merge
Task 62: Ruling: amendment A33 — pc-62a: restore must call predict.IPv6Refusal on the restored configuration and the loaded state before it runs a ufw transition (Part P lines 782 and 788, spec 2.3), after the files are written and before imagePlan, only on the non-before-image path; the connectivity exception may not override it; a direct predict import, no deps hook — cost if wrong: if the connectivity exception should override, an emergency restore on an IPV6=no host with foreign IPv6 chains fails and the change stays needs_repair
Task 62: Ruling: pc-62b — CONFIRMED-GAP carried to the final review: planFamily (image.go lines 143-148) declares every non-built-in image chain, so a jump to a foreign chain absent from the restore input and the current table makes *-restore -n reject the whole table and every rerun of the kept record fails; smallest fix is in planFamily: exclude (into Excluded) any non-built-in image chain with a -j or -g to a chain outside the declared plus current set, repeating until stable, with a test where ufw-user-input jumps to a missing fw-gone; applied after Task 119 because Task 119 rewrites loadImage — cost if wrong: a repair's revert fails on every rerun and the change stays needs_repair (fails closed, no lockout)
Task 62: Ruling: cv-62a — PARK: A32 (headed Task 93) makes a missing /proc/sys a hard error, Task 93's sysctlKeys always reads net/ipv4/ip_forward first and its readSysctl returns any error but fs.ErrNotExist, so a capture on such a host fails and an empty-sysctl snapshot cannot be taken — cost if wrong: if Task 93 is skipped, snapshots without sysctl values restore successfully while writing none
Task 63: Ruling: cv-63a — PARK: A31 and A32 are consistent with spec 2.5 and Task 63's use of them (A31 guarantees deleteAdded one new tuple per family; A32 makes a missing /proc/sys an error so writeBackLive fails the inverse and the revert stops at needs_repair); until Task 93 lands the committed code still has the fs.ErrNotExist exposure, so Task 93's platform commit must not be dropped — cost if wrong: an add that ufw would merge into an existing rule is refused at apply time instead of journaled
Task 63: Ruling: amendment A34 — cv-63b: writeBackLive overwrites every snapshot sysctl value, so undoing an enable or disable replaces a key changed by hand in the pending window, against spec 2.5 step 3 and plan line 124; Task 64 gains a separate commit before its Step 1 that makes writeBackLive write back only keys the change moved (snapshot value kept when the live value equals the snapshot or the predicted forward value, else the live value), with one test — cost if wrong: an extra config read and prediction per state inverse; a key changed by both ufw and the operator keeps the operator's value
Task 64: parked — finishRestore decides Override only from len(blocked) > 0, so a crash during an emergency restore triggered by conflicts or a failed write is later reported as a clean revert (revert.go:554-573) — Ruling: pc-64a PARK, real but the plan already owns it: Task 106 adds restoreRecord, markEmergency and restoreRecordOf and Task 114 makes emergencyRestore store the guard reason, overwritten files and outcomes before its first write and finishRestore return that Override; a fix now would clash with Task 114's replacement blocks; the final review must confirm Task 114 landed — cost if wrong: if Task 114 is skipped or misses this path, such a crash still ends as reverted with no override record
Task 64: Ruling: pc-64b — CONFIRMED-GAP carried to the final review: undo must not check Version (spec 2.5 and 1.2 compare State only), but reinsert (inverse.go:154) uses the absolute Pos4/Pos6, so a manual insert during the pending window puts the re-added rule in the wrong order among the snapshot's rules (snapshot [A,B,R,C], operator inserts M at 1, reinsert at 3 gives [M,A,R,B,C]) and the change is reported reverted with no guard; fix: inverse passes snap to reinsert, parse the snapshot's tuple list per family, find R by Raw, walk back to the first predecessor present in the current tuples at 0-based index m and insert at place m+2 (place 1 with no predecessor; fall back to the recorded Pos when R is not in the snapshot), keep the append-past-end rule, test asserting M, A, B, R, C — cost if wrong: if the absolute index was what the spec meant, a re-added rule lands at a different index than recorded after manual edits, its order among the snapshot's rules kept
Task 64: Ruling: amendment A35 — pc-64c: restore writes every snapshot file (IPV6=no included) before predict.IPv6Refusal runs, so a refused restore leaves IPV6=no on disk for the next reload or boot (spec 2.3); a separate Task 64 commit adds conf.DefaultsIPv6 and snapshotStart, decides the refusal from the snapshot's /etc/default/ufw entry before the first write, and extends TestRestoreRefusesIPv6NoOverForeignRules; restore's doc comment stays unchanged (Task 114 anchors on it) — cost if wrong: one extra conf helper; every non-refused restore behaves the same
Task 64: Ruling: cv-64a — PARK: settled by the controller's diff of the brief's code blocks (lines 473-870 and 28-452) against revert.go and revert_test.go, both identical — cost if wrong: none
Task 65: Ruling: cv-65a — PARK: cv-45a covers only a sysctl read that fails inside an otherwise successful Read; capture (snapshot.go:72-75) returns nil and the wrapped error as soon as readLoaded fails, before the nil-to-empty step and before saveSnapshot, so the zero Loaded with a nil Sysctl that Read returns with an error is thrown away; Read must keep Part C's rule that an error never comes with a partial state — cost if wrong: a later caller using st.Sysctl after an error would read a nil map (empty values, no crash; no current caller ignores the error)
Task 65: Ruling: amendment A36 — pc-65a: Minor 1 (readLoaded and lockout wiring pinned only as non-nil) is already handled by Task 97's TestProductionReadLoaded and TestProductionLockout; Minors 2-3 (the nil-context case does not check that Plan reached check as nil, the error case does not check that detail is empty) are folded into Task 97's TestLockoutVia — cost if wrong: two extra assertions in a test, no production change
Task 66: Ruling: amendment A37 — pc-66a: stateDiff and manualChanges compared rules as a set of texts, so adding or removing a copy of a duplicated manual rule (same manualID) needed no manual: true and showed no diff line, reachable through firewall.snapshot.restore (spec 2.5); both now match rules one copy to one copy through a private helper unmatched (a count per text), manualChanges returns an id once for each copy removed or added, and ruleTexts keeps its map[string]bool signature because Task 80's uninstall code uses it as a set; the consumers at plan lines 36877 and 50028 only test len(ids) > 0 and need no change; a new test TestStateDiffDuplicates covers the copy added, removed and moved — cost if wrong: the unused linter flags ruleTexts until Task 80 uses it, or Merge collapses duplicates and the new test fails (either caught at Step 4)
Task 66: Ruling: cv-66a — PARK: diff.go at 2318db8 meets every Task 66 entry in the Part contracts (signatures, line formats, rule order, the reordered line, sorted sysctl keys, manualChanges id order); the private helper unmatched added by A37 changes no contracted name — cost if wrong: a signature mismatch shows as a compile error in Task 71, 72 or 80
Task 67: Ruling: cv-67a — PARK: the exec allowlist is the platform's ProductionRules (internal/platform/exec_rules.go lines 88-89 allow --version for /usr/sbin/iptables and /usr/sbin/ip6tables, spec 1.8), not the firewall manifest; preflight only stats the save and restore paths and runs the two --version commands; Task 70's review must confirm the manifest sets Preflight: m.f.preflight() unchanged — cost if wrong: on a clean host the iptables-tools check would fail on an allowlist error and the module would show unavailable (unlikely, the platform rules already permit both)
Task 67: Ruling: cv-67b — PARK: the runtime check (internal/agent/runtime/runtime.go lines 261-276) runs m.Preflight in order and stops at the first error, spec 1.5 and 2 ask for one reason, so pfRun matches; Task 70's review must keep the id order [ufw ufw-version iptables-tools] — cost if wrong: a reordered wiring would change the reported reason (cosmetic)
Task 68: Ruling: cv-68a — PARK: the plan runs the Unsupported check before every comparison the spec gates (status in Task 69 before syncDiffs; Task 72 plan only after prepare refuses on cfg0.Unsupported, cur.Unsupported and tg.cfgN.Unsupported; Task 109 keeps hostUnsupported before syncDiffs in status and prepare); mutation.Verify (Tasks 73 and 108) calls syncDiffs and verifyDiffs with no Unsupported check by design (spec 2.1 and 2.3, prepare already refused) — cost if wrong: a host a third party makes unsupported between apply and verify passes verify on the chains it compares; the next status or plan still refuses
Task 68: Ruling: pc-68a — CONFIRMED-GAP carried to Task 98 (not a fix round on Task 68, because Task 98 replaces sync.go and sync_test.go in full): foreignKeys must count a built-in-chain rule as UFW's or Darkpanel's only when it is a bare jump with no matches; in Task 98's replacement sync.go the foreignKeys loop body becomes `k := netfilter.Key(r, fam == fwstate.V6); if !firewallChain(r.Target.Name) || k != "-j "+r.Target.Name { out = append(out, k) }` with its comment saying "not bare jumps to a UFW or Darkpanel chain", confined's comment says "bare jumps to such chains", and TestConfined gains the case {"foreign matched jump", append([]string{"-A INPUT -s 203.0.113.9/32 -j ufw-user-input"}, base...), syDecls, false}; related for the final review: image.go (part A1, plan lines ~15984 and ~16067) uses the same firewallChain(r.Target.Name) test for "non-UFW, non-Darkpanel lines", so a matched jump to a UFW chain added by another program after a repair is counted as UFW's and the revert's *-restore -n would flush it; apply the same bare-jump test in both places (Own: and the got loop) with a matching image test — cost if wrong: a legitimate matched jump into a UFW chain gets its repair refused as foreign (one refused action, no lost state)
Task 69: Ruling: cv-69a — PARK: the web FirewallStatus types (Task 14 types.ts, plan lines 7594-7715) and the CLI m3Status (plan lines 40956-40965) match the committed Status, Configured, Defaults, LoadedState, LoadedChain and ChainDifference JSON tags and types, and status.go never serialises Unsupported, OutOfSync, Rules or Loaded.Sysctl as null; Task 14's review (types.ts and the fwStatus fixture), the StatusBanners/StatusSummary task, Task 21 (StateControls reads only configured.defaults and configured.logLevel) and the Task 83/85/86 harness helper (m3_firewall_helpers_test.go) must check the tags are unchanged — cost if wrong: a renamed tag would make the web page or the M3 harness read zero values, caught by those reviews
Task 70: Ruling: cannot-verify — the two extra rp_filter keys (all, eth0) in firewall_test.go are what Task 17's ruling cv-17 requires (scoped-client test reads rp_filter under default, all and a named interface), read from the ledger and handoff.md, no seat dispatched — none
Task 71: Ruling: cv-71a — PARK: the IPv6 refusal is asked once on the first-enable path (for TransEnable) and not again before the follow-up reload; its inputs (the IPV6 setting and the loaded state cur) do not change between the two commands, so a second check would give the same answer — cost if wrong: a refusal that someday treats TransReload differently from TransEnable would go unasked on this path; one line in start fixes it
Task 71: Ruling: cv-71b — PARK: a repair of a UFW that is configured on but stopped falls into the reload branch and runs ufw reload, which matches the brief and spec 2.5 (reload skips only when ENABLED=no); whether a reload from a never-started state is predicted correctly belongs to the predictor and the wave 1 golden-file spike — cost if wrong: the repair's verify would catch a mismatch and the change would end in needs_repair, not pass silently
Task 72: Ruling: pc-72a — CONFIRMED-GAP: drop commit 8447d27 (fw.replacingAdd and TestPlanReplacingAdd) by revert and keep c8354bf, because neither the spec nor the plan requires a plan-time refusal of a rule add that would replace a rule, cv-59a parked it and amendment A31 refuses it at apply; handoff.md line 37 was wrong (the controller copied it into the dispatch) and is rewritten; Task 72 is re-reviewed against its brief alone — cost if wrong: the operator learns of the replacement at apply instead of plan time, no rule lost
Task 73: Ruling: cv-73a — PARK: tg.cmds in target.go holds only enableArgs, disableArgs or reloadArgs (written by transition and start), so transitionOp's fallback is reached only by reload and journals it as a modify of firewall.state:enabled by design — cost if wrong: a later task that adds a transition command without a transitionOp case would have it journaled as a reload-style modify and its inverse would be wrong; no current test would catch it
Task 73: Ruling: cv-73b — PARK: for a restore cfgN is the snapshot's own parsed configuration, so the edit by disableArgs or enableArgs rewrites only ENABLED to the current value (predict/edit.go) and the ufw.conf written carries the current ENABLED — cost if wrong: if a snapshot's ufw.conf ENABLED ever differs from its State.Enabled, the file is written as is and a revert may restore the wrong ENABLED; a restore-disables case in TestMutationRestore would catch it
Task 74: Ruling: pc-74a — CONFIRMED-GAP: in TestStateActionsVerify (actions_state_test.go) the verify assertions must also require the action's own mismatch message (e.Detail contains "after the change"), in the loop and in the disable assertion, because Enabled = false against an active loaded ruleset makes syncDiffs fail with the same code and a check that always returns nil would still pass; no plan amendment, no production change; prove it by making loggingSet's check return nil temporarily (the test must fail) and restoring it — cost if wrong: one stricter assertion in one test
Task 74: Ruling: cv-74a — PARK: the no-op on a repeated enable and the reload after a defaults or logging change are Task 73's defineMutation and plan code, covered by Task 74's tests — cost if wrong: none for Task 74, a Task 73 regression fails these tests
Task 74: Ruling: cv-74b — PARK: confirmed in code, defineMutation declares the lockout guard (handler.go:60), mutation.Plan passes m.guarded(in) to fw.plan, which runs planGuard on the predicted state (plan.go:101, :126-127), and enable's guarded returns in.Guarded — cost if wrong: none
Task 74: Ruling: cv-74c — PARK: every non-read plan goes through the lifecycle engine, which runs the validator before any handler Plan (engine.go:370, :517, repair.go:213); which validator the server and the break-glass CLI wire in was not checked (inference) — cost if wrong: an invalid policy from defaultOp would come back as a plain error instead of request.invalid_input, still refused before touching the host
Task 75: Ruling: pc-75a — PARK: the finding rests on a prediction the predictor does not make; matchTuple returns -2 for two rules that differ only in comment and setRule replaces the IPv4 tuple in place (predict/edit.go:350-363, :417-419), so a family: both add predicts a two-family group carrying the new id, and amendment A31's tuple-count check in runUfw (effects.go:156-161) refuses an add that would replace a rule at apply; the plan-time version was dropped on purpose (pc-72a) — cost if wrong: if predict.Edit did skip one half, A31's count check would pass and a v6-only rule would be recorded as a two-family request with no test to catch it (A31's test drives runUfw through a scripted edit, not predict.Edit)
Task 75: Ruling: cv-75a — PARK: Task 81 registers both actions (f.ruleAdd(), f.ruleDelete() in its list, a registration test expecting firewall.rule.add and firewall.rule.delete, and the schema hash schemas/firewall.rule.add/v1.json) — cost if wrong: neither action is reachable through the engine until Task 81, whose test fails if either is missing
Task 75: Ruling: supplied the TMPDIR /root/.cache/w1-tmp/task-75 in place of the brief's /root/.cache/w1-tmp/75 — handoff.md gotchas say TMPDIR=/root/.cache/w1-tmp/<task> and the dispatch carried it — none
Task 76: Ruling: cv-76a — PARK: the manual-rule delete guard is Task 72's fw.plan (FirewallManualRequired when manualChanges is non-empty and manual is false) reached through Task 73's defineMutation, and Task 76 passes manual: in.Manual; the closing ufw reload comes from Task 71's predictTarget and Task 73's fw.run; Task 81 lists f.rulesApply() in fw.actions (plan line 40182) — cost if wrong: firewall.rules.apply is unreachable through the registry until Task 81, whose review should confirm the entry; a regression in the shared code would also fail Tasks 74 and 75's tests
Task 77: Ruling: pc-77a — AMEND: the restore's own capture prunes by retention before the change row exists (the engine calls Snapshot before it records the change), so restoring the oldest retained snapshot deletes it and the apply fails with "no snapshot has the id"; mutation gains a keep hook that Snapshot pins around capture, snapshotRestore sets keep to in.ID, in a separate commit by Task 77 touching handler.go (the mutation struct and Snapshot only) — cost if wrong: a crash between pin and unpin leaves the target pinned, one extra kept snapshot; the header contract for mutation.Snapshot does not mention the hook, so a later reviewer may ask (point to pc-77a)
Task 77: Ruling: amendment A38 — the restore's own capture prunes the target snapshot before the change row exists; pin the target for the capture (ruling pc-77a) — if wrong, a crash between pin and unpin leaves the target pinned and one extra snapshot is kept; without the fix the oldest listed snapshot cannot be restored
Task 77: Ruling: cv-77a — PARK: a restore's removed manual rules are already refused without manual: true by fw.plan through manualChanges (diff.go collects both directions), covered by TestPlanRefusals and diff_test.go; a restore-specific removal case would only add coverage — cost if wrong: a restore that dropped a manual rule without listing it would lose it unprompted, unlikely because the code is shared with firewall.rule.delete
Task 78: Ruling: cv-78a — PARK: every name snapshots.go consumes exists in an earlier task's file with the shape it uses (loadSnapshot(ctx, env, id, path) in actions_snapshot.go, snapshotConfig in target.go, logical and stateDiff in diff.go, listSnapshots, snapshotFilter and fileHash in store.go, fwstate.StateFiles, readConfig and readLoaded in deps), and the passing suite and lint at 37562b5 settle the rest — cost if wrong: none expected, the next task's build would fail on a missing symbol
Task 78: Ruling: cv-78b — PARK: Task 81 registers both (fw.actions() includes f.snapshotCompare() as firewall.snapshot.compare, fw.resources() includes core.DefineResource(ID+".snapshots", f.snapshots), asserted by its TestManifestActions, plus the two schema files) — cost if wrong: the resource and the compare action would be unreachable through the API, CLI and web if Task 81 were dropped; the plan does not drop it
Task 79: Ruling: cv-79a — PARK: defineMutation (handler.go:61) sets spec.RepairedBy = repairAction ("firewall.state.repair") for every mutating firewall action (rules.apply, state, defaults, logging, rule.add, rule.delete, snapshot.restore, and the repair itself), and TestMutationDefinition checks the produced spec; only the snapshot compare read is built another way and needs none — cost if wrong: a future mutating action that skipped defineMutation would get no RepairedBy and no test would catch it
Task 79: Ruling: cv-79b — PARK: repairChange returns change{repair: true} on all three paths, planGuard skips the out-of-sync refusal for a repair confined to UFW's and Darkpanel's chains and runs the normal lockout guard (plan.go:114-125), and target.go uses c.repair to reload or stop UFW; TestRepairDirect exercises it end to end — cost if wrong: an out-of-sync host could not be repaired, the existing test would fail
Task 79: Ruling: cv-79c — PARK: every production entry point validates the input before Plan (engine.makePlan is reached only through e.canonical, which calls Validator.ValidateInput; recheck at apply re-plans from the validated stored input; the dispatcher, the agent runtime client and the break-glass CLI all reach Engine.Plan), so the unknown-outcome mismatch is reachable only from tests that call the handler directly — cost if wrong: a new production caller of Handler.Plan outside canonical would record a wrong repair outcome; the one-line hardening is to refuse any outcome other than "", restore or keep in repairChange
Task 80: Ruling: cv-80a — PARK: the module's Uninstall runs while firewall_snapshots and its rows still exist; the uninstall sequence is internal/install/uninstall.go (barrier, services, agent lock, modules via runModules, marked blocks, files, units, binary, data), agent.db is open from PlanUninstall's load and deleted only by the last data step, runModules passes Env{DB: u.db}, and the module is registered through modules.All() (modules.go:22) with value receivers so the core.Uninstaller assertion succeeds — cost if wrong: rules=baseline would do nothing or fail at the modules step on a real host, rules=remove unaffected
Task 81: Ruling: pc-81a — PARK: the Task 81 test stands without the rp_filter all and eth0 reads Task 70 added; what cv-17 needed (the controller unit and the net/ipv4/conf/<iface>/rp_filter owned path with ^[012]$) is still pinned exactly by TestManifestDeclaresTheHost, rp_filter is still read under default, the <iface> expansion is shared platform code covered by the all and named-interface cases of other keys, and the extra keys were Task 70's own addition, not plan text — cost if wrong: a per-key fault in rp_filter under all or a named interface would show at run time, not in this test (fix: two map entries)
Task 81: Ruling: cv-81a — PARK: TestSnapshotsCurrent (internal/modules/modules_test.go) binds the generated schemas and passed in the suite-run at a5064cc; the controller also ran find schemas -path 'schemas/firewall.*' -type f | LC_ALL=C sort | xargs sha256sum and all 12 sums equal the plan's (lines 40199-40210) — cost if wrong: none, a stale schema fails every later suite run
Task 81: Ruling: cv-81b — PARK: LockKeys, RepairedBy and Guards are set in one place by defineMutation (handler.go:61-62, confirmed by cv-79a) and have no JSON schema field by design; TestManifestActions asserts them for every action including Converges only for firewall.rules.apply, and the suite-run shows the firewall package passing — cost if wrong: none expected, a missing field fails TestManifestActions in every later suite run
Ruling: wave 69 base proof — ledger holds no closing line after Wave 34 (waves 35-68 ran as one-task waves), so the full Integration check ran at a5064cc → pass, 61 pass 0 fail; base-proof-failures.txt written empty — none
Task 82: Ruling: cannot-verify (no raw output for the package-wide checks) — settled by the controller reading the suite log .dforge/suites/1793117720-2848883.log from the task worktree: 61 ok packages, no FAIL, panic or warning lines — none
Task 83: Ruling: cv-83a — PARK: the expected guard strings and both action inputs agree with the committed guard code and action schemas; only the runtime behaviour on the VM fleet is left for the gate-7 -m3 harness run — if wrong, the -m3 run fails one refusal subtest at its strings.Contains check, costing one harness rerun and a one-line test-only edit
Task 83: Ruling: cv-83b — PARK: Task 83 changes only two files behind the harness build tag, so go test -race ./internal/cli/ cannot depend on it; the wave-end Integration check runs go test ./... on the merged head — if wrong, a race in internal/cli would be another task's
Group 84,85: Ruling: review seat dforge:judge-sonnet-high wrote a malformed report (line 16 "- (Task 85) (1 more Minor left out)" is not a finding line) — deleted the report and re-dispatched the same seat once with the same prompt file — none
Task 85: parked — repair-reverts passes silently when the ordinary change is refused with any API error — Ruling: pc-85a PARK: the brief's code stands because Task 101 replaces this exact switch (m3xOutOfSyncRefusal, every plan error fatal) and its Old block is the brief text byte for byte; Task 101 must stay in the run order and its review must confirm the replacement landed — cost if wrong: if Task 101 is dropped, repair-reverts keeps passing on any API error from the plan route
Task 85: Ruling: cv-85a — PARK: the apply route answers 200 with the change body and a failed verify reverts and keeps firewall.prediction_mismatch (actions.go runAction, lifecycle/apply.go failApply, handler.go Verify) — cost if wrong: a run over the 50 s SyncAnswer returns 202 applying and the step fails loudly
Task 85: Ruling: cv-85b — PARK: preflight only needs /usr/sbin/ufw to be a regular file and reads the version from dpkg status, so the wrapper passes — cost if wrong: systemd hardening could still block the wrapper, a loud failure in the first harness run
Task 85: Ruling: cv-85c — PARK: keep the 30 s repair timeout; the first VM run settles it, and raising it to 60 means editing Task 85 and both of Task 101's blocks together — cost if wrong: one red harness run and a three-line amendment
Task 86: Ruling: cv-86a — PARK: the golden layout (rules/commands.txt, save/nft-v4-filter.txt, save/nft-v6-filter.txt) exists for all four OSes and the commands run under sudo sh -c — cost if wrong: the step fails loudly at its first read in the harness run
Task 86: Ruling: cv-86b — PARK: the unqualified iptables-save is right, every golden version.txt reads (nf_tables) and nothing in the repo changes the alternatives; that the VM images default to nft is inference from package defaults — cost if wrong: the golden comparison fails for a reason unrelated to UFW on the first harness run
Task 86: Ruling: cv-86c — PARK: the refusal codes and detail strings agree with the committed guard and predict code; sshd pre-auth process names, LoginGraceTime timing, nft quoting and Docker re-sync need the gate-7 harness run — cost if wrong: a scenario fails at gate 7 with a message naming the difference
Task 87: Ruling: cv-87a — PARK: Task 91 Step 3 already wires verifySysctl(..., st.Sysctl) with before = snap.State.Sysctl; Task 91's review must check the four points in rulings-cannot-verify-87-report.md (st.Sysctl from the same readLoaded result as the chain comparison, before from snapshotOfChange(env.ChangeID), the no-sysctl early return before the snapshot lookup, the mismatch through mismatch() so the code is firewall.prediction_mismatch) — cost if wrong: swapped or wrong maps make a start verify wrongly or fail falsely, which the handler tests Task 91 adds should catch
Task 88: Ruling: cv-88a — PARK: the manifest already declares /etc/ufw/sysctl.conf as an unconditional owned restore path (Task 70 added it in ownedPaths in firewall.go, Task 81 kept it) and the exact owned-path list in firewall_test.go would fail if it were dropped or gated; the final review only confirms that test passes at HEAD — cost if wrong: a gated entry would leave the sysctl file out of restore on some hosts, which that test catches
Ruling: wave 74 runs one task after another — scripts/context-size --wave 2 says handoff (232k of 210k), plain context-size says ok (232k of 350k); Task 89 then Task 90 run in the checkout through the ordinary loop — a slower wave, the wave check follows check-due
Task 89: Ruling: cv-89a — PARK: Task 102 already wires the topology runtime source to Listening alone (startRuntimeListeners calls s.topo.setRuntime(s.runtimeListeners.Listening) and startServices no longer registers Addrs); Task 102's review must confirm those two wiring points and that topology.read takes all three results from one call (the remaining Unbound use in withUnbound is the listener-state message and is allowed) — cost if wrong: Addrs then Unbound could miss a listener that rebinds between the reads
Task 90: Ruling: cv-90a — PARK: only gate 7 (harness.yml on the pushed branch) shows the step runs on runners and TestM3Firewall finishes inside 70 minutes; the plan header and handoff.md already put gate 7 before the finish merge, and Task 90's own text is met including the 60-minute m3Deadline — cost if wrong: the first gate-7 run fails or times out and the step needs a timeout change
Task 90: Ruling: cv-90b — PARK: the seat read .github/workflows/harness.yml at HEAD, the edits match the brief and the syntax looks valid; actionlint and yaml.safe_load were not rerun by the seat (no shell), the report's results stand — cost if wrong: the workflow fails to parse on the first push
Task 91: Ruling: cv-91a — PARK: in lifecycle/apply.go every Apply or Verify error goes to failApply, which reverts any revertible change whatever its errcode (the code is read only afterwards to fill the change record), so an unsupported_configuration error from verifySysctl reverts like firewall.prediction_mismatch; the seat read the code and did not run the focused test — cost if wrong: a coded non-mismatch Verify error would skip the revert, which the e2e tests Task 91 added would not show
Ruling: wave 76 runs one task after another — scripts/context-size --wave 2 says handoff (268k of 210k), plain context-size says ok (268k of 350k); Tasks 92, 93 and 94 run in the checkout through the ordinary loop in index order — a slower wave, the wave check follows check-due
Task 92: Ruling: cv-92a — PARK: the plan Assumptions (lines 2230-2231) record the source of the rows (iptables 1.8.11 tarball with its sha256, fetched 2026-10-26; a debian:13 container probe of 227 spellings, scripts and output in F1Ufix-scratch); a container re-run, if wanted, belongs to gate 7 or the final review — cost if wrong: a wrong row keys a rule differently from iptables-save and shows as a false out-of-sync reading
Task 92: Ruling: cv-92b — PARK: a search of the whole checkout found no other user of icmpTypes, icmp6Types or icmpType, and icmpTypeNumber appears only at key.go:156, :158 and its definition at :233; go build and vet passed at 48bbc8f per the report — cost if wrong: a build-tagged file using a removed identifier fails to compile, which the integration check catches
Task 93: Ruling: cv-93a — PARK: the measurement that writing net/ipv6/conf/all/forwarding sets every interface including lo is recorded in the plan Assumptions (line 1922, 2026-10-24, and the Task 93 ruling at line 2217); Assumptions line 1920 still says only non-loopback interfaces are read, stale plan prose no task executes — cost if wrong: none for the code, a reader of the Assumptions could be misled
Task 93: Ruling: cv-93b — PARK: the ledger records A32/pc-60a (Task 60 ruling, progress.md) and amendments.md A32 adds files_sysctl.go and files_sysctl_test.go to Task 93's file list; the platform commit 45d23f0 came before d5ae787 — cost if wrong: none
Task 94: Ruling: pc-94a — CONFIRMED-GAP: delete TestGxLiveHostRoutes and the then-unused "testing" import from guard/routes_test.go, keep gxDumpHost, its Routes method and the three compile-time assertions, commit as test(firewall): drop the live route dump test; Part G's rule that only TestLiveHost touches the live host binds Task 94 and netstat's own tests already run a live route dump — cost if wrong: a break in the LiveHost.Routes pass-through goes uncaught in package guard until the harness runs
Task 94: Ruling: cv-94a — PARK: Task 113 uses gxDumpHost's dumps and err fields exactly as Task 94 defines them; Task 113's review must check the dumps counts in TestGxCheckIPv6Forwarding (one IPv6 family dump only when a value changes), the dumpErr case through the fake's err, and that Task 113 does not redefine gxDumpHost — cost if wrong: Task 113 fails at its own GREEN step
Ruling: wave 77 runs one task after another — scripts/context-size --wave 2 says handoff (314k of 210k), plain context-size says ok (314k of 350k); Tasks 95, 96 and 97 run in the checkout through the ordinary loop in index order — a slower wave, the wave check follows check-due
Task 96: Ruling: cv-96a — PARK: the manual check in fw.plan (plan.go:93) runs for keep and no-change repairs but finds nothing, because manualChanges compares only rules and both builds return change{repair: true} with no ops and no restore; only the restore build swaps in the snapshot rules, so firewall.manual_required can come only from there, as the contract says (the seat did not open writeUser and ran no tests) — cost if wrong: a keep or no-change repair could be refused with firewall.manual_required, which the repair tests would show
Ruling: wave 78 runs one task after another — scripts/context-size --wave 2 says handoff at this size and plain context-size says ok (339k of 350k); Tasks 98, 99 and 100 run in the checkout through the ordinary loop in index order — a slower wave, the full check follows at the wave close
Task 98: Ruling: cv-98a — PARK: netfilter.Key renders a bare jump as "-j <target>" for both families (key.go adds options only for set prefixes, interfaces, protocols and matches, and no step depends on the family for a rule with no matches), so foreignKeys' bare-jump test is right; key_test.go has no test asserting the bare-jump string itself, the TestConfined cases cover it through foreignKeys — cost if wrong: a bare jump is reported as foreign and a repair is refused, which three TestConfined cases would catch
Task 98: Ruling: cv-98b — PARK: the image.go half of pc-68a is carried to the final review by handoff.md (the line starting "Task 98's dispatch MUST carry the Task 68 ruling pc-68a") and by the ledger; Task 98's sync.go half is implemented as ruled — cost if wrong: image.go restores treat a matched jump to a UFW chain as UFW's own, a fail-open safety gap for that one case
Task 98: Ruling: cv-98c — PARK: Task 108 Step 3 Edit 2 replaces syncDiffs with verifyDiffs in mutation.Verify with the signature Task 98 defines; Task 108's review must check the five points in rulings-cannot-verify-98-report.md — cost if wrong: verify keeps comparing with syncDiffs and a reload that drops a before.rules nat rule passes verify, which TestE2EVerifyNatRule catches
Task 99: Ruling: pc-99a addrtype and other undecidable values in a modelled module are never listed — CONFIRMED-GAP: In `internal/modules/firewall/eval/unsupported.go`, replace the module loop at the end of `ruleGap` with `for _, m := range r.Matches { if why := matchGap(m); why != "" { return why } }`. Add an unexported `matchGap(m netfilter.Match) string` (doc comment: "matchGap says why the walk cannot decide m for any packet its module applies to, or "" when it can."). It returns `"match " + m.Module` when `!modelledModules[m.Module]`, as today. Otherwise it loops over `m.Options` and returns the first of these value gaps. (1) `addrtype`: when no comma-separated type in `o.Values[0]` is one of `LOCAL`, `UNICAST`, `BROADCAST`, `MULTICAST`, return `"match addrtype type " + o.Values[0]`. A list mixing a modelled type with an unmodelled one is packet-dependent (for example, `LOCAL,BLACKHOLE` decides every input packet) and is not listed. (2) `mark`/`connmark`: when `parseMark(o.Values[0])` fails, return `"match " + m.Module + " value " + o.Values[0]`. (3) `tcp-flags`: when `flagSet` fails on `o.Values[0]` or `o.Values[1]`, return `"match tcp flags " + o.Values[0] + " " + o.Values[1]`. (4) `sport`/`dport`/`sports`/`dports`: when `portRange` fails on any comma item of `o.Values[0]`, return `"match " + m.Module + " port " + o.Values[0]`. (5) `ctorigdst`: when `o.Values[0]` parses as neither `netip.ParsePrefix` nor `netip.ParseAddr`, return `"match conntrack original destination " + o.Values[0]`. Update the `modelledModules` doc comment to say a modelled module can still hold a value the walk cannot read, which `matchGap` lists. In `unsupported_test.go`, add four `TestUnmodelledAgreesWithEvaluate` cases: `{"-A INPUT -m addrtype --dst-type BLACKHOLE -j DROP", "tcp", true}`, `{"-A INPUT -m addrtype --dst-type LOCAL,BLACKHOLE -j DROP", "tcp", false}`, `{"-A INPUT -p tcp -m tcp --tcp-flags SYN,ECE SYN -j DROP", "tcp", true}` and `{"-A INPUT -m mark --mark zz -j DROP", "tcp", true}`. Add one `TestUnmodelled` case expecting `[]string{"mangle/PREROUTING#1: match addrtype type BLACKHOLE"}` for `-A PREROUTING -m addrtype --dst-type BLACKHOLE -j DROP`. Edit no other file; `TestUnmodelledFixtures` must still pass. — The plan's Step 3 code is what the reviewer flagged, but it does not mandate treating every value of a modelled module as decidable. Task 99's own brief and the binding contract in the part A2fix constraints both require that `Unmodelled` and `Evaluate` agree. Spec 2.1 says an evaluator gap in mangle or security is a hard, non-overridable refusal, and `match.go:285` returns undecidable for every unicast packet against the address types iptables-save really prints (BLACKHOLE, UNREACHABLE, PROHIBIT and others). Today such a host gets only the lockout guard's overridable unknown, so an operator can override past a spec hard refusal, and no test catches it. Covering only addrtype would leave the same break, less likely to be reached, in the other four parse-failure paths of the walk. Each costs about one line, so all five are required. The header's contract sentence still names only `modelledModules` and `targetGap`. Because `matchGap` is called from the same `ruleGap` and the agreement test now covers it, that drift is accepted. — If wrong (the extra cases over-list), a host whose mangle or security rule uses an unreadable value is refused as unsupported instead of failing an overridable guard. Since `Evaluate` returns unknown for that rule either way, the operator loses only the override.
Task 99: parked — pc-99b copiesMark exemption and agreement-test sampling (Minor) — Ruling: Leave both Minor findings deferred as the ledger records them. The pc-99a fix covers neither. — The `copiesMark` exemption depends on a mark value that an earlier rule in the ruleset fixed. That is ruleset state, not a match value, so `matchGap` does not reach it. The new agreement cases are still hand-picked samples, not cases derived from the walk's undecidable reasons. Neither finding is built on by any later task, and both fail safe (unknown, never accept). — If wrong, a rare ruleset (a fixed mark followed by an undecidable CONNMARK copy) leaves a host with an overridable unknown instead of the hard refusal. A later pass can close that by having `copiesMark` list a copy whenever an earlier rule in the same table sets that mark.
Task 99: parked — cv-99a Task 109 consumes eval.Unmodelled — Ruling: The requirement is carried by Task 109, and nothing in Task 99 is unmet. — Task 109's plan text calls `eval.Unmodelled(f.rs)` for `st.V4` and `st.V6` inside `unmodelledRules(st fwstate.Loaded) []string`, which matches the signature Task 99 defines and the part E constraint (`func Unmodelled(rs *netfilter.Ruleset) []string`). `hostUnsupported` puts the configured reasons, then the loaded reasons, then those rules into a non-nil slice. Task 109's review must check four things. (a) `hostUnsupported` returns that order and is never nil. (b) Each rule renders as `<tool>-<variant> <table>/<chain>#<n>: <what>, which the evaluator cannot model`. (c) `fw.prepare` refuses with `firewall.unsupported_configuration` for both the current loaded state and the predicted `tg.pred`, and an override never passes that refusal. (d) `Status.Unsupported` carries the same list. The review should also confirm that its `ayMangle` rule is still listed after the pc-99a fix. — If wrong, a refusal-wording or ordering defect surfaces at Task 109's review rather than here, which costs one review round.
Task 101: parked — cv-101a plan endpoint accepts overrides ["firewall.lockout"] and returns a failing guard on an out-of-sync host — Ruling: PARK: the code supports m3xOutOfSyncRefusal's assumption (the plan carries a firewall.lockout guard with OK false and Overridden false, not an error); gate 7 must confirm on the VM fleet that the real request path and the real out-of-sync state (not also an unsupported configuration) produce this and that both plan responses decode into m3Plan.Guards — cost if wrong: m3RepairReverts fails loudly through t.Fatalf on the VM fleet, one red gate 7 run and a one-line fix
Task 102: parked — cv-102a merge with Task 111's topology.go edit — Ruling: PARK: Task 111's four topology.go Old blocks and its server.go anchor each occur once, byte for byte, in the committed files and no task between 102 and 111 edits either file; Task 111's review must check that topology.go's Complete line has both !closed and runtimeUnbound == 0, that closed is read under t.mu with the four sources in one lock hold, and that s.topo.close() runs in Shutdown before the first closer — cost if wrong: a merge that dropped runtimeUnbound == 0 would read the topology complete during a runtime listener's bind retry, caught only by TestTxTopologyWaitsForRuntimeListeners
Task 102: parked — cv-102b Runtime.Listening reads its three results under one lock — Ruling: PARK: listen/runtime.go Listening takes r.mu once and reads addresses, dropped labels and the unbound count before one Unlock, so the requirement is met — cost if wrong: none found
Task 103: parked — cv-103a a manual rule re-added by an undo without its unsafe comment while the diff text names the comment — Ruling: PARK: the plan's Task 103 rulings and the owner decision of 2026-10-26 accept that a revert re-adding a manual rule without its unsafe comment ends needs_repair and a repair restores the file; diff.go ruleText only describes rules in a plan diff and no plan writes a manual rule through a vector, so the operator is not misled; the final review must check that such a revert ends needs_repair, not reverted — cost if wrong: an undo could report reverted while the rule's comment is missing from the file
Task 104: parked — cv-104a real UFW 0.36.2 keeps built-in chain rules across reload and disable so copies accumulate — Ruling: PARK: the code stands; the debian:13 run dp-w1-P-5 (plan Assumptions) saw a before.rules nat rule appear once, twice and three times across enable, reload and enable, and a foreign INPUT rule survive disable, and the iptables-restore --noflush load of before.rules and after.rules does not depend on the table; no capture used a *filter block and every golden before.rules and after.rules is the stock file, so the filter-table behaviour is inferred, not captured; the final review must record that — cost if wrong: verify fails safely with firewall.prediction_mismatch and reverts on each reload, and nothing unpredicted is applied
Task 105: parked — cv-105a the evaluator accepts ("output", "lo") for local output packets and treats mark 0x80000 as known — Ruling: PARK: the merged evaluator accepts an output packet on lo (eval.go validation), takes a non-zero output mark as the socket's exact mark (startMarks), and reroute accepts the router answer ("output", <non-empty>) that Task 105's route returns; gate 7 or the final review must confirm on a real host with tailscaled that both IPv4 tailscale flows (UDP 41641 and TCP 443, mark 0x80000) evaluate to Accept, that a local output flow to a host address leaves through lo, and that the IPv6 tailscale flows pass (no test runs them end to end) — cost if wrong: a tailscaled or local-output flow comes back unknown and the guard fails closed
Task 106: Ruling: review seat dforge:judge-opus wrote a malformed report (a finding line began with a [lens:security] tag before the severity) — deleted the report and re-dispatched once with the same prompt file — none
Task 106: Ruling: review seat dforge:judge-opus wrote a malformed report a second time (the [lens:security] tag placed after the severity, before the path; the verdict grammar needs '- [Severity] path:line — text') — deleted the report and dispatched a fresh dforge:judge-opus with the exact line format and the tag after the dash — none
Task 106: Ruling: cv-106a — AMEND: (a) is met and (b) is only partly met, so Task 114's text gets two edits: A1 adds a test, and A2 fixes the Warn log so it carries the real decode error — On (a), Task 114's plan text has exactly two callers of the emergency path. `restore` passes nil, and `emergencyRestore` passes `mustJSON(emergencyRecord{...})`, which is always non-empty, valid JSON (mustJSON panics rather than return bad output; see internal/modules/firewall/observe.go). So no caller stores an empty or invalid payload, and the Task 106 minor (store.go:398) is not reached by anything the plan builds. On (b), `finishRestore` falls back to a normal restore with `env.Log.Warn`, but there are two problems. First, the `err` it logs is the outer `err` from `restore`, which is always nil at that point, because the decode error lives only inside the `if` statement's scope. Second, no test covers an empty or malformed record. Task 114's review must check three things: the new test passes; the Warn carries `readErr`; and no caller other than `restore` and `emergencyRestore` reaches `restoreOver`, `beginRestoreRecord` or `markEmergency` with an emergency payload — Cost if wrong: a stored record that cannot be read would still end as a plain revert, which is the intended fallback. The cost is only one extra test and a log line that is more useful than before.
Task 106: Ruling: amendment A39 (Task 114) — adds TestAxRevertFinishesAnUnreadableEmergencyRecord with an empty and a malformed stored emergency record (cv-106a) — cost if wrong: an unreadable record's fallback to a normal restore stays untested
Task 106: Ruling: amendment A40 (Task 114) — finishRestore's Warn logs the record's decode error instead of the always-nil outer err (cv-106a) — cost if wrong: the log line names no cause for an unreadable record
Task 107: parked — cv-107a the REJECT and LOG spellings match iptables 1.8.11 as the test comments say — Ruling: PARK: the plan records the container measurement (plan lines 2230-2233, Review fix F1Ufix assumptions: debian:13 container dp-w1-F1U-1, iptables and ip6tables 1.8.11 nf_tables, 43 REJECT spellings in both families and 20 LOG levels, test rows generated from probe.out and probe2.out); the probe scripts are uncommitted scratch and probe.out was not opened — cost if wrong: Key could treat two spellings of one target as different and report a false difference, which verify catches as a mismatch
Task 108: parked — cv-108a verifyDiffs covers the built-in chains of all five tables — Ruling: PARK: verifyKeep in sync.go accepts any chain named PREROUTING, INPUT, FORWARD, OUTPUT or POSTROUTING whatever the table and loaded.Compare loops over both families and every table on either side, so raw, mangle and security take the same code path as the nat cases TestVerifyDiffs and TestE2EVerifyNatRule run; the tests name only nat and filter — cost if wrong: a built-in chain of raw, mangle or security would not be compared after a start and a reload that dropped its rule would pass verify
Task 110: Ruling: the brief's Step 1 replace block occurs 96 times in m3_firewall_helpers_test.go, so the implementer appended TestM3xOutOfSync at the end of the file — same content as the brief, only its position differs — none
Task 111: Ruling: cannot-verify (the full suite reported only as a summary) — settled by the controller opening .dforge/suites/1793151473-212335.log: 61 lines read ok, no other lines — none
Task 112: Ruling: pc-112a a dp-panel runtime listener with no proxy address gets no flow, against Part G's fail-closed rule — AMEND: In Task 112, a controller listener on a `dp-panel` address whose next address lies outside the bridge prefix becomes one `unknown` flow labelled `controller listener` with reason `the runtime listener <addr:port> on dp-panel has no proxy address in its network`, instead of no flow; the test case "a runtime listener on a bridge address without a proxy address" expects that flow, and Step 2's expected RED output lists that subtest. Apply entries A1 to A5 below, all of which belong to this item. The implementer then changes flows.go and flows_test.go to match in a fix commit. Why: the finding is real. Spec 2.3 (spec lines 2788 and 2808-2811) says a flow whose path cannot be determined is `unknown` and fails the guard, and Part G's constraint (brief line 359) says a flow the guard cannot place is `unknown`. The Gfix constraint (brief line 423) does not license dropping the flow, so the narrower "no flow otherwise" in the Task 112 contract (brief line 1880, Part contracts) is the plan's error. With no flow, a change can close the controller's runtime port on such a host and the guard never reports it. The Part contracts paragraph lies outside the task and cannot be amended here, so the New prose states that Task 112's text supersedes it. Cost if wrong: on a host whose `dp-panel` does not follow spec 8's layout (a /29 with gateway .1 and proxy .2), every firewall change fails the guard with an overridable `unknown` until an operator overrides it. That is fail-closed and cheap. Today such a host gets a silent hole.
Task 112: parked — pc-112b proxy address assumed to be the gateway's next address; panelProxy ignores whether dp-panel is up (Minor) — Ruling: Leave both Minor findings deferred as the ledger has them. The pc-112a amendment covers neither of them. Why: the broadcast concern cannot arise under spec 8. The spec (lines 4209-4215) gives `dp-panel` a /29 with the gateway at `.1` and the proxy at `.2`, so the gateway's next address is never the broadcast address. The ignored `Up` state fails closed, because a down `dp-panel` is not an eligible in-interface (spec line 2786, Part G's "no eligible in-interface" is `unknown`), so the result is a stricter verdict and never a wrong `accept`. Nothing downstream builds on either. Cost if wrong: on a non-spec bridge layout, the guard checks a flow from a broadcast source that cannot exist, which can only produce a spurious failure, never a hole.
Task 112: parked — cv-112a the proxy container sits at the bridge gateway's next address — Ruling: The requirement is carried by the spec. Section 8's panel exposure steps (spec lines 4209-4215) create network `darkpanel-panel` with bridge `dp-panel` on a free /29, with "gateway `.1`, proxy address `.2`". They attach the proxy container (NPM or cloudflared) "with the static address `.2`". Step 3 (lines 4216-4218) puts the runtime listener on `<gateway>:9443` with allowed peer `.2`, and step 6 (lines 4225-4226) adds `allow in on dp-panel from <proxy> to <gateway> port 9443`. So the proxy really is the gateway's next address. Wave 2's review must confirm three things: `panel.network.create` sets the subnet to a /29 with the gateway at `.1`; `npm.network.attach` and `cloudflare.network.attach` pin the static address `.2` instead of letting Docker assign one; and the runtime listener binds the gateway address. Cost if wrong: if wave 2 lets Docker assign the proxy address, the guard models the wrong source and its verdict on the runtime listener can be wrong in either direction.
Task 112: parked — cv-112b ckHost holds no controller socket that would now produce a listener flow — Ruling: `ckHost` (internal/modules/firewall/guard/guard_test.go:53-69) holds no controller listening socket. Its sockets belong to `sshd` (PID 10), `sshd-session` (PID 11), `darkpanel-agent` (PID 99) and `tailscaled` (PID 300). `ckRunning` (guard_test.go:112-114) sets the controller's MainPID to 4242. `heldBy` (flows.go:167-174) is called with only that PID and no command names, so no `ckHost` socket matches and no listener flow is added. The existing `Check` expectations are unchanged, which agrees with the suite log the controller has already confirmed. Cost if wrong: none beyond a test expectation that a later task would catch.
Task 112: Ruling: amendment A41 — part of the pc-112a AMEND (an unknown flow for a dp-panel runtime listener with no proxy address: prose, test case, expected failing output, doc comment and code) — cost if wrong: a closed runtime port on such a host goes unreported by the guard
Task 112: Ruling: amendment A42 — part of the pc-112a AMEND (an unknown flow for a dp-panel runtime listener with no proxy address: prose, test case, expected failing output, doc comment and code) — cost if wrong: a closed runtime port on such a host goes unreported by the guard
Task 112: Ruling: amendment A43 — part of the pc-112a AMEND (an unknown flow for a dp-panel runtime listener with no proxy address: prose, test case, expected failing output, doc comment and code) — cost if wrong: a closed runtime port on such a host goes unreported by the guard
Task 112: Ruling: amendment A44 — part of the pc-112a AMEND (an unknown flow for a dp-panel runtime listener with no proxy address: prose, test case, expected failing output, doc comment and code) — cost if wrong: a closed runtime port on such a host goes unreported by the guard
Task 112: Ruling: amendment A45 — part of the pc-112a AMEND (an unknown flow for a dp-panel runtime listener with no proxy address: prose, test case, expected failing output, doc comment and code) — cost if wrong: a closed runtime port on such a host goes unreported by the guard
Task 113: parked — cv-113a firewall manifest sysctlKeys covers net/ipv6/conf/<iface>/forwarding — Ruling: No change; firewall.go sysctlKeys (line 27) declares net/ipv6/conf/<iface>/forwarding, Manifest owns every entry, firewall_test.go line 67 pins it, and the platform treats <iface> as a wildcard matching all, default or any valid interface name — cost if wrong: a forwarding key the manifest did not declare would make the live read fail with a "does not declare" error
Task 114: Ruling: cv-114a — PARK: Task 114's Step 2 red fails under the base code for the reason the brief states (both subtests stop at revert_test.go:466, the !out.Override check); the suite log only holds the green run, so the ruling rests on the quoted red output and the code — cost if wrong: the new tests might not have been red against the old finishRestore
Ruling: group 116 review seat (judge-sonnet-high) returned its report in the reply and wrote no report file — re-dispatched once with an instruction to write the file with its Write tool — one extra seat run if the second try also fails
Task 115: parked — cv-115a merged tree builds — Ruling: carried by the plan header's Integration check, which runs on the wave head after the merge; Task 115 edits only netfilter/key.go and key_test.go — cost if wrong: a merged-tree build break would show at the wave check
Task 116: parked — cv-116a tailscaled holds a UDP socket on the logged-out agent VM — Ruling: carried by gate 7 (docs/dforge/gates.md), which the plan header's Global Constraints tie to Task 116: the harness.yml run on the pushed branch must show refuse-tailscaled-egress neither skipping nor failing at m3xTailscaledSocket — cost if wrong: the step fails on the VM, caught at gate 7
Task 116: parked — cv-116b the refusal text contains "tailscale from " — Ruling: produced by tailscaleFlows in guard/flows.go and asserted by guard_test.go lines 108 and 367, not by Task 116; gate 7 confirms it end to end — cost if wrong: m3RequireCut would not match on the first real run
Group 116: Ruling: review seat dforge:judge-sonnet-high returned its first report in the reply without writing the file — re-dispatched the same seat once with an instruction to write it — none
Ruling: wave 91 clean-up kept /root/repositories/darkpanel/.claude/worktrees/agent-afe3ed434613518f1 (Task 116's isolated worktree, locked by this session's own claude process, pid 1349849) — git worktree remove refuses a locked worktree and the lock is released only when the session ends; the implementer was stopped with TaskStop and its commit is merged — the worktree stays on disk until removed by hand
Task 117: Ruling: pc-117a — AMEND: activeNow returns (bool, error) and restoreOver returns that error before beginRestoreRecord, with a new test TestAxRestoreOverUnreadableChains (loadErr while another snapshot's record exists; no record of the snapshot remains and a retry runs ufw reload); the contract at plan line 1697, Task 117's text, Steps 1-4 and the test change with it — cost if wrong: a restore over a cut-off restore fails cleanly while the loaded state is unreadable, which it already did one step later
Ruling: header amendment A46 confirmed by a second judge-opus — AMEND, all eight entries agreed (A1-A8 of the first report, recorded as A46-A53) — if wrong, the plan's Contracts carry a bad activeNow signature into Task 117
Task 117: Ruling: amendments A47-A53 — parts of the pc-117a AMEND (task summary, second test, Steps 2-4, activeNow and restoreOver code) — cost if wrong: Task 117's fix lands against the wrong text
Task 117: parked — cv-117a beginRestoreRecord replaces another snapshot's record — Ruling: met and tested; beginRestoreRecord deletes then inserts in one write transaction, and TestRestoreIntent and Task 114's TestAxRestoreRecordKind assert the replacement — cost if wrong: the test's no-record assertion would fail, and both store tests would fail first
Task 119: Ruling: pc-119a — CONFIRMED-GAP: in revert (revert.go, inverses branch) load the before-image only when every snapshot state file is byte-for-byte what the snapshot captured; when a file differs set writeErr naming the changed files so the revert goes through the guard to needs_repair, with tests changed accordingly (spec 2.5 steps 3 and 4, plan line 124) — cost if wrong: a repair reverted over a manual edit ends in needs_repair instead of reverted
Task 119: Ruling: cv-119a — CONFIRMED-GAP, the same fix as pc-119a: spec 2.3's "unchanged files" wording assumes no manual edit and spec 2.5 steps 3-4 decide the case — cost if wrong: the same as pc-119a
Ruling: final review stopped at gate 6 and gate 7 (both red on pushed 1c0336a) — CI run 40628648672 failed in go (revive time-naming on two untouched files; the self-hosted runner may use another linter) and release-scripts (shellcheck missing on the runner); harness run 40628653109 failed on all four operating systems in TestM3Firewall (revert-on-reboot ends needs_repair, then six steps fail with 423 lock.held; debian-12 and debian-13 lack sysctl; ipv6-ssh-session fails with 409 manual_required) — reviewers not dispatched because a red gate stops the review; refuse-tailscaled-egress passed on all four, which settles cv-116a and cv-116b — if wrong, reviewers would spend seats on a branch the gates already show broken
Ruling: gates 6 and 7 cleared at 2153bb8 (CI and harness both success) — fixes: revive time-naming renames cc1fb37, pinned shellcheck b4266b0, setup-go check-latest 2153bb8 (the runner cached Go 1.27.1, govulncheck found stdlib advisories fixed in 1.27.2-1.27.3), lifecycle RunTask test race 4f0eacd (single-CPU follower poll, owner approved the frozen-package test edit), agent unit ordered after ufw, docker and tailscaled 7440575 (the reboot revert raced Docker's DOCKER-USER chains), harness sysctl via sudo, journal dump and change error detail — cost if wrong: a later red gate sends the branch back here
Ruling: pc-gate7-manual — firewall.state.enable and firewall.state.disable take manual: true (StateInput.Manual, schemas regenerated, commit 711d308) because starting UFW with IPV6=no empties user6.rules, so the plan answered firewall.manual_required with no input that could confirm it; the final review must confirm this additive v1 input is acceptable and that the web state controls need no change — cost if wrong: a contract field the owner did not want, removable before release
Ruling: web/src/modules/firewall/state-controls.tsx sends input {} for state.enable and state.disable; whether the shared change dialog can offer a manual confirmation for manual_required was not checked — carried to the final review (adjacent finding, not fixed)
Ruling: final review codex round skipped — codexReview is off — the Claude list and four lens reports are the only lists — a defect only Codex would find is missed
Ruling: final review — agent unit After=docker.service kept (reviewer Important: a dockerd that hangs at boot, TimeoutStartSec=0 upstream, keeps the agent from starting): without the ordering the reboot revert failed verify on every Docker host (harness run 40634754858), and a start job cannot be bounded in the unit — cost if wrong: a host with a hung dockerd has no agent, so no timer revert, until Docker starts or the operator intervenes
Ruling: final review — ci.yml check-latest kept (reviewer Minor: Go patch floats): the runner cached 1.27.1 and govulncheck failed on standard library advisories — cost if wrong: a CI result is not reproducible from go.mod alone
Ruling: final review — the manual input is added to LoggingInput, DefaultsInput and RuleAddInput as well as StateInput (reviewer Important: they hit manual_required on an IPV6=no host with leftover IPv6 rules and had no input to confirm it); the web confirmation flow for manual_required is deferred to a register row, not built in this wave — cost if wrong: the web cannot complete those actions on such a host; the API and CLI can
Ruling: final review — record-only items need no code: cv-104a (real UFW's re-append of built-in filter rules on reload is inferred from the nat-table run dp-w1-P-5, not captured), cv-105a (the IPv6 tailscale flows are evaluated end to end nowhere; refuse-tailscaled-egress covers IPv4), the Task 8 ruling (gate 7 passed but the harness logs do not show systemctl is-enabled nftables, so that fact is unverified on Ubuntu 22.04 and Debian 13) — cost if wrong: none for merge
Ruling: final review — spec edits (the ts-input sentence at spec line 2784, the refusal wording at line 2694) and folding migration 0002 into 0001 are not done in the fix wave — the spec is the owner's to correct and a migration fold is churn — cost if wrong: the spec keeps two inaccurate sentences
Ruling: residual-1 CONFIRMED-GAP — revert commit 65159e8 alone: pruneSnapshots' unpin of repaired and kept snapshots cleared the temporary pin a restore sets in mutation.Snapshot, so retention could delete the snapshot mid-restore; the original Minor (one snapshot left pinned after a failed repair unpin) stays open as accepted — cost if wrong: a snapshot that should be unpinned stays pinned after a rare failed unpin, slowly growing the snapshot table; no restore is affected (carried out as 091581a)
Ruling: residual-2 PARK — ci.yml keeps the x86_64 shellcheck tarball: the release-scripts job ran it green on the self-hosted runner (run 40637461670) — cost if wrong: if the runner becomes arm64 the shellcheck step fails loudly and the tarball must be made architecture-aware
Ruling: residual-3 CONFIRMED-GAP — comment-only correction of the two Vanished fields (restore.go, revert.go) — cost if wrong: one comment commit (carried out as d441ed6)
Ruling: residual-4 CONFIRMED-GAP — move TestTargetRestore's doc comment back above its function — cost if wrong: one test-file-only commit (carried out as 1a23255)
Ruling: deferred by the final review — the web confirmation flow for firewall.manual_required (the change dialog and state, logging and defaults controls send input without manual) is not built; the Go side accepts manual on state.enable, state.disable, logging.set, defaults.set and rule.add — cost if wrong: the web cannot complete those actions on an IPV6=no host with leftover IPv6 manual rules; the API and CLI can
```

## Amendments made

## A1 — Task 27
Reason: The whole-file replacement of snapshots-panel.tsx was written before Task 22's fix round (commit e668b0a), which made the restore dialog's Cancel button clear the manual-rules consent. As written, Task 27 brings back the stale consent and breaks the test "cancelling a restore clears the manual-rules consent". The new text matches the file at 045e6df.
Cost if wrong: None to the built code, because HEAD already matches the new text. If this amendment were left out, a rerun of Task 27 would bring back a consent bug, and only Task 22's test would catch it.
### Old
````text
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
````
### New
````text
          <Button
            variant="outline"
            onClick={() => {
              setManual(false)
              onCancel()
            }}
          >
            Cancel
          </Button>
````

## A2 — Header
Reason: The Part F2 contract entry for Task 29 (plan line 599) lists the reasons in the order Read checks them, but it has no reason for lines that UFW's init script and its Python `_get_defaults` read differently. Task 29's fix round (pc-29a and pc-29b) adds that reason as the first check: lines of `ufw.conf` first, then lines of `/etc/default/ufw`. The contract must state the exact accepted line forms, because the fix's tests reject a trailing `\r` and any value character outside the stated set.
Cost if wrong: Without it, an implementer or reviewer working from the contract would treat the new reason as out of contract or out of order. A looser wording would let a reviewer flag the strict check as over-refusing, or let a re-implementation accept lines the shell and Python read differently. That second case is a lockout-safety gap.
### Old
````text
- `func Read(root string) (fwstate.Config, error)` — reads `filepath.Join(root, p)` for every path (production `"/"`). `Files` holds every `fwstate.StateFiles` entry and, when `IPT_SYSCTL` is set, `/etc/ufw/sysctl.conf` (never another path), each a `core.RestoreEntry{Path, Present, Mode (permission bits), UID, GID, Data}`, absent files with `Present: false`; files are opened without following symbolic links. `Enabled`/`IPv6`/`ManageBuiltins` are `yes`/`YES` true, `no`/`NO` or unset false; `LogLevel` is lower-cased; policies are taken as written; `Rules4`/`Rules6` hold the parsed tuple lines in order (user6.rules is read whenever present); `SysctlFile` is `/etc/ufw/sysctl.conf` when `IPT_SYSCTL` names it (also through a path that cleans to it), the value as written when it names anything else, and `""` when unset; `Sysctl` holds `/etc/ufw/sysctl.conf`'s settings in order with keys through `SysctlKey`, and is empty when `IPT_SYSCTL` names another file, which is never read. Unsupported reasons, one each, in the order Read checks them: `ENABLED` not yes/YES/no/NO; LOGLEVEL missing or not in `LogLevels`; `IPV6` and then `MANAGE_BUILTINS` not yes/YES/no/NO; `MANAGE_BUILTINS=yes`; each DEFAULT_INPUT/OUTPUT/FORWARD_POLICY missing or not exactly ACCEPT/DROP/REJECT; a missing `user.rules`, `before.rules` or `after.rules` (and the `6` files when IPv6 is on); a tuple line `ParseTuple` refuses (`<file> line <n>: <error>`); an executable `/etc/ufw/before.init` or `after.init`; `IPT_SYSCTL` naming anything but `/etc/ufw/sysctl.conf` (`/etc/default/ufw: IPT_SYSCTL names "<value>"; only /etc/ufw/sysctl.conf is supported`); a sysctl key with a glob; a key `FilteringSysctl` reports.
````
### New
````text
- `func Read(root string) (fwstate.Config, error)` — reads `filepath.Join(root, p)` for every path (production `"/"`). `Files` holds every `fwstate.StateFiles` entry and, when `IPT_SYSCTL` is set, `/etc/ufw/sysctl.conf` (never another path), each a `core.RestoreEntry{Path, Present, Mode (permission bits), UID, GID, Data}`, absent files with `Present: false`; files are opened without following symbolic links. `Enabled`/`IPv6`/`ManageBuiltins` are `yes`/`YES` true, `no`/`NO` or unset false; `LogLevel` is lower-cased; policies are taken as written; `Rules4`/`Rules6` hold the parsed tuple lines in order (user6.rules is read whenever present); `SysctlFile` is `/etc/ufw/sysctl.conf` when `IPT_SYSCTL` names it (also through a path that cleans to it), the value as written when it names anything else, and `""` when unset; `Sysctl` holds `/etc/ufw/sysctl.conf`'s settings in order with keys through `SysctlKey`, and is empty when `IPT_SYSCTL` names another file, which is never read. Unsupported reasons, one each, in the order Read checks them: each line of `ufw.conf` and then `/etc/default/ufw` (split on `\n` only) that is not empty or only spaces and tabs, is not a comment (its first character after leading spaces and tabs is `#`), and does not match `^([A-Z_][A-Z0-9_]*)=([A-Za-z0-9_./:@%+,-]*|"[A-Za-z0-9_./:@%+, -]*")[ \t]*$` (`<file> line <n>: not a plain KEY=value line, which UFW's init script and its Python code may read differently`; the line's value is not read); `ENABLED` not yes/YES/no/NO; LOGLEVEL missing or not in `LogLevels`; `IPV6` and then `MANAGE_BUILTINS` not yes/YES/no/NO; `MANAGE_BUILTINS=yes`; each DEFAULT_INPUT/OUTPUT/FORWARD_POLICY missing or not exactly ACCEPT/DROP/REJECT; a missing `user.rules`, `before.rules` or `after.rules` (and the `6` files when IPv6 is on); a tuple line `ParseTuple` refuses (`<file> line <n>: <error>`); an executable `/etc/ufw/before.init` or `after.init`; `IPT_SYSCTL` naming anything but `/etc/ufw/sysctl.conf` (`/etc/default/ufw: IPT_SYSCTL names "<value>"; only /etc/ufw/sysctl.conf is supported`); a sysctl key with a glob; a key `FilteringSysctl` reports.
````

## A3 — Task 30
Reason: Protocol 0 ("0" or "ip") means every protocol in the kernel, and "icmp6" is ICMPv6; the walk treated both as non-matching and could skip a DROP rule
Cost if wrong: More rules match their packets, which is what the kernel does; no Unknown path turns into Accept
### Old
````text
var protoNumbers = map[string]int{
	"icmp": 1, "igmp": 2, "tcp": 6, "dccp": 33, "udp": 17, "ipv6": 41, "gre": 47,
	"esp": 50, "ah": 51, "ipv6-icmp": 58, "icmpv6": 58, "sctp": 132, "udplite": 136,
}

// protoNumber returns a protocol's number from its name as iptables-save or
// a caller spells it, or from its decimal number.
func protoNumber(name string) (int, bool) {
	name = strings.ToLower(name)
	if n, ok := protoNumbers[name]; ok {
		return n, true
	}
	n, err := strconv.Atoi(name)
	return n, err == nil && n >= 0 && n <= 255
}

func sameProto(a, b string) bool {
	na, oka := protoNumber(a)
	nb, okb := protoNumber(b)
	if oka && okb {
		return na == nb
	}
	return strings.EqualFold(a, b)
}

func protoMatch(m netfilter.NameMatch, proto string) bool {
	if m.Name == "" {
		return true
	}
	return (m.Name == "all" || sameProto(m.Name, proto)) != m.Neg
}
````
### New
````text
var protoNumbers = map[string]int{
	"ip": 0, "icmp": 1, "igmp": 2, "tcp": 6, "dccp": 33, "udp": 17, "ipv6": 41, "gre": 47,
	"esp": 50, "ah": 51, "ipv6-icmp": 58, "icmpv6": 58, "icmp6": 58, "sctp": 132, "udplite": 136,
}

// protoNumber returns a protocol's number from its name as iptables-save or
// a caller spells it, or from its decimal number.
func protoNumber(name string) (int, bool) {
	name = strings.ToLower(name)
	if n, ok := protoNumbers[name]; ok {
		return n, true
	}
	n, err := strconv.Atoi(name)
	return n, err == nil && n >= 0 && n <= 255
}

func sameProto(a, b string) bool {
	na, oka := protoNumber(a)
	nb, okb := protoNumber(b)
	if oka && okb {
		return na == nb
	}
	return strings.EqualFold(a, b)
}

// protoMatch applies -p. The kernel matches every protocol for protocol 0,
// which a restore file may spell "0" or "ip" and the parser keeps as given.
func protoMatch(m netfilter.NameMatch, proto string) bool {
	if m.Name == "" {
		return true
	}
	n, ok := protoNumber(m.Name)
	return (m.Name == "all" || ok && n == 0 || sameProto(m.Name, proto)) != m.Neg
}
````

## A4 — Task 105
Reason: Spec 2.3 flow 6 covers tailscaled's egress to any destination, so it must include IPv6; the IPv4-only stand-ins leave IPv6-only hosts unguarded.
Cost if wrong: a dual-stack change that cuts only tailscaled's IPv6 egress gets an overridable guard failure.
### Old
````text
- [ ] **Step 4: Run the tests and checks**
````
### New
````text
Then give tailscaled's flows both address families: spec 2.3 flow 6 is its egress to any destination, and `standIn` drops a family without a route. In `internal/modules/firewall/guard/flows.go`, replace this block (the one this step just wrote):

```go
	}
	slices.Sort(ports)
	for _, port := range ports {
		p := newPacket("output", netip.IPv4Unspecified(), port, internet4, 41641, "udp")
		p.Mark = tailscaleMark
		s.add(flow{label: "tailscale", pkt: p, standIn: true})
	}
	p := newPacket("output", netip.IPv4Unspecified(), unknownPort, internet4, 443, "tcp")
	p.Mark = tailscaleMark
	s.add(flow{label: "tailscale", pkt: p, standIn: true})
}
```

with:

```go
	}
	slices.Sort(ports)
	for _, src := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
		dst := internetFor(src)
		for _, port := range ports {
			p := newPacket("output", src, port, dst, 41641, "udp")
			p.Mark = tailscaleMark
			s.add(flow{label: "tailscale", pkt: p, standIn: true})
		}
		p := newPacket("output", src, unknownPort, dst, 443, "tcp")
		p.Mark = tailscaleMark
		s.add(flow{label: "tailscale", pkt: p, standIn: true})
	}
}
```

In `internal/modules/firewall/guard/flows_test.go`, replace this block:

```go
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 41641, "198.51.100.1", 41641, "udp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 0, "198.51.100.1", 443, "tcp"), 0x80000), standIn: true},
			},
```

with:

```go
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 41641, "198.51.100.1", 41641, "udp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 0, "198.51.100.1", 443, "tcp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "::", 41641, "2001:db8:ffff::1", 41641, "udp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "::", 0, "2001:db8:ffff::1", 443, "tcp"), 0x80000), standIn: true},
			},
```

If a test in `guard_test.go` or `route_test.go` lists the exact route lookups, probes or guard message of a run that includes tailscaled, add the IPv6 counterpart right after each IPv4 tailscale entry: source `::`, destination `2001:db8:ffff::1`, mark `0x80000`, the same ports. Change nothing else.

- [ ] **Step 4: Run the tests and checks**
````

## A5 — Header
Reason: The recorded golden-tree hash does not match the 5d5a310 drafted tree or a fresh capture; both hash to f43bf060…
Cost if wrong: Later golden-value tests would expose a different authoritative tree.
### Old
````text
- Golden captures were produced on 2026-10-24 by Task 33's command: ubuntu:22.04 ufw 0.36.1-4ubuntu0.1 / iptables v1.8.7, ubuntu:24.04 0.36.2-6 / v1.8.10, debian:12 0.36.2-1 / v1.8.9, debian:13 0.36.2-9 / v1.8.11, all nf_tables (matching `research.md`). 736 files; `find internal/modules/firewall/testdata/golden -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` printed `206dc9fb3e90733a43cde115b0f06cd40d62dcc41a5f8d06c04cd3ca8f28baf9`. A second run for `ubuntu:24.04`, and a run of the committed script for `debian:12`, were byte-identical (`diff -r`). A later run may differ if an image's ufw or iptables package moves; the tests decide.
````
### New
````text
- Golden captures were produced on 2026-10-24 by Task 33's command: ubuntu:22.04 ufw 0.36.1-4ubuntu0.1 / iptables v1.8.7, ubuntu:24.04 0.36.2-6 / v1.8.10, debian:12 0.36.2-1 / v1.8.9, debian:13 0.36.2-9 / v1.8.11, all nf_tables (matching `research.md`). 736 files; `find internal/modules/firewall/testdata/golden -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` printed `f43bf0607cf5019a9e41b73b5db0515de863c3ced469861b164d29faf7e338be`. A second run for `ubuntu:24.04`, and a run of the committed script for `debian:12`, were byte-identical (`diff -r`). A later run may differ if an image's ufw or iptables package moves; the tests decide.
````

## A6 — Task 33
Reason: The expected hash must match the 5d5a310 tree that the fallback restores, otherwise the step can never pass.
Cost if wrong: Later golden-value tests would expose a different authoritative tree.
### Old
````text
Expected: `shellcheck` prints nothing; each image prints `captured <slug>/<scenario>` for `fresh enabled rules disabled ipv6no logging manual sysctl` (32 lines in all); `docker ps` prints only its header; `find` prints `736`. Then compare with the drafting run: `find internal/modules/firewall/testdata/golden -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` must print `206dc9fb3e90733a43cde115b0f06cd40d62dcc41a5f8d06c04cd3ca8f28baf9` (2026-10-24). Later tasks pin values taken from that capture, so a different hash is not acceptable. When it differs (an image's ufw or iptables package has moved), save `git status --short internal/modules/firewall/testdata/golden | head -50` for the report, then replace the tree with the drafted capture: `rm -rf internal/modules/firewall/testdata/golden && git fetch -q /root/repositories/darkpanel/.dforge/drafts/v1-w1-firewall/parts/F2.bundle f2-rework && git checkout 5d5a310 -- internal/modules/firewall/testdata/golden`, run the hash command again (it must print the hash above) and report the saved difference to the controller. If Docker or the network is unavailable, use the same checkout instead of the script and report that.
````
### New
````text
Expected: `shellcheck` prints nothing; each image prints `captured <slug>/<scenario>` for `fresh enabled rules disabled ipv6no logging manual sysctl` (32 lines in all); `docker ps` prints only its header; `find` prints `736`. Then compare with the drafting run: `find internal/modules/firewall/testdata/golden -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` must print `f43bf0607cf5019a9e41b73b5db0515de863c3ced469861b164d29faf7e338be` (2026-10-24). Later tasks pin values taken from that capture, so a different hash is not acceptable. When it differs (an image's ufw or iptables package has moved), save `git status --short internal/modules/firewall/testdata/golden | head -50` for the report, then replace the tree with the drafted capture: `rm -rf internal/modules/firewall/testdata/golden && git fetch -q /root/repositories/darkpanel/.dforge/drafts/v1-w1-firewall/parts/F2.bundle f2-rework && git checkout 5d5a310 -- internal/modules/firewall/testdata/golden`, run the hash command again (it must print the hash above) and report the saved difference to the controller. If Docker or the network is unavailable, use the same checkout instead of the script and report that.
````

## A7 — Task 40
Reason: the legacy-variant host with nft ip6tables must not run ip6tables-nft-save while nf_tables is absent (Part C host safety)
Cost if wrong: a rerun of Task 40 restores a read that makes the kernel load nf_tables
### Old
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
		}
````
### New
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
			haveNFT := r.NFTablesLoaded
			if haveNFT == nil {
				haveNFT = func() bool {
					_, statErr := os.Stat("/sys/module/nf_tables")
					return statErr == nil
				}
			}
			// Under the legacy variant, ip6tables-nft-save would make the kernel
			// load nf_tables; the host is unsupported already, so V6 stays nil.
			if v == variantNFT && !haveNFT() {
				continue
			}
		}
````

## A8 — Task 56
Reason: Task 56 replaces read.go wholesale and must keep the nf_tables gate on the IPv6 nft save under the legacy variant
Cost if wrong: Task 56 silently removes Task 40's fix and Darkpanel loads nf_tables on mixed hosts
### Old
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
		}
````
### New
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
			haveNFT := nfTablesLoaded
			if r.NFTablesLoaded != nil {
				haveNFT = r.NFTablesLoaded
			}
			// Under the legacy variant, ip6tables-nft-save would make the kernel
			// load nf_tables; the host is unsupported already, so V6 stays nil.
			if v == variantNFT && !haveNFT() {
				continue
			}
		}
````

## A9 — Task 60
Reason: Task 60 replaces read.go wholesale and must keep the nf_tables gate on the IPv6 nft save under the legacy variant
Cost if wrong: Task 60 silently removes the fix and Darkpanel loads nf_tables on mixed hosts
### Old
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
		}
````
### New
````text
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
			haveNFT := nfTablesLoaded
			if r.NFTablesLoaded != nil {
				haveNFT = r.NFTablesLoaded
			}
			// Under the legacy variant, ip6tables-nft-save would make the kernel
			// load nf_tables; the host is unsupported already, so V6 stays nil.
			if v == variantNFT && !haveNFT() {
				continue
			}
		}
````

## A10 — Task 44
Reason: the tailnet shortcut fails open for carrier-grade NAT sources and for IPv6, where tailscaled installs no drop rule; it now requires the route back to leave through tailscale0
Cost if wrong: a real tailnet session whose route back is not through tailscale0 is checked on every interface, giving an overridable spurious refusal
### Old
````text
Two controller rulings on eligible in-interfaces. First, a flow from a Tailscale address (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) is checked only on `tailscale0` while that interface is up, because tailscaled's `ts-input` drops such sources on every other interface and checking them there would refuse every change while a tailnet SSH session is open. Second, an interface whose `rp_filter` key vanished between listing the interfaces and reading the key is left out of the facts instead of failing the check; any other read error, and a missing `all` key, stay errors. The tests prove both rules, the fallback to the normal rule without an up `tailscale0`, that other sources are unaffected, and that other read errors still fail.
````
### New
````text
Two controller rulings on eligible in-interfaces. First, a flow from a Tailscale address (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) is checked only on `tailscale0` while that interface is up and the route back to the source (`RouteGet(netstat.RouteQuery{Dst: src})`) leaves through `tailscale0`; checking a real tailnet session on every other interface would refuse every change while a tailnet SSH session is open, but tailscaled drops these sources elsewhere only for IPv4 and only with its netfilter rules installed, and `100.64.0.0/10` is also carrier-grade NAT space, so a source routed back through another interface, or whose lookup fails, keeps the normal rule. Second, an interface whose `rp_filter` key vanished between listing the interfaces and reading the key is left out of the facts instead of failing the check; any other read error, and a missing `all` key, stay errors. The tests prove both rules, the fallback to the normal rule without an up `tailscale0`, for a tailnet-range source routed back through `eth0` in both families and when the route-back lookup fails, that other sources are unaffected, and that other read errors still fail.
````

## A11 — Task 44
Reason: the test must prove the shortcut needs a route back through tailscale0 and that carrier-grade NAT and failed-lookup sources keep the normal rule
Cost if wrong: the fail-open regression returns unnoticed
### Old
````text
	tsSSH4 := flPkt("input", "100.101.1.2", 50000, "100.101.1.1", 22, "tcp")
````
### New
````text
	tsPeer4 := flPkt("input", "100.101.1.3", 50000, "100.101.1.1", 22, "tcp")
	cgnat4 := flPkt("input", "100.64.7.7", 50002, "192.0.2.1", 22, "tcp")
	cgnat6 := flPkt("input", "fd7a:115c:a1e0::9", 50003, "2001:db8::1", 22, "tcp")
	noRoute6 := flPkt("input", "fd7a:115c:a1e0::a", 50004, "2001:db8::1", 22, "tcp")
	tsSSH4 := flPkt("input", "100.101.1.2", 50000, "100.101.1.1", 22, "tcp")
````

## A12 — Task 44
Reason: the IPv4 shortcut case uses a peer routed back through tailscale0, and three cases cover sources that must keep the normal rule
Cost if wrong: the fail-open regression returns unnoticed
### Old
````text
		{"an IPv4 tailnet source only on tailscale0", tsSSH4, withTS(true), []string{"tailscale0"}},
		{"an IPv6 tailnet source only on tailscale0", tsSSH6, withTS(true), []string{"tailscale0"}},
````
### New
````text
		{"an IPv4 tailnet source only on tailscale0", tsPeer4, withTS(true), []string{"tailscale0"}},
		{"an IPv6 tailnet source only on tailscale0", tsSSH6, withTS(true), []string{"tailscale0"}},
		{"an IPv4 tailnet-range source routed back through eth0 keeps the normal rule", cgnat4, withTS(true), []string{"eth0"}},
		{"an IPv6 tailnet-range source routed back through eth0 keeps the normal rule", cgnat6, withTS(true), []string{"eth0", "eth1", "tailscale0"}},
		{"a failed route-back lookup keeps the normal rule", noRoute6, withTS(true), []string{"eth0", "eth1", "tailscale0"}},
````

## A13 — Task 44
Reason: route keys for the new cases, aligned with gofmt's existing value column
Cost if wrong: the new cases go unknown and fail
### Old
````text
				rtQuery("203.0.113.7", "192.0.2.1", ""):   rtVia("eth0", ""),
			}}
````
### New
````text
				rtQuery("203.0.113.7", "192.0.2.1", ""):   rtVia("eth0", ""),
				rtQuery("100.101.1.3", "", ""):            rtVia("tailscale0", ""),
				rtQuery("100.101.1.3", "100.101.1.1", ""): rtVia("eth0", ""),
				rtQuery("fd7a:115c:a1e0::5", "", ""):      rtVia("tailscale0", ""),
				rtQuery("100.64.7.7", "", ""):             rtVia("eth0", ""),
				rtQuery("100.64.7.7", "192.0.2.1", ""):    rtVia("eth0", ""),
				rtQuery("fd7a:115c:a1e0::9", "", ""):      rtVia("eth0", ""),
			}}
````

## A14 — Task 44
Reason: the constant's comment states a premise that is false for IPv6 and for some tailscaled netfilter modes
Cost if wrong: comment only
### Old
````text
// tailnetIface is tailscaled's interface, and tailnetRanges are the
// address ranges it assigns. tailscaled's ts-input chain drops a packet from
// these ranges on every other interface, so such a source can reach this
// host only through tailnetIface.
````
### New
````text
// tailnetIface is tailscaled's interface, and tailnetRanges are the
// address ranges it assigns. tailscaled drops these sources on other
// interfaces only for IPv4 and only when its netfilter rules are installed,
// and 100.64.0.0/10 is also carrier-grade NAT space, so fromTailnet also
// requires the route back to the source to leave through tailnetIface.
````

## A15 — Task 44
Reason: the eligible doc comment must state the route-back condition
Cost if wrong: comment only
### Old
````text
// source leaves through it. A source in a tailnet range arrives only on
// tailscale0 while that interface is up.
````
### New
````text
// source leaves through it. A source in a tailnet range arrives only on
// tailscale0 while that interface is up and the route back to the source
// leaves through it.
````

## A16 — Task 44
Reason: fromTailnet takes the shortcut only when the route back to the source leaves through tailscale0
Cost if wrong: an overridable spurious refusal for a tailnet peer routed elsewhere
### Old
````text
// fromTailnet reports whether src is a tailnet address and tailscale0 is up.
func (r *router) fromTailnet(src netip.Addr) bool {
	in := false
	for _, p := range tailnetRanges {
		in = in || p.Contains(src.Unmap())
	}
	if !in {
		return false
	}
	for _, i := range r.f.ifaces {
		if i.Name == tailnetIface && i.Up {
			return true
		}
	}
	return false
}
````
### New
````text
// fromTailnet reports whether src is a tailnet address, tailscale0 is up
// and the route back to src leaves through tailscale0, so a working session
// from src is one tailscaled carries. A failed lookup is not a tailnet
// source.
func (r *router) fromTailnet(src netip.Addr) bool {
	src = src.Unmap()
	in := false
	for _, p := range tailnetRanges {
		in = in || p.Contains(src)
	}
	if !in {
		return false
	}
	up := false
	for _, i := range r.f.ifaces {
		up = up || (i.Name == tailnetIface && i.Up)
	}
	if !up {
		return false
	}
	rt, err := r.host.RouteGet(netstat.RouteQuery{Dst: src})
	return err == nil && rt.OutIface == tailnetIface
}
````

## A17 — Header
Reason: the Part contract for fromTailnet gains the route-back condition
Cost if wrong: a later task's reviewer reads the old contract and flags the fix
### Old
````text
func (r *router) fromTailnet(src netip.Addr) bool // src (unmapped) is in tailnetRanges and an interface named tailscale0 in r.f.ifaces is Up
````
### New
````text
func (r *router) fromTailnet(src netip.Addr) bool // src (unmapped) is in tailnetRanges, an interface named tailscale0 in r.f.ifaces is Up, and r.host.RouteGet(netstat.RouteQuery{Dst: src}) succeeds with OutIface tailscale0
````

## A18 — Header
Reason: the Part contract bullet for tailnet eligibility gains the route-back condition
Cost if wrong: the bullet contradicts the amended fromTailnet contract and a later reviewer flags the fix
### Old
````text
- A protected flow whose source lies in Tailscale's ranges (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) is eligible only on `tailscale0` while that interface exists and is up; without an up `tailscale0` the normal eligibility rule applies (controller ruling, Task 44).
````
### New
````text
- A protected flow whose source lies in Tailscale's ranges (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) is eligible only on `tailscale0` while that interface exists and is up and the route back to the source (a `RouteGet` with the source as destination) leaves through `tailscale0`; otherwise, including when that lookup fails, the normal eligibility rule applies (controller ruling, Task 44, narrowed by ruling pc-44a).
````

## A19 — Header
Reason: the controller ruling's premise is false for IPv6 and for tailscaled without netfilter rules
Cost if wrong: a later reader relies on the stated premise and drops the route-back condition, which lets a carrier-grade NAT source on eth0 slip past the guard
### Old
````text
- Controller ruling (2026-10-24, decision 1; the controller adds it to spec 2.3): a protected flow whose source is in Tailscale's ranges, `100.64.0.0/10` for IPv4 and `fd7a:115c:a1e0::/48` for IPv6, is eligible only on `tailscale0` when that interface exists and is up, because tailscaled drops such sources on every other interface by design and checking them there is a false refusal. Without `tailscale0` the normal eligibility rule applies; this part reads "exists and is up" so that a `tailscale0` that is down also falls back to the normal rule. Implemented in Task 44 (`TestProbesTailscaleSource`).
````
### New
````text
- Controller ruling (2026-10-24, decision 1; the controller adds it to spec 2.3), narrowed by ruling pc-44a: a protected flow whose source is in Tailscale's ranges, `100.64.0.0/10` for IPv4 and `fd7a:115c:a1e0::/48` for IPv6, is eligible only on `tailscale0` when that interface exists and is up and the route back to the source leaves through `tailscale0`. tailscaled's drop of such sources on other interfaces cannot be relied on: `addBase6` installs no `fd7a:` drop (Task 55 fixture `v1.102-v6.save`), `--netfilter-mode=off` or `nodivert` installs none, and `100.64.0.0/10` is also RFC 6598 carrier-grade NAT space. When the route back leaves through `tailscale0`, a session on another interface cannot carry replies, so checking only `tailscale0` is safe. Without an up `tailscale0`, or when the route-back lookup fails or leaves elsewhere, the normal eligibility rule applies. Implemented in Task 44 (`TestProbesTailscaleSource`).
````

## A20 — Task 52
Reason: an absorbed uncertain copy kept a known value the kernel may overwrite, so a later mark match could falsely accept
Cost if wrong: a fail-open survives for copies whose source bits come from an earlier packet
### Old
````text
Tracks the packet mark and the connection mark as a value plus the bits a source outside netfilter may have set: an incoming packet carries `Packet.Mark` exactly, a locally generated packet with mark 0 has every bit unknown (a socket option may set it), a new connection's mark is 0 and a later packet's is unknown. `-m mark` and `-m connmark` hold when they hold on zero for the unknown bits, miss when no unknown bit could change that, and are undecidable when they need an unknown bit to be set (spec 2.3's "a mark only a non-netfilter source could set"). `MARK --set-xmark` and `CONNMARK --set-xmark/--save-mark/--restore-mark` follow `xt_MARK`/`xt_connmark`; an undecidable rule that only copies marks makes the copied bits unknown instead of making the verdict unknown. A mark change before routing makes the walk ask the router again with the new mark. The test covers tailscaled's shapes: the subnet-route mark, the connmark save and restore, a socket's known mark and an unmarked local packet.
````
### New
````text
Tracks the packet mark and the connection mark as a value plus the bits a source outside netfilter may have set: an incoming packet carries `Packet.Mark` exactly, a locally generated packet with mark 0 has every bit unknown (a socket option may set it), a new connection's mark is 0 and a later packet's is unknown. `-m mark` and `-m connmark` hold when they hold on zero for the unknown bits, miss when no unknown bit could change that, and are undecidable when they need an unknown bit to be set (spec 2.3's "a mark only a non-netfilter source could set"). `MARK --set-xmark` and `CONNMARK --set-xmark/--save-mark/--restore-mark` follow `xt_MARK`/`xt_connmark`; an undecidable rule that only copies marks makes the copied bits unknown instead of making the verdict unknown, but only when running it leaves the known value of the mark it writes unchanged; a copy that could change a known bit makes the verdict unknown. In the raw table, which runs before conntrack, `-m connmark` is undecidable and a `CONNMARK` target makes the verdict unknown; on an `INVALID` or `UNTRACKED` packet, which has no connection, `-m connmark` misses, negated or not, and `CONNMARK` does nothing. A mark change before routing makes the walk ask the router again with the new mark. The test covers tailscaled's shapes: the subnet-route mark, the connmark save and restore, a socket's known mark and an unmarked local packet, plus the uncertain copies, raw-table and connectionless cases above.
````

## A21 — Task 52
Reason: markTarget and uncertainCopy must share one copy of the save/restore arithmetic
Cost if wrong: none beyond style; behaviour is identical
### Old
````text
	case sp.save:
		w.conn = markState{
			val: w.conn.val&^sp.ctmask ^ w.mark.val&sp.nfmask,
			ext: w.conn.ext&^sp.ctmask | w.mark.ext&sp.nfmask,
		}
	default:
		w.mark = markState{
			val: w.mark.val&^sp.nfmask ^ w.conn.val&sp.ctmask,
			ext: w.mark.ext&^sp.nfmask | w.conn.ext&sp.ctmask,
		}
	}
````
### New
````text
	case sp.save:
		w.conn = w.saved(sp)
	default:
		w.mark = w.restored(sp)
	}
````

## A22 — Task 52
Reason: absorb an uncertain copy only when both outcomes agree on the known value
Cost if wrong: a fail-open survives for copies whose source bits come from an earlier packet
### Old
````text
// uncertainCopy handles a rule whose match the walk cannot decide and
// whose target only copies one mark into the other, and reports whether it
// did. Whether such a rule runs decides nothing by itself: the walk marks
// the bits it may copy as unknown, so any later match that reads them is
// undecidable in turn.
func (w *walker) uncertainCopy(tg netfilter.Target) bool {
	if tg.Name != "CONNMARK" || tg.Chain {
		return false
	}
	sp, ok := parseMarkTarget(tg)
	switch {
	case !ok || sp.set:
		return false
	case sp.save:
		w.conn.ext |= sp.ctmask
	default:
		w.mark.ext |= sp.nfmask
	}
	return true
}
````
### New
````text
// uncertainCopy handles a rule whose match the walk cannot decide and
// whose target only copies one mark into the other, and reports whether it
// did. It absorbs the rule only when running it leaves the known value of
// the mark it writes unchanged; the walk then marks the bits the copy may
// bring in as unknown, so any later match that reads them is undecidable
// in turn. A copy that could change a known bit is left to decides, which
// makes the verdict unknown.
func (w *walker) uncertainCopy(tg netfilter.Target) bool {
	if tg.Name != "CONNMARK" || tg.Chain {
		return false
	}
	sp, ok := parseMarkTarget(tg)
	switch {
	case !ok || sp.set:
		return false
	case w.noConn():
		return true
	case sp.save:
		ran := w.saved(sp)
		if ran.val != w.conn.val {
			return false
		}
		w.conn.ext |= ran.ext
	default:
		ran := w.restored(sp)
		if ran.val != w.mark.val {
			return false
		}
		w.mark.ext |= ran.ext
	}
	return true
}

// saved is the connection mark after CONNMARK --save-mark.
func (w *walker) saved(sp markSpec) markState {
	return markState{
		val: w.conn.val&^sp.ctmask ^ w.mark.val&sp.nfmask,
		ext: w.conn.ext&^sp.ctmask | w.mark.ext&sp.nfmask,
	}
}

// restored is the packet mark after CONNMARK --restore-mark.
func (w *walker) restored(sp markSpec) markState {
	return markState{
		val: w.mark.val&^sp.nfmask ^ w.conn.val&sp.ctmask,
		ext: w.mark.ext&^sp.nfmask | w.conn.ext&sp.ctmask,
	}
}
````

## A23 — Task 52
Reason: no test exercised an uncertain copy that overwrites a known bit
Cost if wrong: the fail-open could return unnoticed
### Old
````text
	p = evalPacket()
	p.InIface = "wg0"
	evalCheck(t, Evaluate(evalRuleset(t, markIncoming), p, nil), fwstate.Unknown, nil,
		"the mark was changed to 0x100 and no router was given")
}
````
### New
````text
	p = evalPacket()
	p.InIface = "wg0"
	evalCheck(t, Evaluate(evalRuleset(t, markIncoming), p, nil), fwstate.Unknown, nil,
		"the mark was changed to 0x100 and no router was given")
}

// markUncertain puts a CONNMARK restore and save behind a match the walk
// cannot decide; each would overwrite a known bit if it ran.
const markUncertain = `*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -i wg0 -j MARK --set-xmark 0x100/0xff00
-A PREROUTING -i wg0 -m statistic --mode random --probability 0.5 -j CONNMARK --restore-mark --nfmask 0xff00 --ctmask 0xff00
-A PREROUTING -i wg1 -j CONNMARK --set-xmark 0x100/0xff00
-A PREROUTING -i wg1 -m statistic --mode random --probability 0.5 -j CONNMARK --save-mark --nfmask 0xff00 --ctmask 0xff00
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -m mark --mark 0x100/0xff00 -j ACCEPT
-A INPUT -m connmark --mark 0x100/0xff00 -j ACCEPT
COMMIT
`

func TestEvaluateMarkUncertainCopy(t *testing.T) {
	rs := evalRuleset(t, markUncertain)
	for iface, ref := range map[string]string{"wg0": "mangle/PREROUTING#2", "wg1": "mangle/PREROUTING#4"} {
		t.Run(iface, func(t *testing.T) {
			p := evalPacket()
			p.InIface = iface
			evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, nil, ref+": match statistic depends on rates or earlier packets")
		})
	}
}
````

## A24 — Task 52
Reason: connmark in the raw table runs before conntrack attaches a connection
Cost if wrong: an over-cautious Unknown on rulesets nobody generates
### Old
````text
	case "mark", "connmark":
		return w.markMatch(m)
````
### New
````text
	case "mark", "connmark":
		if m.Module == "connmark" && t.Name == "raw" {
			return maybe, "the raw table runs before connection tracking"
		}
		return w.markMatch(m)
````

## A25 — Task 52
Reason: CONNMARK in the raw table acts only when a looped-back packet already has a connection
Cost if wrong: an over-cautious Unknown on rulesets nobody generates
### Old
````text
	case "MARK", "CONNMARK":
		return w.markTarget(tg, ref)
````
### New
````text
	case "MARK", "CONNMARK":
		if tg.Name == "CONNMARK" && t.Name == "raw" {
			return w.stop(fwstate.Unknown, ref+": target CONNMARK in the raw table runs before connection tracking")
		}
		return w.markTarget(tg, ref)
````

## A26 — Task 52
Reason: no test covered connmark in the raw table
Cost if wrong: the fail-open could return unnoticed
### Old
````text
func TestEvaluateMarkReroutes(t *testing.T) {
````
### New
````text
const markRaw = `*raw
:PREROUTING ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
-A PREROUTING -i wg3 -m connmark --mark 0x0/0xff -j ACCEPT
-A PREROUTING -i wg3 -j DROP
-A PREROUTING -i wg4 -j CONNMARK --set-xmark 0x100/0xff00
COMMIT
*filter
:INPUT ACCEPT [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
COMMIT
`

func TestEvaluateMarkRaw(t *testing.T) {
	rs := evalRuleset(t, markRaw)
	p := evalPacket()
	p.InIface = "wg3"
	evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, nil, "raw/PREROUTING#1: the raw table runs before connection tracking")
	p.InIface = "wg4"
	evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, nil, "raw/PREROUTING#3: target CONNMARK in the raw table runs before connection tracking")
}

func TestEvaluateMarkReroutes(t *testing.T) {
````

## A27 — Task 52
Reason: INVALID and UNTRACKED packets have no connection, so the kernel's connmark match is false whether negated or not
Cost if wrong: a connmark ACCEPT on such packets is judged a miss (leans toward drop)
### Old
````text
// markMatch applies -m mark or -m connmark. It handles "!" itself, because
// negating an assumed value is not the same as negating a decided one.
func (w *walker) markMatch(m netfilter.Match) (tri, string) {
	st := w.mark
	if m.Module == "connmark" {
		st = w.conn
	}
````
### New
````text
// noConn reports whether the packet has no connection: the kernel gives
// INVALID and UNTRACKED packets none, so connmark never matches them,
// negated or not, and CONNMARK leaves them alone.
func (w *walker) noConn() bool {
	return w.pkt.CtState == "INVALID" || w.pkt.CtState == "UNTRACKED"
}

// markMatch applies -m mark or -m connmark. It handles "!" itself, because
// negating an assumed value is not the same as negating a decided one.
func (w *walker) markMatch(m netfilter.Match) (tri, string) {
	st := w.mark
	if m.Module == "connmark" {
		if w.noConn() {
			return no, ""
		}
		st = w.conn
	}
````

## A28 — Task 52
Reason: CONNMARK does nothing on a packet without a connection
Cost if wrong: a later connmark match reads a mark the kernel never set
### Old
````text
func (w *walker) markTarget(tg netfilter.Target, ref string) outcome {
	sp, ok := parseMarkTarget(tg)
	if !ok {
		return w.stop(fwstate.Unknown, ref+": target "+tg.Name+" with these options is not modelled")
	}
````
### New
````text
func (w *walker) markTarget(tg netfilter.Target, ref string) outcome {
	sp, ok := parseMarkTarget(tg)
	if !ok {
		return w.stop(fwstate.Unknown, ref+": target "+tg.Name+" with these options is not modelled")
	}
	if tg.Name == "CONNMARK" && w.noConn() {
		return next
	}
````

## A29 — Task 52
Reason: no test covered connmark on a packet without a connection
Cost if wrong: the fail-open could return unnoticed
### Old
````text
func TestEvaluateMarks(t *testing.T) {
````
### New
````text
// markUntracked accepts on the connection mark, negated or not; a packet
// without a connection matches neither, and the CONNMARK rule leaves it alone.
const markUntracked = `*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -j CONNMARK --set-xmark 0x7/0xff
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -m connmark --mark 0x0/0xff -j ACCEPT
-A INPUT -m connmark ! --mark 0x0/0xff -j ACCEPT
COMMIT
`

func TestEvaluateMarkWithoutConnection(t *testing.T) {
	rs := evalRuleset(t, markUntracked)
	for _, st := range []string{"UNTRACKED", "INVALID"} {
		t.Run(st, func(t *testing.T) {
			p := evalPacket()
			p.CtState = st
			evalCheck(t, Evaluate(rs, p, nil), fwstate.Drop, []string{"mangle/PREROUTING#1"}, "filter/INPUT policy DROP")
		})
	}
}

func TestEvaluateMarks(t *testing.T) {
````

## A30 — Task 56
Reason: Task 56's foreign reads add 5 iptables-nft-save calls and nft list tables to the pc-40a subtest in read_test.go, which no task owns.
Cost if wrong: one more count edit if a later task changes the read sequence again.
### Old
````text
- Test: `internal/modules/firewall/loaded/foreign_test.go`

**Interfaces:**
````
### New
````text
- Test: `internal/modules/firewall/loaded/foreign_test.go`
- Test: `internal/modules/firewall/loaded/read_test.go` (the `"nf_tables loaded"` subtest of `TestReadLegacyWithNFTIPv6` only)

In `read_test.go`, the `"nf_tables loaded"` subtest (added by Task 40 under ruling pc-40a) now also sees the foreign reads: five `iptables-nft-save -t` reads for the IPv4 family and one `nft list tables`. Change only that subtest. After the line

```go
		x.answers["/usr/sbin/iptables --version"] = ldResult{out: "iptables v1.8.9 (legacy)\n"}
```

add

```go
		x.answers["/usr/sbin/nft list tables"] = ldResult{}
```

and replace

```go
		if len(x.calls) != 2+len(netfilter.Tables()) {
```

with

```go
		if len(x.calls) != 2+2*len(netfilter.Tables())+1 {
```

Leave the subtest's other assertions as they are; it does not assert `Unsupported`.

**Interfaces:**
````

## A31 — Task 59
Reason: a ufw add that replaces a rule with the same specification was journaled as a pure creation, so its inverse loses the replaced rule.
Cost if wrong: adds that match an existing rule's specification are refused at apply time instead of being journaled as modifications.
### Old
````text
	if op.Kind == core.EffectCreate {
		if len(groups) > 0 {
			return fmt.Errorf("firewall: %s exists already", op.Resource)
		}
		_, id, _ := splitResource(op.Resource)
		e.Resource, e.Recovery = nounRule+":?"+id, &core.RecoveryID{Kind: "marker", Value: id}
		e.Before = core.StateDoc{State: json.RawMessage("null"), Version: before.Version}
	}
	res, runErr := runJournaled(ctx, env, d, e, op.Resource, func() (core.CmdResult, error) {
		return env.Exec.Run(ctx, core.Cmd{Path: ufwPath, Args: op.Args})
	})
	if runErr != nil || res.ExitCode != 0 {
		return ufwFailure(op.Args, res, runErr)
	}
	return nil
}
````
### New
````text
	if op.Kind == core.EffectCreate {
		if len(groups) > 0 {
			return fmt.Errorf("firewall: %s exists already", op.Resource)
		}
		_, id, _ := splitResource(op.Resource)
		// ufw replaces a rule that differs only in action, log type or
		// comment; journaled as a creation, its undo would lose that rule.
		if len(next.Rules4)-len(cfg.Rules4) != taggedTuples(next.Rules4, id) ||
			len(next.Rules6)-len(cfg.Rules6) != taggedTuples(next.Rules6, id) {
			return fmt.Errorf("firewall: ufw %s would replace an existing rule", strings.Join(op.Args, " "))
		}
		e.Resource, e.Recovery = nounRule+":?"+id, &core.RecoveryID{Kind: "marker", Value: id}
		e.Before = core.StateDoc{State: json.RawMessage("null"), Version: before.Version}
	}
	res, runErr := runJournaled(ctx, env, d, e, op.Resource, func() (core.CmdResult, error) {
		return env.Exec.Run(ctx, core.Cmd{Path: ufwPath, Args: op.Args})
	})
	if runErr != nil || res.ExitCode != 0 {
		return ufwFailure(op.Args, res, runErr)
	}
	return nil
}

// taggedTuples counts the tuples whose comment is a tag of id.
func taggedTuples(ts []model.Tuple, id string) int {
	n := 0
	for _, t := range ts {
		if got, _, ok := model.ParseTag(t.Comment); ok && got == id {
			n++
		}
	}
	return n
}
````

## A32 — Task 93
Reason: the platform reports a missing /proc/sys as fs.ErrNotExist, so loaded, observe and restore read "procfs unavailable" as every key gone; files_sysctl.go is not frozen (spec 1.12), and this sysctl task owns the fix
Cost if wrong: if Task 93 is skipped, a host without /proc/sys reads an empty sysctl state, and restores report success while writing nothing
### Old
````text
**Files:**
- Modify: `internal/modules/firewall/loaded/sysctl_read.go`
- Modify: `internal/modules/firewall/loaded/sysctl_predict.go`
- Test: `internal/modules/firewall/loaded/sysctl_read_test.go`
````
### New
````text
**Files:**
- Modify: `internal/modules/firewall/loaded/sysctl_read.go`
- Modify: `internal/modules/firewall/loaded/sysctl_predict.go`
- Test: `internal/modules/firewall/loaded/sysctl_read_test.go`
- Modify: `internal/platform/files_sysctl.go`
- Test: `internal/platform/files_sysctl_test.go`

Platform fix (ruling pc-60a; a separate commit made before this task's Step 1). `openProcSys` wraps its `openat2` error with `%w`, so on a host without `/proc/sys` every `ReadSysctl` and `WriteSysctl` matches `fs.ErrNotExist`, and every caller that skips a missing key reads "procfs unavailable" as "every key gone". Append to `internal/platform/files_sysctl_test.go`:

```go

// TestSysctlRootMissing: a missing sysctl root (no /proc mounted) is an
// error, never fs.ErrNotExist, so no caller reads it as every key gone.
func TestSysctlRootMissing(t *testing.T) {
	f, _ := fpFiles(t, FilesConfig{ProcSys: filepath.Join(t.TempDir(), "absent"), Owned: map[string][]core.OwnedPath{
		"sys": {{Path: sysForward, Kind: core.OwnedSysctl, Pattern: `^[01]$`}},
	}})
	ctx := context.Background()
	if _, err := f.ReadSysctl(ctx, "sys", sysForward); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadSysctl under a missing root = %v; want an error that is not fs.ErrNotExist", err)
	}
	if err := f.WriteSysctl(ctx, "sys", sysForward, "1"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("WriteSysctl under a missing root = %v; want an error that is not fs.ErrNotExist", err)
	}
}
```

Run `mkdir -p /root/.cache/w1-tmp/93 && TMPDIR=/root/.cache/w1-tmp/93 GOTOOLCHAIN=local timeout 300 go test -count=1 -run TestSysctlRootMissing ./internal/platform/` and expect FAIL, with both errors reported as matching fs.ErrNotExist. Then, in `openProcSys` in `internal/platform/files_sysctl.go`, replace the line `		return -1, fmt.Errorf("platform: open %s: %w", root, err)` with `		return -1, fmt.Errorf("platform: open %s: %v", root, err) //nolint:errorlint // a missing sysctl root must not read as a missing key (fs.ErrNotExist)`. Rerun the test and expect `ok`. Then run `TMPDIR=/root/.cache/w1-tmp/93 GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./internal/platform/...` and expect `0 issues.`. Commit with `git add internal/platform/files_sysctl.go internal/platform/files_sysctl_test.go` and `git commit -m "fix(platform): refuse sysctl reads without procfs"`. Leave the `gone` case of `TestReadSysctlErrors` as written below.
````

## A33 — Task 62
Reason: Part P (plan lines 782 and 788) and spec 2.3 require the IPV6=no refusal before every restore transition; revert, repair, uninstall and the emergency restore reach only `restore`.
Cost if wrong: a reverted change whose foreign IPv6 chains appeared after apply cannot be undone automatically and needs a manual repair.
### Old
````text
- [ ] **Step 4: Run the tests and checks**
````
### New
````text
Then apply the IPV6=no refusal (spec 2.3, Part P "Restore (part A1)"); it also consumes `predict.IPv6Refusal` (Task 61). The refusal is hard: no override and no connectivity exception bypasses it. In `internal/modules/firewall/restore.go`, replace this block:

```go
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)
```

with:

```go
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
)
```

and replace this block:

```go
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	var plan imagePlan
```

with:

```go
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	if snap.State.Image == nil {
		cfgS, err := d.readConfig(d.root)
		if err != nil {
			return rep, fmt.Errorf("firewall: restore: %w", err)
		}
		// An IPV6=no start replaces the whole IPv6 filter table, which no
		// snapshot can rebuild (spec 2.3); not even the connectivity
		// exception overrides this refusal.
		if err := predict.IPv6Refusal(cfgS, before, tr); err != nil {
			return rep, err
		}
	}
	var plan imagePlan
```

In `internal/modules/firewall/restore_test.go`, add `"github.com/darkraise/darkpanel/internal/core/errcode"` to the imports and append:

```go
func TestRestoreRefusesIPv6NoOverForeignRules(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.IPv6 = false
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	h.loaded.V6 = fhParse(t, rsTestSave4)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.calls = nil
	_, err := restore(ctx, h.env(), h.deps(), snap)
	var ec *errcode.Error
	if !errors.As(err, &ec) || ec.Code != errcode.FirewallUnsupportedConfiguration || !strings.Contains(ec.Detail, "chain fw-foreign") {
		t.Fatalf("restore = %v, want firewall.unsupported_configuration naming chain fw-foreign", err)
	}
	if got := rsTestRuns(h); got != nil {
		t.Errorf("runs = %v, want none", got)
	}
	if _, ok, _ := pendingRestore(ctx, h.db); !ok {
		t.Error("a refused restore cleared its record")
	}
}
```

- [ ] **Step 4: Run the tests and checks**
````

## A34 — Task 64
Reason: writeBackLive overwrites sysctl keys edited by hand in the pending window, against spec 2.5 step 3 and plan line 124
Cost if wrong: an extra config read and prediction per state inverse; a key changed by both ufw and the operator keeps the operator's value
### Old
````text
**Files:**
- Create: `internal/modules/firewall/revert.go`
- Test: `internal/modules/firewall/revert_test.go`
````
### New
````text
**Files:**
- Create: `internal/modules/firewall/revert.go`
- Test: `internal/modules/firewall/revert_test.go`
- Modify: `internal/modules/firewall/inverse.go`
- Test: `internal/modules/firewall/inverse_test.go`

Sysctl write-back fix (ruling cv-63b; a separate commit made before this task's Step 1). Spec 2.5 step 3 leaves other edits in place, but `writeBackLive` writes every snapshot sysctl value over the live one, so undoing an enable or disable overwrites a key changed by hand during the pending window. In `inverse`'s `nounState` case, before running the ufw vector: read `pre` = `d.readLoaded(ctx, env)`'s `Sysctl`; read `cfg` with `d.readConfig(d.root)`; compute `fwd, err := d.sysctl(cfg, snap.State.Sysctl, tr)` where `tr` is the `fwstate.Transition` the effect made (enable when its `Post` State is true, disable when false). Build `target`: for each key of `snap.State.Sysctl` present in `pre`, the snapshot value when `pre[key]` equals the snapshot value or `fwd[key]`, else `pre[key]` (a manual edit, kept). After the ufw vector, read the loaded state again and call `writeBackSysctl(ctx, env, target, live.Sysctl)`. Change `writeBackLive`'s signature to take `target` instead of `snap`. Any read, config or prediction error fails the inverse. Append one test to `inverse_test.go`: a forward enable whose UFW start sets `net/ipv4/ip_forward` to 1, then a manual write of `net/ipv4/conf/all/rp_filter` to a value differing from both the snapshot and the predicted value, then `inverse`; assert `net/ipv4/ip_forward` is back at the snapshot value and `net/ipv4/conf/all/rp_filter` keeps the manual value.
````

## A35 — Task 64
Reason: restore writes the snapshot's files (IPV6=no included) before the IPV6=no refusal runs, so a refused restore leaves IPV6=no on disk for the next reload or boot (spec 2.3)
Cost if wrong: one extra conf helper; behaviour of every non-refused restore is unchanged
### Old
````text
- Test: `internal/modules/firewall/inverse_test.go`
````
### New
````text
- Test: `internal/modules/firewall/inverse_test.go`
- Modify: `internal/modules/firewall/restore.go`
- Modify: `internal/modules/firewall/conf/conf.go`
- Test: `internal/modules/firewall/restore_test.go`

IPV6=no refusal before the first write (ruling pc-64c; a separate commit made after this task's commits). Spec 2.3 refuses a transition that starts UFW with IPV6=no over foreign IPv6 rules, but `restore` writes every snapshot file before it runs `predict.IPv6Refusal`, so a refused restore leaves `IPV6=no` on disk. Do not edit `restore`'s doc comment (Task 114 anchors on its last line). Append to `internal/modules/firewall/conf/conf.go`:

```go

// DefaultsIPv6 returns IPV6 of /etc/default/ufw's contents data as Read
// reads it: true only for yes or YES.
func DefaultsIPv6(data []byte) bool {
	c := fwstate.Config{Files: map[string]core.RestoreEntry{defaultsUFW: {Path: defaultsUFW, Present: true, Data: data}}}
	r := reader{c: &c}
	return r.yesNo(defaultsUFW, r.shell(defaultsUFW), "IPV6")
}
```

In `internal/modules/firewall/restore.go`, add `"github.com/darkraise/darkpanel/internal/modules/firewall/conf"` to the imports and replace this block:

```go
	rep.Transition = tr
	for _, e := range snap.Entries {
		if err := env.Files.Restore(ctx, ID, e); err != nil {
			return rep, fmt.Errorf("firewall: restore %s: %w", e.Path, err)
		}
	}
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	if snap.State.Image == nil {
		cfgS, err := d.readConfig(d.root)
		if err != nil {
			return rep, fmt.Errorf("firewall: restore: %w", err)
		}
		// An IPV6=no start replaces the whole IPv6 filter table, which no
		// snapshot can rebuild (spec 2.3); not even the connectivity
		// exception overrides this refusal.
		if err := predict.IPv6Refusal(cfgS, before, tr); err != nil {
			return rep, err
		}
	}
	var plan imagePlan
```

with:

```go
	rep.Transition = tr
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	if snap.State.Image == nil {
		cfgS, err := snapshotStart(snap)
		if err != nil {
			return rep, err
		}
		// An IPV6=no start replaces the whole IPv6 filter table, which no
		// snapshot can rebuild (spec 2.3); not even the connectivity
		// exception overrides this refusal. It is decided before the first
		// write, so a refused restore leaves no IPV6=no on disk for the next
		// reload or boot to start from.
		if err := predict.IPv6Refusal(cfgS, before, tr); err != nil {
			return rep, err
		}
	}
	for _, e := range snap.Entries {
		if err := env.Files.Restore(ctx, ID, e); err != nil {
			return rep, fmt.Errorf("firewall: restore %s: %w", e.Path, err)
		}
	}
	var plan imagePlan
```

and add, right after `restore`'s closing brace (before `func joinRefs`):

```go

// snapshotStart is what IPv6Refusal reads of the configuration snap
// restores, taken from its entries before anything is written.
func snapshotStart(snap snapshot) (fwstate.Config, error) {
	for _, e := range snap.Entries {
		if e.Path == "/etc/default/ufw" && e.Present {
			return fwstate.Config{Enabled: snap.State.Enabled, IPv6: conf.DefaultsIPv6(e.Data)}, nil
		}
	}
	return fwstate.Config{}, fmt.Errorf("firewall: restore: snapshot %s holds no /etc/default/ufw", snap.ID)
}
```

In `internal/modules/firewall/restore_test.go`, in `TestRestoreRefusesIPv6NoOverForeignRules`, right after the line `	snap := rsTestSnap(t, h, false)` add:

```go
	s.IPv6 = true
	h.setState(s)
```

and right after the `if !errors.As(err, &ec) ...` block (its closing brace) add:

```go
	if !h.config().IPv6 {
		t.Error("a refused restore wrote the snapshot's IPV6=no")
	}
```

Run `mkdir -p /root/.cache/w1-tmp/64 && TMPDIR=/root/.cache/w1-tmp/64 GOTOOLCHAIN=local timeout 300 go test -count=1 -run TestRestoreRefusesIPv6NoOverForeignRules ./internal/modules/firewall/` on the test change alone and expect FAIL with `a refused restore wrote the snapshot's IPV6=no`; after the code change expect `ok`. Then run, with the same TMPDIR and `GOTOOLCHAIN=local`: `timeout 1200 go test -count=1 ./internal/modules/firewall/...` (every line `ok`), `timeout 300 go vet ./...` (no output), `timeout 900 golangci-lint run --allow-serial-runners ./internal/modules/firewall/...` (`0 issues.`). Commit with `git add internal/modules/firewall/restore.go internal/modules/firewall/restore_test.go internal/modules/firewall/conf/conf.go` and `git commit -m "fix(firewall): refuse IPV6=no before writing files"`.
````

## A36 — Task 97
Reason: TestLockoutVia does not check that a nil guard context reaches check as a nil Plan, or that an error comes with no detail (Task 65 review Minors 2-3).
Cost if wrong: two extra assertions in a test; no production change, and Step 2's line numbers are unchanged.
### Old
````text
	if ok, detail, err := lockout(ctx, core.Env{}, st, nil); !ok || detail != answer.Message || err != nil {
		t.Errorf("lockout = %v, %q, %v; want true, %q, nil", ok, detail, err, answer.Message)
	}

	answerErr = errors.New("firewall: read the socket table: netstat: no socket tables under the proc root")
	if ok, _, err := lockout(ctx, core.Env{}, st, gc); ok || !errors.Is(err, answerErr) {
		t.Errorf("lockout = %v, %v; want false and the check's error", ok, err)
	}
````
### New
````text
	if ok, detail, err := lockout(ctx, core.Env{}, st, nil); !ok || detail != answer.Message || err != nil || len(got) != 2 || got[1].Plan != nil {
		t.Errorf("lockout = %v, %q, %v, inputs %+v; want true, %q, nil and a second input with no plan", ok, detail, err, got, answer.Message)
	}

	answerErr = errors.New("firewall: read the socket table: netstat: no socket tables under the proc root")
	if ok, detail, err := lockout(ctx, core.Env{}, st, gc); ok || detail != "" || !errors.Is(err, answerErr) {
		t.Errorf("lockout = %v, %q, %v; want false, no detail and the check's error", ok, detail, err)
	}
````

## A37 — Task 66
Reason: Set-based rule matching hides a change in the copy count of a manual rule, so a restore can add or remove one without manual: true (spec 2.5) and with no diff line (R39).
Cost if wrong: The unused linter flags ruleTexts until Task 80 uses it, or Merge collapses duplicates and the new test fails; either is caught at Step 4.
### Old
````text
	if got, want := ruleText(r), `rule new: deny in proto udp from 2001:db8::/32 to ::/0 port 5000:5100 comment "lab"`; got != want {
		t.Errorf("ruleText = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w1-tmp/66 && TMPDIR=/root/.cache/w1-tmp/66 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestStateDiff' ./internal/modules/firewall/`
Expected: FAIL — observed output:

```text
# github.com/darkraise/darkpanel/internal/modules/firewall [github.com/darkraise/darkpanel/internal/modules/firewall.test]
internal/modules/firewall/diff_test.go:19:80: undefined: logical
internal/modules/firewall/diff_test.go:22:9: undefined: logical
internal/modules/firewall/diff_test.go:33:12: undefined: stateDiff
internal/modules/firewall/diff_test.go:59:9: undefined: stateDiff
internal/modules/firewall/diff_test.go:67:31: undefined: logical
internal/modules/firewall/diff_test.go:93:12: undefined: stateDiff
internal/modules/firewall/diff_test.go:96:12: undefined: manualChanges
internal/modules/firewall/diff_test.go:99:12: undefined: manualChanges
internal/modules/firewall/diff_test.go:103:22: undefined: planRuleID
internal/modules/firewall/diff_test.go:105:18: undefined: ruleText
internal/modules/firewall/diff_test.go:105:18: too many errors
FAIL	github.com/darkraise/darkpanel/internal/modules/firewall [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/modules/firewall/diff.go`:

```go
package firewall

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// planRuleID is the id a new rule carries in a plan, which has no change
// yet; Apply gives the rule its change's id (a ULID).
const planRuleID = "00000000000000000000000000"

// logical is the logical state a diff compares: UFW's configured state and
// the supported sysctl values.
type logical struct {
	cfg    fwstate.Config
	sysctl map[string]string
}

// stateDiff lists every change from a to b (R39), each as a "-" line for
// the old value and a "+" line for the new one: UFW's state; every rule
// removed (in a's order) and added (in b's order), a manual rule marked,
// and a reordering of the rules both hold; each default policy; the
// logging level; IPV6; and each sysctl value. Equal states give no line.
func stateDiff(a, b logical) []core.DiffLine {
	var out []core.DiffLine
	pair := func(old, now string) {
		if old != now {
			out = append(out, core.DiffLine{Op: "-", Text: old}, core.DiffLine{Op: "+", Text: now})
		}
	}
	pair(enabledText(a.cfg.Enabled), enabledText(b.cfg.Enabled))

	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	inA, inB := ruleTexts(ra), ruleTexts(rb)
	var keptA, keptB []string
	for _, r := range ra {
		if t := ruleText(r); inB[t] {
			keptA = append(keptA, r.ID)
		} else {
			out = append(out, core.DiffLine{Op: "-", Text: t})
		}
	}
	for _, r := range rb {
		if t := ruleText(r); inA[t] {
			keptB = append(keptB, r.ID)
		} else {
			out = append(out, core.DiffLine{Op: "+", Text: t})
		}
	}
	if !slices.Equal(keptA, keptB) {
		out = append(out, core.DiffLine{Op: " ", Text: "rules reordered: " + strings.Join(keptB, ", ")})
	}

	pair("default incoming "+a.cfg.DefaultInput, "default incoming "+b.cfg.DefaultInput)
	pair("default outgoing "+a.cfg.DefaultOutput, "default outgoing "+b.cfg.DefaultOutput)
	pair("default routed "+a.cfg.DefaultForward, "default routed "+b.cfg.DefaultForward)
	pair("logging "+a.cfg.LogLevel, "logging "+b.cfg.LogLevel)
	pair("IPV6 "+yesNo(a.cfg.IPv6), "IPV6 "+yesNo(b.cfg.IPv6))

	keys := slices.Collect(maps.Keys(a.sysctl))
	for k := range b.sysctl {
		if _, ok := a.sysctl[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		va, inA := a.sysctl[k]
		vb, inB := b.sysctl[k]
		if inA && inB && va == vb {
			continue
		}
		if inA {
			out = append(out, core.DiffLine{Op: "-", Text: "sysctl " + k + "=" + va})
		}
		if inB {
			out = append(out, core.DiffLine{Op: "+", Text: "sysctl " + k + "=" + vb})
		}
	}
	return out
}

// manualChanges returns the ids of the manual rules a change from a to b
// removes or adds (spec 2.2: they need manual: true), in stateDiff's order.
func manualChanges(a, b logical) []string {
	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	inA, inB := ruleTexts(ra), ruleTexts(rb)
	var out []string
	for _, f := range []struct {
		rules []model.Rule
		other map[string]bool
	}{{ra, inB}, {rb, inA}} {
		for _, r := range f.rules {
			if r.Origin == model.OriginManual && !f.other[ruleText(r)] {
				out = append(out, r.ID)
			}
		}
	}
	return out
}

func ruleTexts(rules []model.Rule) map[string]bool {
	out := make(map[string]bool, len(rules))
	for _, r := range rules {
		out[ruleText(r)] = true
	}
	return out
}
````
### New
````text
	if got, want := ruleText(r), `rule new: deny in proto udp from 2001:db8::/32 to ::/0 port 5000:5100 comment "lab"`; got != want {
		t.Errorf("ruleText = %q, want %q", got, want)
	}
}

// TestStateDiffDuplicates: rules match one copy to one copy, so adding or
// removing a copy of a duplicated manual rule is a diff line and needs
// manual: true (spec 2.2, 2.5), and moving which copy filters first is a
// reordering; ruleTexts is a set and does not count copies.
func TestStateDiffDuplicates(t *testing.T) {
	h := newFakeHost(t)
	once := h.state()
	once.Rules4 = []string{fhRule22v4, fhRule3000v4}
	a := dgLogical(t, h, once, nil)
	twice := once
	twice.Rules4 = []string{fhRule22v4, fhRule3000v4, fhRule3000v4}
	b := dgLogical(t, h, twice, nil)
	ahead := once
	ahead.Rules4 = []string{fhRule3000v4, fhRule22v4, fhRule3000v4}
	c := dgLogical(t, h, ahead, nil)

	rb := model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	if n := len(rb); n != 3 {
		t.Fatalf("merged rules of b = %d, want 3", n)
	}
	manual := rb[2]
	if manual.Origin != model.OriginManual {
		t.Fatalf("rule 2 is %s", manual.Origin)
	}
	text := ruleText(manual)

	if got, want := stateDiff(b, a), []core.DiffLine{{Op: "-", Text: text}}; !reflect.DeepEqual(got, want) {
		t.Errorf("diff removing a copy\n%+v\nwant\n%+v", got, want)
	}
	if got, want := stateDiff(a, b), []core.DiffLine{{Op: "+", Text: text}}; !reflect.DeepEqual(got, want) {
		t.Errorf("diff adding a copy\n%+v\nwant\n%+v", got, want)
	}
	if got, want := manualChanges(b, a), []string{manual.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("manualChanges removing a copy = %v, want %v", got, want)
	}
	if got, want := manualChanges(a, b), []string{manual.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("manualChanges adding a copy = %v, want %v", got, want)
	}

	want := []core.DiffLine{
		{Op: "-", Text: text},
		{Op: " ", Text: "rules reordered: 01J00000000000000000000001, " + manual.ID},
	}
	if got := stateDiff(c, a); !reflect.DeepEqual(got, want) {
		t.Errorf("diff dropping the copy ahead\n%+v\nwant\n%+v", got, want)
	}
	if got, want := manualChanges(c, a), []string{manual.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("manualChanges dropping the copy ahead = %v, want %v", got, want)
	}
	if got := ruleTexts(rb); len(got) != 2 {
		t.Errorf("ruleTexts = %v, want 2 texts", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w1-tmp/66 && TMPDIR=/root/.cache/w1-tmp/66 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestStateDiff' ./internal/modules/firewall/`
Expected: FAIL — observed output:

```text
# github.com/darkraise/darkpanel/internal/modules/firewall [github.com/darkraise/darkpanel/internal/modules/firewall.test]
internal/modules/firewall/diff_test.go:19:80: undefined: logical
internal/modules/firewall/diff_test.go:22:9: undefined: logical
internal/modules/firewall/diff_test.go:33:12: undefined: stateDiff
internal/modules/firewall/diff_test.go:59:9: undefined: stateDiff
internal/modules/firewall/diff_test.go:67:31: undefined: logical
internal/modules/firewall/diff_test.go:93:12: undefined: stateDiff
internal/modules/firewall/diff_test.go:96:12: undefined: manualChanges
internal/modules/firewall/diff_test.go:99:12: undefined: manualChanges
internal/modules/firewall/diff_test.go:103:22: undefined: planRuleID
internal/modules/firewall/diff_test.go:105:18: undefined: ruleText
internal/modules/firewall/diff_test.go:105:18: too many errors
FAIL	github.com/darkraise/darkpanel/internal/modules/firewall [build failed]
FAIL
```

In a fix round on the committed code, the new test instead fails on its assertions: with the set-based matching, `stateDiff(b, a)` and `stateDiff(a, b)` give no line and `manualChanges(b, a)` and `manualChanges(a, b)` return nil.

- [ ] **Step 3: Write the implementation**

Create `internal/modules/firewall/diff.go`:

```go
package firewall

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// planRuleID is the id a new rule carries in a plan, which has no change
// yet; Apply gives the rule its change's id (a ULID).
const planRuleID = "00000000000000000000000000"

// logical is the logical state a diff compares: UFW's configured state and
// the supported sysctl values.
type logical struct {
	cfg    fwstate.Config
	sysctl map[string]string
}

// stateDiff lists every change from a to b (R39), each as a "-" line for
// the old value and a "+" line for the new one: UFW's state; every rule
// removed (in a's order) and added (in b's order), a manual rule marked,
// and a reordering of the rules both hold; each default policy; the
// logging level; IPV6; and each sysctl value. Equal states give no line.
func stateDiff(a, b logical) []core.DiffLine {
	var out []core.DiffLine
	pair := func(old, now string) {
		if old != now {
			out = append(out, core.DiffLine{Op: "-", Text: old}, core.DiffLine{Op: "+", Text: now})
		}
	}
	pair(enabledText(a.cfg.Enabled), enabledText(b.cfg.Enabled))

	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	keptA, removed := unmatched(ra, rb)
	keptB, added := unmatched(rb, ra)
	for _, r := range removed {
		out = append(out, core.DiffLine{Op: "-", Text: ruleText(r)})
	}
	for _, r := range added {
		out = append(out, core.DiffLine{Op: "+", Text: ruleText(r)})
	}
	if !slices.Equal(keptA, keptB) {
		out = append(out, core.DiffLine{Op: " ", Text: "rules reordered: " + strings.Join(keptB, ", ")})
	}

	pair("default incoming "+a.cfg.DefaultInput, "default incoming "+b.cfg.DefaultInput)
	pair("default outgoing "+a.cfg.DefaultOutput, "default outgoing "+b.cfg.DefaultOutput)
	pair("default routed "+a.cfg.DefaultForward, "default routed "+b.cfg.DefaultForward)
	pair("logging "+a.cfg.LogLevel, "logging "+b.cfg.LogLevel)
	pair("IPV6 "+yesNo(a.cfg.IPv6), "IPV6 "+yesNo(b.cfg.IPv6))

	keys := slices.Collect(maps.Keys(a.sysctl))
	for k := range b.sysctl {
		if _, ok := a.sysctl[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		va, inA := a.sysctl[k]
		vb, inB := b.sysctl[k]
		if inA && inB && va == vb {
			continue
		}
		if inA {
			out = append(out, core.DiffLine{Op: "-", Text: "sysctl " + k + "=" + va})
		}
		if inB {
			out = append(out, core.DiffLine{Op: "+", Text: "sysctl " + k + "=" + vb})
		}
	}
	return out
}

// manualChanges returns the ids of the manual rules a change from a to b
// removes or adds (spec 2.2: they need manual: true), in stateDiff's order.
// An id appears once for each copy of the rule removed or added.
func manualChanges(a, b logical) []string {
	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	_, removed := unmatched(ra, rb)
	_, added := unmatched(rb, ra)
	var out []string
	for _, r := range slices.Concat(removed, added) {
		if r.Origin == model.OriginManual {
			out = append(out, r.ID)
		}
	}
	return out
}

// unmatched splits rules into the ids of those other also holds and the
// rules it does not. Rules match by their whole text one copy to one copy:
// identical manual rules share an id, so only a count shows that a change
// adds or removes a copy, which changes which copy filters first.
func unmatched(rules, other []model.Rule) (kept []string, rest []model.Rule) {
	left := make(map[string]int, len(other))
	for _, r := range other {
		left[ruleText(r)]++
	}
	for _, r := range rules {
		if t := ruleText(r); left[t] > 0 {
			left[t]--
			kept = append(kept, r.ID)
		} else {
			rest = append(rest, r)
		}
	}
	return kept, rest
}

// ruleTexts is the set of the rules' texts; it does not count copies.
func ruleTexts(rules []model.Rule) map[string]bool {
	out := make(map[string]bool, len(rules))
	for _, r := range rules {
		out[ruleText(r)] = true
	}
	return out
}
````

## A38 — Task 77
Reason: The restore's own capture prunes by retention before the change row exists, so restoring the oldest retained snapshot deletes it and the apply fails with "no snapshot has the id"; pin the target for the capture (ruling pc-77a).
Cost if wrong: A crash between pin and unpin leaves the target pinned (one extra kept snapshot); without the fix the oldest listed snapshot cannot be restored.
### Old
````text
**Files:**
- Create: `internal/modules/firewall/actions_snapshot.go`
- Test: `internal/modules/firewall/actions_snapshot_test.go`

**Interfaces:**
- Consumes: `defineMutation` (Task 73); `mismatch` (Task 74); `yesNo` (Task 66); `getSnapshot`, `errSnapshotNotFound`, `capture` (part A1); test helpers `ruHost`, `ruApply` (Task 75), `acDiff` (Task 74).
````
### New
````text
**Files:**
- Create: `internal/modules/firewall/actions_snapshot.go`
- Test: `internal/modules/firewall/actions_snapshot_test.go`
- Modify: `internal/modules/firewall/handler.go` (the `mutation` struct and `mutation.Snapshot` only)

Retention fix (ruling pc-77a; a separate commit made after Step 5). The engine calls `Snapshot` before it writes the change row (`internal/core/lifecycle/apply.go`), and `saveSnapshot` prunes in the same transaction with the new, still unattached row counted among the newest `keepSnapshots`. So when the target is the oldest retained non-system snapshot, the restore's own capture deletes it, and `Apply`'s `build` fails with "no snapshot has the id". Do not use `firewall_restores` for this: `beginRestore` replaces any unfinished restore record, and startup recovery would then restore the target. Do not change `capture`'s signature, because later tasks call it verbatim.

First append to `internal/modules/firewall/actions_snapshot_test.go`:

```go

// TestSnapshotRestoreOldestRetained: the restore's own capture counts toward
// retention, so the oldest retained snapshot must survive its own restore.
func TestSnapshotRestoreOldestRetained(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	snCapture(t, h, f) // the host's first snapshot is the baseline, never pruned
	id := snCapture(t, h, f)
	for i := 1; i < keepSnapshots; i++ {
		snCapture(t, h, f)
	}
	if ids := stTestIDs(t, h.db); len(ids) != keepSnapshots+1 || !ids[id] {
		t.Fatalf("%d snapshots before the restore, want the baseline and %d retained including %s", len(ids), keepSnapshots, id)
	}
	s := h.state()
	s.Rules4, s.Input = s.Rules4[:1], "ACCEPT"
	h.setState(s)
	ruApply(t, env, f.snapshotRestore(), `{"id":"`+id+`","manual":true}`)
	got, err := getSnapshot(ctx, h.db, id)
	if err != nil || got.Pinned {
		t.Errorf("target after the restore: pinned %v, %v; want kept and unpinned", got.Pinned, err)
	}
	if h.state().Input != "DROP" {
		t.Errorf("state %+v, want the snapshot's", h.state())
	}
}
```

If the fake host's captures do not sort newest-first in capture order (`created_at DESC, id DESC`), advance the fake host's clock by one second before each `snCapture` so `id` is the oldest retained one. Run `mkdir -p /root/.cache/w1-tmp/77 && TMPDIR=/root/.cache/w1-tmp/77 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSnapshotRestoreOldestRetained' ./internal/modules/firewall/` and expect FAIL with `apply: ... no snapshot has the id`.

Then, in `internal/modules/firewall/handler.go`, replace

```go
	// apply; nil means none.
	finish func(ctx context.Context, env core.Env, in In, out *ChangeOutput) error
}
```

with

```go
	// apply; nil means none.
	finish func(ctx context.Context, env core.Env, in In, out *ChangeOutput) error
	// keep names a snapshot the apply reads; nil means none.
	keep func(in In) string
}
```

and replace

```go
// Snapshot captures the logical snapshot with the plan's guard context.
func (m mutation[In]) Snapshot(ctx context.Context, env core.Env, in In) ([]byte, error) {
	return capture(ctx, env, m.f.d, m.guarded(in).Guard, m.image)
}
```

with

```go
// Snapshot captures the logical snapshot with the plan's guard context. The
// engine calls it before it records the change, and the capture prunes by
// retention counting its own new row, so a snapshot the apply reads (keep)
// is pinned for the capture; otherwise restoring the oldest retained
// snapshot would delete it.
func (m mutation[In]) Snapshot(ctx context.Context, env core.Env, in In) ([]byte, error) {
	if m.keep != nil {
		id := m.keep(in)
		s, err := getSnapshot(ctx, env.DB, id)
		if err != nil {
			return nil, err
		}
		if !s.Pinned {
			if err := setPinned(ctx, env.DB, id, true); err != nil {
				return nil, err
			}
			defer func() {
				if err := setPinned(context.WithoutCancel(ctx), env.DB, id, false); err != nil {
					env.Log.Warn("firewall: cannot unpin the snapshot a change restores", "snapshot", id, "error", err)
				}
			}()
		}
	}
	return capture(ctx, env, m.f.d, m.guarded(in).Guard, m.image)
}
```

In `internal/modules/firewall/actions_snapshot.go`, after the line `		manual:  func(in SnapshotRestoreInput) bool { return in.Manual },` add the line `		keep:    func(in SnapshotRestoreInput) string { return in.ID },` (gofmt aligns it). Rerun the test and expect `ok`, then run every check in Step 4. Commit with `git add internal/modules/firewall/handler.go internal/modules/firewall/actions_snapshot.go internal/modules/firewall/actions_snapshot_test.go` and `git commit -m "fix(firewall): keep a snapshot through its restore"`.

**Interfaces:**
- Consumes: `defineMutation` (Task 73); `mismatch` (Task 74); `yesNo` (Task 66); `getSnapshot`, `errSnapshotNotFound`, `capture`, `setPinned` (part A1); test helpers `ruHost`, `ruApply` (Task 75), `acDiff` (Task 74), `stTestIDs` (Task 37).
````

## A39 — Task 114
Reason: No test covers an empty or malformed stored emergency record. finishRestore's fallback to a normal restore is untested.
Cost if wrong: One extra test. It passes both before and after Task 114's implementation, so Step 2's observed failure output does not change.
### Old
````text
			if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
				t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
			}
		})
	}
}
````
### New
````text
			if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
				t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
			}
		})
	}
}

func TestAxRevertFinishesAnUnreadableEmergencyRecord(t *testing.T) {
	for _, c := range []struct {
		name   string
		record json.RawMessage
	}{
		{"empty", json.RawMessage{}},
		{"malformed", json.RawMessage(`{"detail":`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h, snap := rvTestSetup(t)
			if err := beginRestoreRecord(ctx, h.db, snap.ID, restoreRecord{Transition: fwstate.TransReload, Emergency: c.record}, h.clock.Now()); err != nil {
				t.Fatal(err)
			}
			out, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
			if err != nil {
				t.Fatal(err)
			}
			if out.Override {
				t.Error("an unreadable emergency record finished as an override")
			}
			var got []rvTestEffect
			for _, o := range out.Effects {
				e := rvTestEffect{o.Seq, o.State, o.Reason, ""}
				if o.Current != nil {
					e.current = string(o.Current.State)
				}
				got = append(got, e)
			}
			if want := []rvTestEffect{{1, core.EffectUndone, "", ""}, {2, core.EffectUndone, "", ""}}; !reflect.DeepEqual(got, want) {
				t.Errorf("effects = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(fileHashes(h.config().Files), snap.Hashes) {
				t.Error("the state files are not the snapshot's")
			}
			if _, pending, _ := restoreIntent(ctx, h.db, snap.ID); pending {
				t.Error("the finished restore left its record")
			}
		})
	}
}
````

## A40 — Task 114
Reason: The Warn in finishRestore logs the outer `err` from `restore`, which is always nil there, instead of the JSON decode error.
Cost if wrong: Renaming one local variable. No behaviour changes apart from the content of the log line.
### Old
````text
	if rec.Emergency != nil {
		var er emergencyRecord
		if err := json.Unmarshal(rec.Emergency, &er); err == nil {
			pin(ctx, env, snap.ID)
			er.Detail.Vanished = rep.Vanished
			return core.RevertOutcome{Effects: er.outcomes(), Override: true, Detail: mustJSON(er.Detail)}, nil
		}
		env.Log.Warn("firewall: the emergency restore record cannot be read", "snapshot", snap.ID, "error", err)
	}
````
### New
````text
	if rec.Emergency != nil {
		var er emergencyRecord
		readErr := json.Unmarshal(rec.Emergency, &er)
		if readErr == nil {
			pin(ctx, env, snap.ID)
			er.Detail.Vanished = rep.Vanished
			return core.RevertOutcome{Effects: er.outcomes(), Override: true, Detail: mustJSON(er.Detail)}, nil
		}
		env.Log.Warn("firewall: the emergency restore record cannot be read", "snapshot", snap.ID, "error", readErr)
	}
````

## A41 — Task 112
Reason: the contract's "no flow otherwise" leaves a runtime listener unguarded, against spec 2.3 and Part G's fail-closed rule; say so in the task and say it supersedes the Part contracts text.
Cost if wrong: hosts with a non-spec dp-panel layout fail the guard (overridable) on every firewall change.
### Old
````text
With a controller on the host (its main process is known from the unit), every TCP socket it listens on is a protected flow, so a change made with no client connected, a break-glass change, a timer revert or a system plan still cannot close the panel, runtime or agent port; before, only the established sessions' peers were protected. Each listener is reached from an internet stand-in address of its family with source port 0 (an unspecified listener is resolved by routing, as the panel flows are); a loopback listener and a listener bound to an address of another host are skipped. The runtime listener sits on the panel bridge's gateway address, and the only rule that opens it is `allow in on dp-panel from <proxy>` (spec 8), so a stand-in internet source would be dropped on every host with an exposure; its flow therefore enters on `dp-panel` from the next address of the bridge's network, the proxy container's. The tests cover the listener kinds in `buildFlows` and a full `Check` that fails when the change drops the agent listener port and passes when it does not.
````
### New
````text
With a controller on the host (its main process is known from the unit), every TCP socket it listens on is a protected flow, so a change made with no client connected, a break-glass change, a timer revert or a system plan still cannot close the panel, runtime or agent port; before, only the established sessions' peers were protected. Each listener is reached from an internet stand-in address of its family with source port 0 (an unspecified listener is resolved by routing, as the panel flows are); a loopback listener and a listener bound to an address of another host are skipped. The runtime listener sits on the panel bridge's gateway address, and the only rule that opens it is `allow in on dp-panel from <proxy>` (spec 8), so a stand-in internet source would be dropped on every host with an exposure; its flow therefore enters on `dp-panel` from the next address of the bridge's network, the proxy container's. When that next address lies outside the bridge's network, the guard cannot place the flow, so it is one `unknown` flow labelled `controller listener` with the reason `the runtime listener <address:port> on dp-panel has no proxy address in its network` (spec 2.3, Part G: a flow the guard cannot place is `unknown`). This supersedes the Part contracts' "no flow otherwise" for Task 112 (ruling pc-112a). The tests cover the listener kinds in `buildFlows` and a full `Check` that fails when the change drops the agent listener port and passes when it does not.
````

## A42 — Task 112
Reason: the test case for a bridge address without a proxy address must expect the unknown flow.
Cost if wrong: the test keeps asserting the hole and no test catches it.
### Old
````text
				ifaces: append(gIfaces(), netstat.Interface{Index: 6, Name: "dp-panel", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/32")}})},
````
### New
````text
				ifaces: append(gIfaces(), netstat.Interface{Index: 6, Name: "dp-panel", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/32")}})},
			want: []flow{{label: "controller listener", unknown: "the runtime listener 172.30.0.1:9445 on dp-panel has no proxy address in its network"}},
````

## A43 — Task 112
Reason: with A2 the new subtest fails before the implementation too, so the expected RED output must list it.
Cost if wrong: the implementer sees a RED mismatch and stops for a ruling.
### Old
````text
    --- FAIL: TestBuildFlows/the_local_controller's_listening_sockets (0.00s)
````
### New
````text
    --- FAIL: TestBuildFlows/the_local_controller's_listening_sockets (0.00s)
    --- FAIL: TestBuildFlows/a_runtime_listener_on_a_bridge_address_without_a_proxy_address (0.00s)
````

## A44 — Task 112
Reason: the implementation emits the unknown flow instead of skipping the listener, and the doc comment says so.
Cost if wrong: same as A1.
### Old
````text
// address of the bridge's network, may reach it, so that flow enters on
// the bridge from the proxy's address.
````
### New
````text
// address of the bridge's network, may reach it, so that flow enters on
// the bridge from the proxy's address; with no such address the flow
// cannot be placed and is unknown.
````

## A45 — Task 112
Reason: the code change matching A1, A2 and A4.
Cost if wrong: same as A1.
### Old
````text
			if proxy.IsValid() {
				p := newPacket("input", proxy, unknownPort, l, so.Local.Port(), "tcp")
				p.InIface = panelBridge
				s.add(flow{label: "controller listener", pkt: p})
			}
			continue
````
### New
````text
			if !proxy.IsValid() {
				s.add(flow{label: "controller listener", unknown: "the runtime listener " +
					netip.AddrPortFrom(l, so.Local.Port()).String() + " on dp-panel has no proxy address in its network"})
				continue
			}
			p := newPacket("input", proxy, unknownPort, l, so.Local.Port(), "tcp")
			p.InIface = panelBridge
			s.add(flow{label: "controller listener", pkt: p})
			continue
````

## A46 — Header
Reason: The Part contract makes `activeNow` fall back to the untrusted `ENABLED` when the record or the loaded chains cannot be read, and `restoreOver` then records that fallback for every retry to reuse.
Cost if wrong: A restore over a cut-off restore fails while the loaded state is unreadable, which it already did one step later; the only behaviour lost is writing a record that should not be trusted.
### Old
````text
**Task 117 — the transition of a restore (`restore.go`).** `func activeNow(ctx context.Context, env core.Env, d deps, cur fwstate.Config) bool`: `cur.Enabled`, unless a restore record exists (it can only be another snapshot's, because `restoreOver` asks only when the snapshot has none) and the loaded state is readable; then `ufwRunning` of the loaded state. `restoreOver` passes it to `chooseTransition` in place of `cur.Enabled`. `pendingRestore` is now called outside tests.
````
### New
````text
**Task 117 — the transition of a restore (`restore.go`).** `func activeNow(ctx context.Context, env core.Env, d deps, cur fwstate.Config) (bool, error)`: `cur.Enabled`, unless a restore record exists (it can only be another snapshot's, because `restoreOver` asks only when the snapshot has none); then `ufwRunning` of the loaded state. A failure to read the record or the loaded state is returned as an error, never replaced by `cur.Enabled`, and `restoreOver` returns it before `beginRestoreRecord`, so no transition chosen without the facts is recorded for a retry to reuse; the other snapshot's record stays. `restoreOver` passes the result to `chooseTransition` in place of `cur.Enabled`. `pendingRestore` is now called outside tests.
````

## A47 — Task 117
Reason: The task's summary must state the fail-closed rule and the added test.
Cost if wrong: Summary text only.
### Old
````text
A restore that finds a record of another snapshot's cut-off restore replaces it, and with it the UFW transition the record held. It then chose its own from `ENABLED` in the files, which the cut-off restore may have half written: after a cut-off disable (`ENABLED=no` written, UFW still running) a restore of an active snapshot chose `ufw --force enable`, which loads nothing on a running UFW. Now, while a record of another snapshot exists, the restore asks the loaded chains whether UFW is running (`ufwRunning`) and chooses from that. The test covers a cut-off disable and a cut-off enable, a stopped UFW, and the unchanged choice when no record exists.
````
### New
````text
A restore that finds a record of another snapshot's cut-off restore replaces it, and with it the UFW transition the record held. It then chose its own from `ENABLED` in the files, which the cut-off restore may have half written: after a cut-off disable (`ENABLED=no` written, UFW still running) a restore of an active snapshot chose `ufw --force enable`, which loads nothing on a running UFW. Now, while a record of another snapshot exists, the restore asks the loaded chains whether UFW is running (`ufwRunning`) and chooses from that. When the record or the chains cannot be read, the restore fails before it writes its own record, so a retry chooses again from the facts instead of replaying a guess. The first test covers a cut-off disable and a cut-off enable, a stopped UFW, and the unchanged choice when no record exists; the second covers unreadable chains and the retry.
````

## A48 — Task 117
Reason: Step 1 now adds a second test.
Cost if wrong: Wording only.
### Old
````text
Add one test.
````
### New
````text
Add two tests.
````

## A49 — Task 117
Reason: No test covered a loaded-state read failure while another snapshot's record exists.
Cost if wrong: Without it, the persisted wrong transition could come back unnoticed.
### Old
````text
			if _, pending, _ := pendingRestore(ctx, h.db); pending {
				t.Error("the restore left a record")
			}
		})
	}
}
````
### New
````text
			if _, pending, _ := pendingRestore(ctx, h.db); pending {
				t.Error("the restore left a record")
			}
		})
	}
}

func TestAxRestoreOverUnreadableChains(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.Enabled = true
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	other := rsTestSnap(t, h, false)
	if err := beginRestore(ctx, h.db, other.ID, fwstate.TransDisable, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	s.Enabled = false
	h.setState(s)
	for _, args := range []string{"reload", "--force enable", "disable"} {
		h.ufw[args] = func(*fhState) int { return 0 }
	}
	h.loadErr = errors.New("loaded: iptables-nft-save exited 1")
	h.calls = nil
	if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil {
		t.Fatal("restore succeeded without the loaded chains")
	}
	if _, ok, _ := restoreRecordOf(ctx, h.db, snap.ID); ok {
		t.Fatal("a restore that could not read the loaded chains recorded its transition")
	}
	if got := rsTestRuns(h); got != nil {
		t.Errorf("ufw ran %v, want nothing", got)
	}
	h.loadErr = nil
	h.calls = nil
	if _, err := restore(ctx, h.env(), h.deps(), snap); err != nil {
		t.Fatal(err)
	}
	if got, want := rsTestRuns(h), []string{"run /usr/sbin/ufw reload"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the retry ran %v, want %v", got, want)
	}
}
````

## A50 — Task 117
Reason: Step 2 must also run the new test and expect it to fail.
Cost if wrong: The new test's RED would go unobserved.
### Old
````text
Run: `mkdir -p /root/.cache/w1-tmp/117 && TMPDIR=/root/.cache/w1-tmp/117 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAxRestoreAfterAnotherInterruptedRestore' ./internal/modules/firewall/`
Expected: FAIL. Observed output:
````
### New
````text
Run: `mkdir -p /root/.cache/w1-tmp/117 && TMPDIR=/root/.cache/w1-tmp/117 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAxRestore(AfterAnotherInterruptedRestore|OverUnreadableChains)$' ./internal/modules/firewall/`
Expected: FAIL. `TestAxRestoreOverUnreadableChains` also fails, with `a restore that could not read the loaded chains recorded its transition` (the drafting run predates this test, so its line is not in the output below). Observed output of the first test:
````

## A51 — Task 117
Reason: `activeNow` must return a read failure as an error instead of falling back to the untrusted `ENABLED`.
Cost if wrong: Same as A1.
### Old
````text
// activeNow reports whether UFW is active, for choosing the transition of a
// restore that has no record yet. The configuration says so, unless a
// record of another snapshot's restore is still there: that restore was cut
// off, its files may be half written while UFW still runs as before, and
// the new restore replaces its record, which held the transition. Then the
// loaded chains say.
func activeNow(ctx context.Context, env core.Env, d deps, cur fwstate.Config) bool {
	if _, other, err := pendingRestore(ctx, env.DB); err != nil || !other {
		return cur.Enabled
	}
	st, err := d.readLoaded(ctx, env)
	if err != nil {
		return cur.Enabled
	}
	return ufwRunning(st)
}
````
### New
````text
// activeNow reports whether UFW is active, for choosing the transition of a
// restore that has no record yet. The configuration says so, unless a
// record of another snapshot's restore is still there: that restore was cut
// off, its files may be half written while UFW still runs as before, and
// the new restore replaces its record, which held the transition. Then the
// loaded chains say. A record or chains it cannot read are an error, never
// the configuration's word, because the caller records the transition it
// chooses and a retry reuses it.
func activeNow(ctx context.Context, env core.Env, d deps, cur fwstate.Config) (bool, error) {
	_, other, err := pendingRestore(ctx, env.DB)
	if err != nil {
		return false, err
	}
	if !other {
		return cur.Enabled, nil
	}
	st, err := d.readLoaded(ctx, env)
	if err != nil {
		return false, fmt.Errorf("firewall: restore: %w", err)
	}
	return ufwRunning(st), nil
}
````

## A52 — Task 117
Reason: `restoreOver` must return `activeNow`'s error before `beginRestoreRecord` writes a record.
Cost if wrong: Same as A1.
### Old
````text
			tr = chooseTransition(snap.State.Enabled, activeNow(ctx, env, d, cur))
````
### New
````text
			active, err := activeNow(ctx, env, d, cur)
			if err != nil {
				return rep, err
			}
			tr = chooseTransition(snap.State.Enabled, active)
````

## A53 — Task 117
Reason: Step 4 must run the new test as well.
Cost if wrong: The targeted GREEN run would skip the new test; the full suite still runs it.
### Old
````text
Run: `TMPDIR=/root/.cache/w1-tmp/117 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAxRestoreAfterAnotherInterruptedRestore' ./internal/modules/firewall/` Expected: PASS.
````
### New
````text
Run: `TMPDIR=/root/.cache/w1-tmp/117 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAxRestore(AfterAnotherInterruptedRestore|OverUnreadableChains)$' ./internal/modules/firewall/` Expected: PASS.
````
