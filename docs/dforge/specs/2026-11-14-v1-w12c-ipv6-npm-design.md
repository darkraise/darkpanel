# v1 w12c: IPv6 visitors of NPM and the m3g flake

**Date:** 2026-11-14
**Parent spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md
**Rows:** v1-completion register (`docs/dforge/registers/2026-10-21-v1-completion.md`) rows 482 and 483; security register (`docs/dforge/registers/2026-11-06-v1-security-review.md`) row 152
**Execution strategy:** sequential — inline execution
**Depth:** full — risk

## Decisions

The owner ruled the three open rows on 2026-11-14, through the questions below.

- **Flow.** "Rule and implement them with fast mode … or the full dforge flow?" — "Full dforge flow (Recommended)".
- **Rows in `verify` and `doing`.** The 35 rows in `verify` wait for the M3 audit and need no ruling. Of the 20 rows the finished w12b plan left in `doing`, 19 move to `verify` at the w12b finish; security row 6 stays `doing` until harness run 43551309523 (gate 7 of w12b, acceptance A26) is green. This spec touches none of them.
- **Rows 483 and 152.** Options: patch each row; do less (docs and a warning, accept row 152); make darkpanel-npm dual-stack; patch 483 and accept 152. Answer: "Patch each row (Recommended)" — "#483: with the integration off, deploy-public adds a tagged allow-in 80,443/tcp rule (previewed, guarded, auto-reverted) and the docs say why. #152: the guard gains an output flow from the darkpanel-npm gateway to NPM's container on 80/443."
- **Row 482.** Answer: "Root-cause it in this plan (Recommended)" — "One plan task investigates with systematic debugging (likely a cold first load or a fixture race) and fixes the cause; timeouts are raised only if the cause is a slow load the test should wait for."
- **Order.** Answer: "Merge w12b, this plan, then audit (Recommended)" — merge w12b into main once harness run 43551309523 is green, branch this plan from main, then rerun the M3 audit.
- **State boards.** Answer: "No boards (Recommended)" — "Touch depth: change the copy, then prove the form at 375, 768 and 1440 px, light and dark, with both integration states, as the w12b layout proof did."
- **UI/UX depth:** touch — the copy of the NPM deploy form, its second read and the new input fields. Per dforge:ui-ux the delta lives in the proof report; its items are the `UI-` and `OP-` statements below.
- **Approach.** Considered and not chosen: IPv6 on darkpanel-npm (it needs the daemon's ip6tables, recreates the network on existing installs and reworks the hidden-client design w12b shipped; a post-v1 candidate), and the do-less option (a warning leaves IPv6 visitors refused by default on the documented integration-off path, and row 152 stays a medium finding the guard does not cover).
- **A new action version.** `docs/versioning.md:55-61` forbids changing what an action does within a version, so the integration-off ports step ships as `npm.instance.deploy-public` v2; v1 keeps today's behaviour until the next major release. This follows from the project's policy, not from an owner question. It is the repository's first second version of an action.
- **Row 152 source of facts.** The guard learns the userland proxy's target from the live process that holds the IPv6 listener, not from the exposure record: NPM's container address changes when an upgrade recreates the container, and the record holds NPM's address on the panel network (`internal/core/guard.go:45`), not on darkpanel-npm.
- **Row 152 before-state.** The new flow fails only on a block the change itself adds, as the w12b spec review ruled for the hidden client (its A2, "refuse only what the change itself newly blocks", owner "Right" on 2026-11-13).
- **Spec review, 2026-11-14.** Fable's undecided item on row 152's ports: "443 only (Recommended)" — the relay covers the exposure's recorded HTTPS binding, as every other flow of the exposure does, narrowing the "80/443" of the row 152 answer. Approval: "Approve and run unattended", the run starting after the w12b merge.
- Early CI: first — consented 2026-11-14 under the owner's standing authorization for the v1 programme to push branches and run the harness. The CI configuration has no platform this host does not cover (`ci.yml` runs on the self-hosted Linux runner, `harness.yml` on ubuntu-24.04); the early run proves the riskiest assumption before any guard code is written (see Riskiest assumption).

Finish: merge

Worktree: in-place

## Scope

| Row | Section |
|---|---|
| v1 483 | IPv6 visitors with the integration off; Manual rules with the same tuple |
| security 152 | The userland proxy's leg |
| v1 482 | The m3g first-load flake |

Out of scope: IPv6 on darkpanel-npm; any change to the integration-on step list; the 55 rows named under Decisions.

## IPv6 visitors with the integration off

**Today.** NPM's network is IPv4 only and NPM publishes 80 and 443 on every address (`internal/modules/npm/deps.go:456-459`). An IPv4 visitor is DNATed by Docker around UFW. An IPv6 visitor reaches Docker's userland proxy, a socket of the host, through UFW's INPUT chain, which denies it by default. With the Docker integration on, the realizer already emits an incoming allow for a published family that has no container address (`internal/modules/firewall/dockerfw/realize.go:70-77`, pinned at `realize_test.go:144-154`), so `firewall.containers.apply` admits IPv6. With it off, `deployPublicComposer.Steps` returns no firewall step (`internal/modules/npm/composite_deploy_expose.go:93-95`), and the deploy form tells the operator to open the ports themselves (`web/src/modules/npm/instance-panel.tsx:69`).

**v2 input.** `npm.instance.deploy-public` v2 takes v1's fields plus two required booleans, given as `integration` is: `firewall`, the `supported` field of resource `firewall.status` (`internal/modules/firewall/status.go:17,101`), and `ipv6`, its `configured.ipv6` (`status.go:35,89`). The controller-side check step (`internal/modules/npm/controller_checks.go:60-62`) compares both with the host's state, as it compares `integration`, and refuses a change with `plan.state_changed` naming the field; a `firewall.status` read that fails is an error of the check step, as the `firewall.docker` read is (`controller_checks.go:149-166`). `firewall` and `ipv6` are optional inputs of `npm.instance.check` v1, compared only when present, so its released schema (`schemas/released/v1.0.0-beta.1/npm.instance.check/v1.json:21-26`) stays valid; deploy-public v1 keeps sending `hostId` and `integration` only.

**v2 steps.** With the integration on, the steps are v1's. With the integration off and `firewall` true, the composite gains a last step `ports`: `firewall.rules.apply` with `keepManual` set (see Manual rules with the same tuple), adding Darkpanel rules that allow TCP in from any source to any address, one rule per port and family: 80 and 443 for IPv4, and for IPv6 when `ipv6` is true. Each rule has a fixed ULID constant of its own, the same on every host, and the batch label is constant, so the plan and the apply build the same steps (apply rebuilds them and compares the state hash, `internal/core/lifecycle/composite_run.go:1132`, `internal/core/lifecycle/apply.go:334`), and a second deploy finds the rules present under their ids and plans no new rule (`internal/modules/firewall/actions_rules.go:159-165`). A rule present under one of those ids with another definition refuses the plan with `request.invalid_input` naming the id, as today; the user docs say how to remove it. A Darkpanel rule of another id with the same tuple (a leftover DERP rule; deploy refuses a host with DERP installed) is replaced by UFW and takes the deploy's id; this is today's behaviour of `firewall.rules.apply` and is left as it is.

With `firewall` false, on a host whose `firewall.status` reports `supported` false, where every firewall change is refused (`internal/modules/firewall/plan.go:57-60`), the steps are v1's and the host deploys as it does today. On a host without UFW the check step cannot read `firewall.docker` (`internal/modules/firewall/docker_resource.go:80-83`), so deploy-public fails there today and still does. On a host out of sync with UFW's configuration `firewall` is true and the ports step's guard fails as a hard refusal (`plan.go:137-147`), so v2 is refused until `firewall.state.repair`; the user docs say so. On a host where UFW is installed but inactive, the rules are stored and enforced once UFW starts.

The step is revertible, so the change dialog previews it, the guard checks it and it reverts unless confirmed; the engine accepts a revertible step only as the composite's last, which it is. If the confirmation window passes, the firewall change reverts and the composite undoes the deploy, as on the integration-on path (`docs/user/modules/npm.md:203-204`). `portsOpened` in v2's output is true when the composite holds a ports step. `npm.instance.confirm-public` stays required with the integration off: a UFW allow does not prove the internet reaches the host. `npm instance remove` leaves the allowances, as it does with the integration on.

**Manual rules with the same tuple.** Today an add whose tuple a manual rule holds is predicted as UFW's replacement of that rule (`internal/modules/firewall/predict/edit.go:350-371`, `419-421`), so the plan is refused with `firewall.manual_required` (`plan.go:111-114`, `diff.go:84-95`), and an operator who ran `ufw allow 443/tcp` as today's docs say could not deploy v2. `firewall.rules.apply` v1 gains an optional boolean input `keepManual`, default false, which keeps today's behaviour for every other caller (additive under `docs/versioning.md:55-56`). With it set, an added rule is left out when, in every family it covers, a manual rule holds its tuple and differs only in the comment (`matchTuple` returns -2); the diff carries a context line naming that manual rule; the post-change check (`actions_rules.go:126-131`) accepts the id as absent while that manual rule is present; output `rules` (`actions_rules.go:139-144`) leaves the id out. A manual rule with the same tuple and another action or log type, or one that holds only one family of a both-family rule, is refused as today. The ports step of deploy-public v2 sets the field; panel exposure add and remove and the DERP deploy and remove do not, and behave exactly as today. An agent that predates the field refuses the step's input (inference), so the user docs say that v2 with the integration off needs the agent at the controller's version.

**Clients, copy and docs.** The web deploy form plans v2. It reads `firewall.status` beside `firewall.docker`; Plan stays disabled until both reads succeed, as `docker.isSuccess` gates it today (`instance-panel.tsx:37`), and a failed status read shows its error as the Docker read does. Its integration-off line says, when `firewall` is true, that the deploy allows 80 and 443 in the firewall as its last step and that the operator then checks them from outside and confirms the instance is public; when `firewall` is false it keeps today's text. The CLI's `npm instance deploy-public` becomes v2, since the generated command takes the latest version (`internal/cli/names.go:62-64`); `--firewall` and `--ipv6` are given by the operator as `--integration` is, and the docs say where to read them (`darkpanel firewall status`). `docs/user/modules/npm.md` (lines 49-55, 66-69, 134, 146-161, 196-204, 309-310, and the errors table), `docs/api/npm.md` and the action's summary describe v2: the ports step on both paths, the confirmation window and the undo on expiry, why IPv6 needs the INPUT allow, the four rule ids, the out-of-sync refusal, the agent version, and the rules an operator of an instance deployed before this change adds. `CHANGELOG.md` records deploy-public v2 and the `keepManual` field.

**Harness.** The harness gains versioned plan and apply helpers; today's hardcode `/v1` (`test/harness/m4_helpers_test.go:63,118`). The ipv6-client step stops adding its own `ufw allow … ::/0 … 443` (`test/harness/m6_panel_test.go:445-450`) and relies on the deploy's rule (A7). The scenario at `test/harness/m6_npm_test.go:560-579` deploys through v2, expects and confirms the `ports` step, and expects `portsOpened` true. The ipv6-client-guard step (w12b A26) first removes the deploy's IPv6 443 rule by its id through `firewall.rules.apply` over the loopback API, adds the ULA-only allow as today, plans the delete of that allow with today's assertion unchanged (`m6_panel_test.go:251`), and afterwards re-adds the deploy's rule under its id.

## The userland proxy's leg

**Today.** For a request through an NPM exposure, the guard checks the visitor-to-host leg of each public binding, on both families for `[::]` (`internal/modules/firewall/guard/flows.go:303-335`). The second leg of an IPv6 visit, from the userland proxy to NPM's IPv4 container address, leaves the host on its OUTPUT path, and no flow covers it. A change that blocks that output, such as a default-deny outgoing policy, cuts every IPv6 visitor and passes the guard (`docs/security/threat-model.md:235` records it as a residual risk).

**Change.** The leg is built in `viaFlows`, for an NPM exposure, under its conditions: a retired exposure gets none (`flows.go:264`), and only the controller's host builds them (`flows.go:116-118`). For each public binding recorded on `[::]` or on an IPv6 address (an IPv4 binding has no IPv6 side), the guard looks up the TCP socket listening on the binding's port at `[::]` or at the binding's address in the socket table it already reads (`internal/modules/firewall/guard/facts.go:61-67`). The exposure records only its HTTPS binding (`internal/core/listeners.go:41-43`), so the leg covers 443. A socket may have several holders (`internal/platform/netstat/sockets.go:65-91`):

- A holder named `docker-proxy` whose command line names an IPv4 container address and a port: one protected flow, output, TCP, source left for routing to choose (`internal/modules/firewall/guard/route.go:84-100`, as `tailscaleFlows` uses), to that address and port, labelled `exposure <id> IPv6 relay to <addr>:<port>`. Like the wild-source flows, it is compared with the before-state (`internal/modules/firewall/guard/cells.go:297`): a change fails on it only when the change itself blocks it, and a leg the rules already blocked is counted with the sources already blocked. The comparison flag is part of the flow's identity (`flows.go:77-88`), so a compared flow never absorbs an uncompared one with the same packet; with no before-state (`Input.Before` nil) the relay must pass, as a wild-source flow must. An unresolved route is an unknown probe, as for every flow (`route.go:76-79`).
- A `docker-proxy` holder whose target is an IPv6 address: no flow (Docker then DNATs IPv6).
- A `docker-proxy` holder whose command line cannot be read or names no container address or port, or a listener none of whose holders is `docker-proxy`: an unknown flow naming the reason or the holders, so the change is refused unless overridden, as every undecidable flow is. A process that exited between the socket read and the command-line read gives that unknown flow, not a guard error.
- No listener: no flow; nothing serves IPv6 visitors on that binding.

`docs/security/threat-model.md` replaces the residual-risk line 235 with the guarded leg, its fail-closed cases, and the Docker release it was verified on (the harness pin, 29.9.1, `test/harness/images.json`). NPM accepts Docker 28.0 and later (`internal/modules/npm/controller_checks.go:47-54`); a release whose `docker-proxy` takes another argument form gets the fail-closed unknown flow. Unknown relay flows are not compared with the before-state, so on a host where the read fails every guarded change through the exposure is refused until overridden, whatever it changes; the threat model says so. With Docker's userland proxy disabled it is Unknown whether `dockerd` alone holds the listener; if it does, that host gets the unknown flow. No harness VM covers it.

## The m3g first-load flake

`web/e2e/m3g-held-data.pw.ts` fails one test in about one full run in two, a different one each time (UI-4 at the 60 s test timeout, UI-3 at a 5 s first-load visibility wait), at host load 1-3; targeted reruns pass (w12b ledger, Task 8). The plan's task finds the cause with dforge:systematic-debugging before changing anything: candidates are the first page load against a cold Vite dev server, fixture state shared between tests, and an animation or refetch timer the test races. The investigation is time-boxed to 10 instrumented full runs (traces on, host load below 6). The fix removes the cause; a wait is lengthened only when the cause is a slow load the test should wait for, and the ledger records which. When the box ends with no cause found, the task changes no test, records the evidence in the ledger, and leaves row 482 open for the owner.

## Testing

Go unit tests extend the files beside each change: `internal/modules/npm/composite_deploy_expose_test.go` for v2's steps and the check step, `internal/modules/firewall/actions_rules_test.go` for `keepManual`, the guard's flow tests for the IPv6 relay with a fake proc root holding a `docker-proxy` process, the CLI's generated-command tests for the v2 flags, and the web Vitest test of the deploy form for the copy, the second read and the v2 input. The released-schema check covers v1 staying as it is. The harness steps run on VMs only, at early CI and at gate 7. Row 482's evidence is repeated full runs of the m3g spec.

## Acceptance

A1. [test, high] With the integration off, `firewall` true and `ipv6` true, the plan of `npm.instance.deploy-public` v2 ends with a `ports` step of `firewall.rules.apply` with `keepManual` set, adding four Darkpanel-tagged rules, each with its fixed id, allowing TCP 80 and 443 in from any source to any address for IPv4 and for IPv6; `portsOpened` is true.
A2. [test, neg, high] With the integration on, the plan of v2 holds v1's step list: its last step is `firewall.containers.apply` allowing 80 and 443, and it holds no `firewall.rules.apply` step; the check step's input carries the two new fields; `portsOpened` is true.
A3. [test, neg, high] Planning v2 with the integration off on a host that holds Darkpanel's four allowances under those ids plans no added rule.
A4. [test, high] The plan and the apply of one v2 composite build the same steps, so the apply is not refused with `plan.state_changed`.
A5. [test, high] UI-1: with the integration off and `firewall` true, the deploy form says that the deploy allows ports 80 and 443 in the firewall as its last step and that the operator then confirms the instance is public; with the integration on, or `firewall` false, the text is today's; Plan stays disabled until both reads succeed, and a failed status read shows its error.
A6. [demo, high] OP-1: the deploy form is proven at 375x812, 768x1024 and 1440x900, light and dark, in these states: integration on; off with `firewall` true; off with `firewall` false; either read loading; either read failed.
A7. [test, high] Harness: on every OS, an IPv6 client reaches NPM on 443 after a v2 deploy-public with the integration off, with no `ufw` rule the harness added itself.
A8. [test, high] Harness: on every OS, a process named `docker-proxy` among the holders of NPM's `[::]:443` listener has `-container-ip <NPM's darkpanel-npm IPv4 address> -container-port 443` in `/proc/<pid>/cmdline`, read by the harness on the VM.
A9. [test, high] For a request through an NPM exposure whose IPv6 443 listener is held by `docker-proxy` with an IPv4 target, a change that sets the default outgoing policy to deny is refused with `firewall.lockout` naming `exposure <id> IPv6 relay to <addr>:<port>`, unless overridden.
A10. [test, neg, high] The same change passes the relay flow when it also allows output to the container address and port.
A11. [test, neg, high] A `docker-proxy` holder whose target is an IPv6 address, or no listener on the binding, adds no relay flow.
A12. [test, high] A relay whose command line cannot be read or names no container address or port, or a listener none of whose holders is `docker-proxy`, refuses the change with a reason naming what could not be read or the holders, unless overridden; a listener held by `docker-proxy` and another process is read through its `docker-proxy` holder.
A13. [test, neg, high] A request that did not come through an NPM exposure, a retired exposure, and a host other than the controller's get no relay flow.
A14. [test, high] Harness: on every OS, planning a default-deny outgoing policy through the exposure from the IPv6 client is refused naming `IPv6 relay to <NPM's darkpanel-npm IPv4 address>:443`; nothing is applied.
A15. [test, high] After a fix, the m3g-held-data spec passes 10 consecutive full runs at host load below 6 with no retries; or, when the time box ends without a cause, the ledger holds the traces of the 10 instrumented runs, no test is changed and row 482 stays open.
A16. [demo, high] `docs/user/modules/npm.md` describes v2, and the generated `docs/api/npm.md` gives its summary, input and output: the integration-off ports step, its confirmation window and undo, why IPv6 needs it, the rule ids, the out-of-sync refusal, the agent version and the rules an operator of an earlier instance adds; `docs/security/threat-model.md` describes the relay flow as guarded, its fail-closed cases and the Docker release it was verified on, in place of the residual-risk line; `CHANGELOG.md` records v2 and `keepManual`.
A17. [test, neg, high] With the integration off and `firewall` false, the plan of v2 holds v1's step list and no firewall step; `portsOpened` is false.
A18. [test, high] With `ipv6` false, the ports step adds the two IPv4 rules only.
A19. [test, high] The check step refuses a v2 change with `plan.state_changed` when the host's `firewall` or `ipv6` differs from the input.
A20. [test, neg, high] `npm.instance.deploy-public` v1 plans the same steps as before this change, with the integration on and off, and the same check-step input.
A21. [test, neg, high] On a host with a manual `allow 443/tcp`, `firewall.rules.apply` with `keepManual` set, adding a Darkpanel rule with the same tuple, plans no change to the manual rule, keeps its comment, and its diff names the manual rule that already allows it.
A22. [test, high] A relay leg the rules already blocked before the change does not refuse a change that does not touch it.
A23. [test, high] The apply and post-change check of a `keepManual` batch with a left-out rule succeed, and output `rules` omits the left-out id.
A24. [test, neg, high] Without `keepManual` the same batch is refused with `firewall.manual_required`; with it, a manual `deny 443/tcp` is still refused.
A25. [test, high] v2 with the integration off on a host holding a manual `ufw allow 443/tcp` plans the two port-80 rules only and names the manual rule.
A26. [test, neg, high] v2 with the integration off on a host out of sync with UFW's configuration is refused at the ports step's guard until `firewall.state.repair`.
A27. [test, high] The generated CLI command `npm instance deploy-public` plans v2 and carries `--firewall` and `--ipv6`.

## Riskiest assumption

That the `docker-proxy` process holding NPM's IPv6 listener names its container target on its command line (`-container-ip <addr> -container-port <port>`) under the Docker Engine the harness pins (29.9.1, `test/harness/images.json`), and that the agent can read it from `/proc/<pid>/cmdline`. Evidence so far: published Docker 28.0.0 source builds exactly `-proto -host-ip -host-port -container-ip -container-port [-use-listen-fd]` (moby `libnetwork/portmapper/proxy_linux.go`, `cmd/docker-proxy/main_linux.go`, read by the spec review on 2026-11-14), and the parent does not close the passed listener there, so dockerd likely shares the socket; A12 reads a shared socket through its `docker-proxy` holder. Nothing is checked against Docker 29 on a live host, and no probe may run on this host, whose Docker daemon the plan must not touch.

unverified — the plan's first task verifies it. Its harness probe (A8) runs on the pushed branch before any guard code is written and logs every holder of the listener with its command line. Three outcomes: the target is read and is NPM's darkpanel-npm address, proceed; the target is read on another network of the container, proceed with the address read and correct A8 and the docs; no `docker-proxy` holder, or the arguments absent or in another form, stop and return row 152 to the owner, with the Docker Engine API's view of NPM's darkpanel-npm endpoint as the next source.

## Critic findings

Architecture critic (judge-opus), 2026-11-14: 0 Blocker, 3 High, 6 Medium (its verdict line counts 5; its findings list 6), 3 Nit; all 3 High fixed in the spec, and the Fable spec review found each fix holds.

- [High, fixed] The relay flow claimed a before-state comparison the guard gives only to wild-source flows (`cells.go:297`): the flow is now compared with the before-state (Decisions, A22).
- [High, fixed] Rule ids minted at plan would fail every apply, since apply rebuilds the steps and compares the state hash: the ids are fixed ULID constants, one per port and family (A4).
- [High, fixed] "Integration off" includes hosts whose firewall Darkpanel cannot manage: v2 carries `firewall` and adds the step only when it is true (A17, A19).
- [Medium, fixed] The output contract and callers change silently: the change ships as v2, `portsOpened` is defined, and the docs name the window and the undo (A16, A20).
- [Medium, fixed] A manual allow with the same tuple: the premise (retagging) was wrong, as the spec review found; `keepManual` lets v2 deploy past it (A21, A23-A25).
- [Medium, fixed] A26 rework left open and a both-family rule cannot be deleted for IPv6 alone: per-family rules, and the harness removes the deploy's IPv6 443 rule before w12b's unchanged ULA proof.
- [Medium, fixed] A listener held by another process failed open and several holders were undefined: any `docker-proxy` holder is read, a listener with none is an unknown flow (A12).
- [Medium, fixed] The probe covers Docker 29.9.1 only, while NPM accepts 28.0 and later: the threat model names the verified release, 28.0.0 source corroborates the argument form, and another form fails closed.
- [Medium, fixed] No outcome if the m3g investigation finds no cause: a 10-run time box, after which the row returns to the owner (A15).
- [Nit, fixed] A15 carried an open question: 10 runs, a 1 in 1024 chance of passing by luck at the observed rate.
- [Nit, fixed] The relay label collided with `exposure <id> proxy`: the flow is `exposure <id> IPv6 relay to <addr>:<port>`.
- [Nit, fixed] Retired exposures and other hosts were unstated: the relay is built in `viaFlows` under its conditions (A13).

## Review rulings

Fable spec review, round 1, 2026-11-14: 20 rulings, 1 undecided, open 0 Blocker, 0 High; no second round.

- [High] Manual rules with the same tuple: today's add is refused with `firewall.manual_required`, not retagged; `firewall.rules.apply` v1 gains opt-in `keepManual`, set only by deploy-public v2 (A21, A23-A25).
- [Medium] `firewall` and `ipv6` are optional inputs of `npm.instance.check` v1; v1 of deploy-public sends neither (A20).
- [Medium] `firewall` and `ipv6` come from `firewall.status`; a failed read is a check-step error; the no-UFW and out-of-sync cases are stated (A26).
- [Medium] The CLI takes `--firewall` and `--ipv6` from the operator (A27).
- [Medium] The form reads `firewall.status`, gates Plan on both reads and proves five states (A5, A6).
- [Medium] The ipv6-client-guard step removes the deploy's IPv6 443 rule first and keeps w12b's ULA assertion unchanged.
- [Medium] The relay's source is left to the router; the interface-prefix case is dropped.
- [Medium] A8 is a harness read of the command line; the stop condition has three outcomes.
- [Medium] A15 covers the no-cause outcome of the time box.
- [Nit] The comparison flag is part of the flow's identity; with no before-state the relay must pass.
- [Nit] An unknown relay refuses every guarded change through the exposure; userland-proxy-off hosts are Unknown and uncovered.
- [Nit] A1, A2 and A17 name `ipv6` and `portsOpened`.
- [Nit] A leftover Darkpanel rule of another id with the same tuple is replaced as today.
- [Nit] `CHANGELOG.md` entries; versioned harness helpers.
- [Ruling, w12c Task 9] A16's prose (the ports step, its confirmation window, why IPv6 needs the allow, the rule ids) lives in `docs/user/modules/npm.md`; the generated `docs/api/npm.md` covers v2's input, output and summary, since `docs/api` is generated from the registry and is not edited by hand.
- [Ruling, owner 2026-11-14, M3 audit] The Task 9 ruling stands and A16 is amended to say so; the agent-written UI proof (A6) and the judges' reading of the docs (A16) count as the demo observations.
- [Holds] Critic High 1 (before-state comparison).
- [Holds] Critic High 2 (fixed ids).
- [Holds] Critic High 3 (`firewall` input), in substance.
- [Holds] v2 follows `docs/versioning.md`.
- [Holds] Other callers of `firewall.rules.apply` behave as today.
- [Holds] R40: the design adds refusals, not lockouts.
