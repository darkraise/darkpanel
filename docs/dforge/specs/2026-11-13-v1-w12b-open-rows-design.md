# v1 w12b: the open rows before the M3 audit

**Date:** 2026-11-13
**Parent spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md
**Rows:** security register (`docs/dforge/registers/2026-11-06-v1-security-review.md`) rows 6 and 131-146; v1-completion register (`docs/dforge/registers/2026-10-21-v1-completion.md`) rows 479-481
**Execution strategy:** sequential — inline execution
**Depth:** full — risk

## Decisions

The owner ruled the 59 open rows on 2026-11-13, through the questions below.

- **Flow.** "Ruling on 20 open register rows … Which do you want?" — "Full dforge flow (Recommended)".
- **Rows in `verify`.** The 39 rows in state `verify` need no ruling: their plans are merged, and the M3 milestone audit confirms them. This spec does not touch them.
- **Security row 6 (high).** Options: fail closed; IPv6 on the darkpanel-npm network; both; accept and record. Answer: "Fail closed (Recommended)" — "When a request came through an NPM exposure and the reported client is a host address, treat the real client as unknown: check that binding's family from every source range, private, ULA and tailnet included, and refuse unless overridden."
- **Security row 131 (medium).** Answer: "Trace in the #6 plan (Recommended)" — "If it leaks, mask it the way row 12 did, with a regression test; if it does not, record the evidence and close the row." The trace on 2026-11-13 found a leak into the agent's log only (see Row 131).
- **Security rows 132-146 (low).** Options: fold 2 and defer 13; fold all 15; defer all 15. Answer: "Fold all 15".
- **v1 rows 479-481 (minor).** Answer: "Fold into the plan (Recommended)" — "two web layout fixes proven at 375/768 px, and a harness message that tells a failed download from a missing pin."
- **State boards.** Answer: "No boards (Recommended)" — "Touch depth: fix, then prove with worst-case data at 375, 768 and 1440 px, light and dark, like the m3g held-data proof."
- **UI/UX depth:** touch — two styling fixes on the Cloudflare connectors page and the NPM admin banner. Per dforge:ui-ux the delta lives in the proof report, not in this spec; its items are the `UI-` and `OP-` acceptance statements below.
- **Approach for row 6.** Considered and not chosen: putting IPv6 on darkpanel-npm (it changes deploy and upgrade, needs existing deployments recreated and depends on the daemon's ip6tables), and accepting the risk (spec 9 requires every high finding fixed). The do-less option, accepting and recording, would leave a high finding open, which spec 9 forbids.
- **Approach for row 146.** The composite runner carries the client peer and exposure path of the request that applied or repaired the composite, in memory on its run. Persisting them was not chosen: a composite resumed after a controller restart starts no step (`internal/core/lifecycle/composite_run.go:486-487`), so nothing would read a stored copy.
- Early CI: first — consented 2026-11-13 under the owner's standing authorization for the v1 programme to push branches and run the harness. The CI configuration has no platform this host does not cover; the early run exists to prove the riskiest assumption: Task 1's harness probe is pushed and run (`gh workflow run harness.yml`) before any guard code is written. The probe has three outcomes: a host address recorded, proceed; another address recorded, stop and return row 6 to the owner, with IPv6 on darkpanel-npm as the next option; no request recorded, inconclusive: fix the probe and rerun before any guard code.

- **Spec review, 2026-11-13.** A2 "Right" (refuse only what the change itself newly blocks); A3 "Right" (IPv4 bindings are checked too); A12 "Right" (variable forward hosts stay uncounted and documented). Fable's undecided item on plan-time 503 detail: "No change now (Recommended)" — the spec states where each detail is visible.

Finish: merge

Worktree: in-place

## Scope

| Row | Section |
|---|---|
| security 6 | Row 6: a client the guard cannot see |
| security 146 | Row 146: composite steps |
| security 131 | Row 131 |
| security 132, 141, 143, 144 | NPM forward targets |
| security 133, 135, 136, 145 | NPM upgrade forward check |
| security 134 | NPM assignable addresses |
| security 137, 138 | npmapi tests |
| security 139 | NPM deploy detail |
| security 140 | NPM operator header lines |
| security 142 | Threat model |
| v1 479, 480 | Web layout |
| v1 481 | Harness pin fetch |

Out of scope: every row in state `verify`, and any change to how NPM is deployed.

## Row 6: a client the guard cannot see

**Today.** The default NPM deployment puts NPM on darkpanel-npm, an IPv4-only bridge, and publishes 443 on `[::]`. Docker's userland proxy, a process in the host's network namespace, carries an IPv6 client to the container's IPv4 address, so NPM sees the connection coming from one of the host's own addresses and names that address in `X-Darkpanel-Client`. `runtimeClient` (`internal/controller/api/runtime.go:88`) accepts it, and `npmClientFlows` (`internal/modules/firewall/guard/flows.go:335`) drops it through `fromHere`, so the guard checks no flow for the real client. The internet flow covers only `2000::/3` minus documentation space (`internal/modules/firewall/eval/cells.go:19`). A change that drops a ULA or tailnet client on 443 passes without an override.

**Change.** In `npmClientFlows`, a client for which `fromHere` holds means a client the guard cannot see. For each public binding of the exposure, the guard adds the public leg's own flows for that binding, as `npmPublicFlows` (`flows.go:299-329`) builds them — same packet, `fwdReplies`, `standIn`, and the rule that a `[::]` binding's IPv6 leg exists only when the host has an IPv6 address that is neither loopback nor link-local — labelled `<exposure> hidden client` and marked to take the unicast cell space instead of the internet one. `flowKey` gains the mark, so the set (`flows.go:75-96`) keeps the hidden-client flow beside the public flow of the same packet. "Every unicast address" is the family's whole space minus unspecified, loopback, multicast and every address of this host (`localAddrs`, `flows.go:125-134`): a packet from one of the host's own addresses arrives only through loopback, which no flow is checked on (`fromHere`), and a one-address cell of a host address can be undecidable under strict reverse-path filtering, as `addPolicyCuts` (`internal/modules/firewall/guard/cells.go:215-219`) already avoids. IPv4 is also minus `0.0.0.0/8` and `240.0.0.0/4`. Private, CGNAT and tailnet (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`), ULA, link-local and special-purpose ranges are all in it.

The flow is evaluated like the internet flow of the public leg: `cellFlows` (`internal/modules/firewall/guard/cells.go:63`) asks `eval` for the unicast cells (a new `eval.UnicastCells(rs, v6, host)` beside `InternetCells`, sharing its partition code and taking the host's addresses as one-address holes) instead of the internet cells, and a cell fails only when the change itself blocks it (compared with the rules before the change, `cells.go:60-79`). The cell limits of `overCellLimit` apply unchanged, and over the limit the flow is unknown, which refuses. On a typical Docker host the rules and routes name a few dozen private prefixes (bridges, the tailnet, the LAN), so the unicast space splits into hundreds of cells, far under the 65536-cell limit; a `cells_test.go` case near the limit pins the over-limit refusal. A refusal names the first blocked range, its label, and the override, as other guard refusals do. In a revert's guard, which has no before-state (`lockoutVia`, `internal/modules/firewall/wire.go:66-78`), the hidden-client flow of the snapshot's guard context must pass in every unicast cell, as the public leg must pass in every internet cell.

The concrete client flow for a client that is not a host address is unchanged. A direct panel client (`panelFlows`) is unchanged: it does not pass through NPM.

**Threat model.** `docs/security/threat-model.md` lines 215, 229, 233 and 234 change: the client of a request through an NPM exposure is checked whatever its range when NPM names a non-host address; when NPM names one of the host's addresses (Docker's userland proxy, or a browser on the host), every unicast source of each binding's family other than the host's own addresses is checked, and only a block the change itself adds refuses; line 233 widens to say a revert guard without a before-state fails on any unicast range the host already blocked on the port for such a client. The leg from Docker's userland proxy to NPM's container (bridge gateway to the container's address, on the host's output path) is not a protected flow; the residual risks say so, and a new security register row records it for a later fix.

## Row 146: composite steps

**Today.** `runComposite` runs on the engine's run context (`internal/core/lifecycle/composite_run.go:392`), and `guardContext` (`internal/controller/dispatch/guard.go:35-58`) reads the client peer and the exposure path only from the call's context. A firewall step of a composite applied through an NPM exposure gets neither, so it has no NPM client flow and no exposure flows.

**Change.** `compositeRun` (`composite_run.go:56`) gains the client peer and the `core.Proxied` value read from the context of the apply (`applyComposite`, `composite_run.go:1038`) and of a repair that starts a run (`composite_ops.go:98`). `startStep` runs each child's plan and apply on the run context with those values restored through `core.WithClientPeer` and `core.WithProxied`, so `guardContext` and `withGuardRefresh` see them exactly as for a top-level change. A run restores them on each child's plan and apply, the only calls the dispatcher builds a guard context for (`dispatch.go:204-208`, `apply.go:50`). An unwind needs no restoring: a firewall step is revertible and therefore a composite's last (`internal/core/lifecycle/composite.go:202-247`), an unwind reverts it (`composite_unwind.go:229`), and the firewall's revert, whenever it does not restore the prior state cleanly, runs the lockout guard with the guard context its snapshot took from the plan (`internal/modules/firewall/snapshot.go:102`, `revert.go:157-173`); a revert that restores cleanly runs no guard, as for a top-level change, which now holds the client and the exposure path, also after a restart. The composite writes them to none of its own rows (`core_composites`, `core_composite_steps`); a child's sealed plan input and the firewall snapshot hold the guard context, as they do for a top-level change. A run started without them (recovery, `composite_watch.go:124`, and a release, `composite_ops.go:366`, pass `nil`) starts no step, which this change does not alter. A composite sends its steps fixed inputs, so a step carries no override. A step whose guard fails is refused at its plan with `plan.guard_failed` and fails as any refused child does (`stepNotApplied`, `composite_run.go:653-660`): a reverse composite unwinds, a forward composite needs repair, and the composite's error names the step and the guard's message. The way through is to apply the composite from a client the step does not cut, or to make the firewall change directly with the override. Restoring the peer also gives a composite applied by a direct panel client its panel flow. The exposure path is the one captured at apply: a composite that changes NPM's bindings in an earlier step guards its later steps against the bindings it was applied with, which is accepted and recorded. `Retired` keeps retiring an exposure the composite has removed, as today. The comments in `internal/controller/dispatch/guard.go:28-29` and `internal/core/guard.go:29-30` change to match.

## Row 131

**Trace (2026-11-13).** `validateConfig` (`internal/modules/docker/compose_check.go:435-468`) inspects images, volumes and networks by the names `docker compose config` resolved, so `.env` values are already substituted. A failure other than not-found goes to `engineError` (`internal/modules/docker/effects.go:102-115`), which keeps the `dockerapi` error as its cause. That error quotes the request path, and through Go's URL error the whole URL (`internal/platform/dockerapi/client.go:261,277,286-295,311`), so the image reference, volume name or network filter appears in it. The caller never sees it: an error without a code becomes Internal with an empty detail. The agent's log does: `writeProblem` (`internal/agent/api/handler.go:874`) and `changeError` (`internal/core/lifecycle/apply.go:465`) log the whole chain for a 5xx.

**Change.** Classify, as row 12 did (`composeFailureDetail`, `compose_check.go:530-539`). The checks in `validateConfig` turn an inspect or list failure into an error built only from fixed text, the failure class, the object kind (image, container, volume, network) and the object's key in the Compose file — the service key for an image or a container, the top-level `volumes` or `networks` key for the others — which Compose does not interpolate. The `dockerapi` error is not kept as a cause. An unreachable engine or a transport failure keeps the code and fixed text `engineError` gives an unreachable engine today. An HTTP error answer is `errcode.Unavailable` with the detail "the Docker Engine answered HTTP <status> while checking the <kind> of <key>" (the status number only); an answer that did not decode is `errcode.Unavailable` with "the Docker Engine gave an answer that did not decode while checking the <kind> of <key>". The error value and the agent's log carry that text, and the change record does when the failure happens during an apply. A refusal at plan time answers the code and its title only, because a 5xx problem carries no detail (`internal/agent/api/handler.go:861-875`, `internal/controller/api/wire.go:37-51`).

## NPM forward targets

`forwardTargets` (`internal/modules/npm/network_claims.go:821-875`):

- **132.** Each distinct forward host name is resolved once per call, the distinct names concurrently, at most 8 at a time, under one deadline for the whole call of `resolveTimeout` plus one second; later uses reuse the answer. A name still unresolved at the deadline counts as a transient failure (143). Docker's own names are read once per call; the deadline covers the lookups with the host's resolver.
- **141.** A failed proxy-host listing, whatever the status or failure, is `errcode.NPMUnhealthy`, not `proxyhostAddAnswer`'s `invalid_input`, with the failure class in the detail: "Nginx Proxy Manager did not answer", "Nginx Proxy Manager answered HTTP <status>" (the number only) or "Nginx Proxy Manager gave an answer that did not decode". Attach, the undo of a detach, and upgrade see the new code. The error value and the agent's log carry the detail, and the change record does for an apply-time failure; a plan-time refusal answers the code and its title only.
- **143.** A resolver error that `transientLookup` (`internal/modules/npm/proxyhost_add.go:416`) calls transient fails the call with a Definite `errcode.Unavailable` whose detail is "forward host <host> could not be resolved: the resolver did not answer; retry". A name that does not exist is still skipped.
- **144.** A forward host carrying a path (`10.0.0.5/x`) is checked by the part before the first `/`; that part goes through `proxy.LiteralAddr` first, and what it cannot parse is resolved as a name, a name that does not exist being skipped as today. A forward host carrying an nginx variable (`$`) is not counted, and the threat model says so: only an NPM administrator can write one, outside Darkpanel.

## NPM upgrade forward check

`forwardCheck` (`internal/modules/npm/instance_upgrade.go:107-131`):

- **133.** The proxy hosts are listed and their names resolved once per upgrade, before the network loop. `forwardGuard` splits into fetching the targets and a pure check of the targets against one network.
- **145, 136.** The networks checked are the old container's networks plus darkpanel-npm, which the new container always joins (`instance_upgrade.go:187-189`), inspected even when the old container is not on it. A container with no networks therefore still lists the proxy hosts, and an upgrade with NPM's API down is refused with `npm.unhealthy`, whose detail names the way out: start Nginx Proxy Manager and retry, or remove and redeploy the instance. The detail reaches the error value, the agent's log and an apply-time change record; at plan the caller sees `npm.unhealthy` and its title. The threat model records that an upgrade cannot repair an NPM whose API does not answer. A darkpanel-npm network that is missing at the check is a Definite `errcode.NotFound` naming the network, a 4xx code, so the name reaches a plan-time caller.
- **135.** Tests cover the `npm.unhealthy` refusal at plan and at apply, and with the NPM container stopped.

## NPM assignable addresses

**134.** `networkAssignable` (`network_claims.go:881-908`) leaves out the subnet's network address, except in an IPv4 /31 or /32, and the broadcast address of an IPv4 subnet shorter than /31, as well as the gateway and held addresses it already leaves out. This follows Docker's allocator (moby v27.5.1 `libnetwork/ipams/defaultipam/allocator.go`, `newPoolData`, read 2026-11-13); the plan confirms it at the pinned Docker major (28).

## npmapi tests

- **137.** `TestW12aFailuresKeepNoAnswerText` (`internal/modules/npm/npmapi/transport_test.go:40`) gains a row whose answer declares `Content-Length: 100` and sends less, through the `read` call. The error is `npmapi: read GET /api/nginx/proxy-hosts/1 answer: no usable answer`.
- **138.** The "refused connection" row uses a client whose dialer returns `syscall.ECONNREFUSED`, so no closed server's port is reused.

## NPM deploy detail

**139.** `deployCancelledWait` (`internal/modules/npm/instance_deploy.go:36-38`) keeps naming a cancelled wait. A wait that ended because its deadline passed (`errors.Is(err, context.DeadlineExceeded)`) gets its own detail: "the deploy ran out of time while it waited for Nginx Proxy Manager to start".

## NPM operator header lines

**140.** `proxy.OperatorConfig` (`internal/modules/npm/proxy/location.go:39-50`) drops every line whose first two whitespace-separated tokens are, compared case-insensitively, `proxy_set_header` and `x-darkpanel-client` with any surrounding single or double quotes removed from the second, at any indentation and with any whitespace (tabs, repeated spaces) between them, not only the module's exact line. `npm.proxyhost.update` then writes the module's line once, and nginx sends the header once. A line is dropped only when the directive is all it holds: the value and one `;` follow, then only whitespace or a `#` comment. A line of any other shape is kept as the operator's; the threat model records that it still makes nginx send the header twice, which fails closed on panel hosts. The existing test row that keeps an indented module line as the operator's is reversed.

## Threat model

- **142.** `docs/security/threat-model.md:291`: an `operate` caller can stop the container holding a forwarded address during an upgrade, between the forward check and the new container's start, so "an address another container held when it was checked can be freed before NPM connects" is a risk an `operate` caller can force, not only a race.
- The row 6, 141, 143, 144 and 146 changes are reflected in the Firewall and Exposure bullets.

## Web layout

Rows 479 and 480, at touch depth, following the patterns the codebase already uses:

- `ConnectorsTable` (`web/src/modules/cloudflare/connectors-page.tsx:41-82`): the State badge cell is `whitespace-nowrap` with a `size="sm"` badge (`web/src/pages/host-overview.tsx:98`). The table keeps a minimum width of 40rem and scrolls inside its wrapper (`host-overview.tsx:80-82`), so tunnel and container names wrap whole words or, for a name with no break, at the cell edge (`wrap-anywhere`, as `host-overview.tsx:119`), never a character or two per line.
- `HostChoice` (`connectors-page.tsx:218-237`): the label of a host radio wraps at the card edge (`min-w-0`, `wrap-anywhere`) and never runs past the card's right edge.
- `web/src/modules/npm/banner.tsx:47`: the Open Nginx Proxy Manager link breaks within the banner and is underlined at rest (`underline underline-offset-4`, as `web/src/pages/update-now-dialog.tsx:111`).

The proof extends `web/e2e/m3g-held-data.pw.ts`'s worst-case fixtures (63-character host, 64-character tunnel, 62-character container, 62-character NPM admin hostname). Its overflow check also compares each named element's bounding box with its container's, because `scrollWidth` misses clipped overflow. The named elements are the State badge, the tunnel and container name cells, the Host label, and the banner's link and description; the container of a table cell is the table, of the others the card or the banner.

## Harness pin fetch

**481.** `fetchFunc` (`test/harness/provision.go:24-35`) runs curl with `-S` and captures the HTTP status (`-w '%{http_code}'`). A 404 or 410 keeps today's "pinned package <name>=<version> missing; update the harness pins". Any other failure prints "could not download pinned package <name>=<version> (curl exit <n>, HTTP <status>)", with curl's own error line. `TestFetchChecksThePins` (`test/harness/provision_test.go:82-110`) gains a case for each.

## Testing

Every row gets a named regression test that fails before its fix, except 142 (wording only) and 137 and 138 (they are tests). The guard tests extend `internal/modules/firewall/guard/exposure_test.go` (`TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused`) and `cells_test.go`; the composite test drives a composite with a firewall step through `internal/controller/dispatch` with a proxied context, beside `guard_test.go`. The Compose tests extend `TestCCPrepareAppliesTheConfigurationRefusals` (`internal/modules/docker/compose_check_test.go:514`). The NPM tests extend the tests named in each section. The harness (`test/harness`) gains the row 6 probe in two parts. Task 1 adds the first: the lab's IPv6 is on-link `2001:db8:d0::/64` only (`test/harness/m2_test.go:41,155-167`), so the step puts a ULA /64 (`fd00:d0::/64`) on the scanner's link interface and the matching on-link prefix on the target, asserts as a precondition that the target reaches the scanner's ULA address, sends a request through the default NPM exposure with `curl --interface <ula>`, and asserts that the client the controller records — read from the audit source text "`<client> via <proxy>`" (`clientAddr`, `internal/controller/api/server.go:209-211`) — is one of the target's own addresses. A second precondition first checks that a request through the exposure over IPv6 from the scanner's existing `2001:db8:d0::/64` address succeeds. It runs early (see Decisions, Early CI). A later task adds the second part once the guard change exists: a firewall change dropping that ULA source on 443 is refused without the override, and a change made through the same ULA client that blocks nothing on 443 (allowing another port) passes without it. The guard tests also cover a host with strict reverse-path filtering and a kept rule whose source is a host address (a change that blocks nothing on the binding passes, and the existing row "a client address of this host", `exposure_test.go:123-125`, keeps passing) and a revert guard without a before-state. Gate 7 (the harness in GitHub Actions) runs both at the end, as the plans before this one did.

## Acceptance

A1. [test, high] Row 6: when a request through an NPM exposure carries a client that is one of the host's addresses or loopback, a firewall change that newly blocks any unicast source of a public binding's family (a ULA, tailnet, private or link-local source included) on that binding is refused unless overridden, and the refusal names the first blocked range and the exposure; on an IPv4 binding reached through Docker's DNAT, the hidden-client flow and the public leg give the same verdict for the same public source.
A2. [test, neg, confirmed] Row 6: for such a client, a change is not refused for a source range the rules before the change already blocked.
A3. [test, confirmed] Row 6: for such a client, an IPv4 public binding is checked from every IPv4 unicast source as well, not only IPv6 bindings.
A4. [test, neg, high] Row 6: a request through an NPM exposure whose client is not a host address gets the concrete client flow it gets today and no hidden-client flow.
A5. [test, high] Row 146: a firewall step of a composite applied or repaired through an NPM exposure is guarded with that request's client and exposure path, at the step's plan and apply, and the snapshot of the step's change holds that client and exposure path for a revert: a step that would drop the requesting client on the exposure's binding is refused, and the composite fails naming the step and the cut flow; a composite applied by a direct panel client guards its firewall step with that client's panel flow.
A6. [test, neg, high] Row 146: the composite's own rows hold neither the client nor the exposure path; a composite resumed after a restart starts no step.
A7. [test, neg, high] Row 131: a Compose validation whose image, container, volume or network check fails (unreachable engine, an HTTP error answer, a transport error, an undecodable answer) writes no image reference, volume name or network name from the configuration to any log line, error or change record; a test with a marker value in an interpolated image reference and volume name checks the log and the returned error.
A8. [test, high] Row 131: that failure reports the object kind, its key in the Compose file and the failure class in the error value, the agent's log line and, for an apply-time failure, the change record; an unreachable engine keeps its current code and text.
A9. [test, high] Row 132: each distinct forward host name is resolved once per forward check, however many proxy hosts and locations name it, and with 20 distinct names that each take the full resolve timeout the check ends within `resolveTimeout` plus one second.
A10. [test, high] Row 141: a failed proxy-host listing makes attach, the undo of a detach, and upgrade fail with `npm.unhealthy`, whose detail, in the error value, the agent's log line and, for an apply-time failure, the change record, tells no answer, an HTTP status (401 included) and an undecodable answer apart.
A11. [test, high] Row 143: a transient resolver failure of a forward host makes attach and upgrade fail with `unavailable` and the detail "forward host <host> could not be resolved: the resolver did not answer; retry" in the error value and the log; a name that does not exist is skipped.
A12. [test, confirmed] Row 144: a forward host with a path is checked by its host part; a forward host holding an nginx variable is not counted, and the threat model says so.
A13. [test, high] Rows 133, 136, 145: an upgrade lists the proxy hosts once, checks the old container's networks plus darkpanel-npm, and is refused with `npm.unhealthy` when NPM's API does not answer, also for a container with no networks, with a detail that names starting NPM or redeploying in the error value, the agent's log line and, for an apply-time failure, the change record; at plan the caller sees `npm.unhealthy` and its title; a missing darkpanel-npm network is a Definite `not_found` refusal naming it.
A14. [test, high] Row 135: the upgrade's `npm.unhealthy` refusal is checked at plan and at apply, and with the NPM container stopped.
A15. [test, high] Row 134: a forward to a subnet's network address, or to an IPv4 subnet's broadcast address below /31, does not refuse an attach, and a forward to the first address of an IPv4 /31 still refuses.
A16. [test, high] Rows 137, 138: the npmapi failure tests cover a short read, and their refused-connection row dials no real port.
A17. [test, high] Row 139: a deploy whose wait for NPM passed its deadline reports "the deploy ran out of time while it waited for Nginx Proxy Manager to start"; a cancelled wait keeps its current detail.
A18. [test, high] Row 140: after `npm.proxyhost.update` of a proxy host whose advanced configuration has an operator `proxy_set_header X-Darkpanel-Client` line (any case or indentation), the written configuration sets the header exactly once, also when the operator line separates its words with a tab or repeated spaces or quotes the header name; a line holding more than the directive (a `}` after the `;`) is kept.
A19. [demo, high] Row 142 and the threat model: `docs/security/threat-model.md` states the hidden-client check, the composite step guard, the forced upgrade risk, the `npm.unhealthy` listing failure, the transient resolver failure, the uncounted variable forward hosts, the upgrade that cannot repair an NPM whose API does not answer, the exposure path captured at apply, the revert guard without a before-state, the unprotected userland-proxy leg, and the kept header lines.
A20. [test, high] Row 481: the harness fetch reports a 404 of a pinned package as a stale pin and any other download failure as a failed download with curl's exit code and the HTTP status.
A21. [test, high] UI-1: the State badge of every row of the connectors table stays on one line at 375x812, 768x1024 and 1440x900.
A22. [test, high] UI-2: with a 64-character tunnel name and a 62-character container name, the connectors table is at least 40rem wide; where its wrapper is narrower the wrapper scrolls and the page does not; each name cell is at least 10 characters of its font wide.
A23. [test, high] UI-3: a 63-character host name in the connectors page's Host choice stays inside the card at 375 px.
A24. [test, high] UI-4: the NPM banner's Open Nginx Proxy Manager link stays inside the banner at 375 px with a 63-character host name, as does the description with a 62-character admin hostname, and the link is underlined at rest.
A25. [test, neg, high] OP-1: at all three viewports, light and dark, the two surfaces show no axe violation (WCAG 2.1 A and AA), no console error, no page-level horizontal scroll, and no named element (State badge, both name cells, the Host label, the banner link and description) extends past its container's right edge (for a table cell the table, otherwise its card or banner).
A26. [test, high] Row 6, on a harness VM: a request from an IPv6 ULA address through the default NPM exposure records one of the host's addresses as its client (Task 1, run early), and a firewall change dropping that ULA source on 443 is refused without the override, while a change made through the same ULA client that blocks nothing on 443 (allowing another port) passes without it.
A27. [test, neg, high] Row 6: for a client that is a host address, on a host with Docker bridges, a tailnet interface, strict reverse-path filtering, and ULA and link-local addresses, a change that blocks no source on the binding passes without the override, in both families.

## Riskiest assumption

That the address NPM names for a client carried by Docker's userland proxy is one of the host's own addresses, which `localAddrs` lists (`internal/modules/firewall/guard/flows.go:125-134`), so `fromHere` identifies every client the guard cannot see. Evidence: Docker documents that the host's IPv6 ports map to an IPv4-only container network through the userland proxy (https://docs.docker.com/engine/network/port-publishing/, read 2026-11-13). docker-proxy runs in the host's network namespace, so its connection to the container leaves from a host address: the darkpanel-npm bridge gateway. The w12a panel re-review (security row 6 note) reached the same reading. Whether NPM's header then names that address is unverified on a live host — the plan's first task verifies it: its harness probe (see Testing) runs on the pushed branch before any guard code is written, and a negative result stops the plan (see Decisions, Early CI). No probe runs on this host, which is live and whose Docker daemon no command of the plan may touch.

## Critic findings

`dforge:judge-opus`, 2026-11-13, report `.dforge/critic-2026-11-13-v1-w12b-open-rows-design-report.md`: 0 Blocker, 1 High, 9 Medium, 4 Nit.

- [High] Riskiest assumption proven only at the end — fixed: Task 1's harness probe runs on the pushed branch before any guard code, and a negative result stops the plan (Decisions, Early CI). A probe on this host is not possible: the host is live and the plan may not touch its Docker daemon.
- [Medium] Hidden-client flow copied only the destination — fixed: it reuses the public leg's flows with a unicast-space flag; A1 asserts equal verdicts on a DNAT binding.
- [Medium] A2, A3 and A12 carry open questions — answered: they are settled at the spec review, before the plan is written, as Auto by default prescribes.
- [Medium] Row 132 leaves lock time unbounded — fixed: concurrent resolution, 8 at a time, under one deadline; A9 bounds it.
- [Medium] Row 131 errors name no object and no code — fixed: the Compose key names the object, and each failure class has a code and detail.
- [Medium] Row 141 reports a credential refusal as "did not answer" — fixed: the detail carries the failure class.
- [Medium] Row 140 prefix match misses tabs and quotes — fixed: token comparison; A18 covers them.
- [Medium] Rows 136 and 145 refuse upgrades with no way out — fixed: the detail names the recovery, the threat model records it, and a missing darkpanel-npm network is a Definite refusal.
- [Medium] Harness ULA step needs lab routing — fixed: Testing states the prefix, the precondition and the curl source.
- [Medium] Row 146 silent on undo and revert — answered (revised after Fable round 1): the dispatcher guards only plan and apply, a firewall step is a composite's revertible last step, and its revert, when it does not restore cleanly, guards with its snapshot's guard context, which now holds the client and path, also after a restart.
- [Nit] No cell-count estimate — fixed: estimate stated, with a near-limit test.
- [Nit] `Via.Public` snapshot can be stale — accepted and recorded in Row 146.
- [Nit] `break-all` breaks words — fixed: `wrap-anywhere`.
- [Nit] Row 144 split and parsing — fixed: `proxy.LiteralAddr` after the split, otherwise resolved as a name.

## Review rulings

`dforge:judge-fable`, round 1, 2026-11-13, report `.dforge/spec-review-2026-11-13-v1-w12b-open-rows-design-1-report.md`: 23 rulings, all applied.

- [High] Row 6 unicast space kept the host's own addresses — removed as one-address holes; strict rp_filter test added.
- [Medium] No statement that an unrelated change passes for a hidden client — A27 added; A26 extended.
- [Medium] Hidden-client flow undefined without a before-state — revert guard must pass every unicast cell; threat-model line 233 widened; test added.
- [Medium] Hidden-client flow deduplicated away — `flowKey` gains the mark.
- [Medium] Userland-proxy-to-NPM leg is unprotected — recorded as a residual; a new security register row opens for it.
- [Medium] Probe had no inconclusive outcome — second precondition, audit source text, three outcomes.
- [Nit] IPv6 leg condition misstated — "neither loopback nor link-local".
- [High] Row 146 restored the client on undo, which no guard reads — plan and apply only; the firewall snapshot carries the context into a revert; A5 and the critic answer rewritten.
- [Medium] A composite step has no override — refusal path defined; A5 rewritten; panel-client case added.
- [Medium] "Never stored" was false — the composite's own rows hold neither; A6 rewritten.
- [Nit] Recovery and release references, stale core comment — corrected.
- [Medium] Plan-time callers see a 5xx title only — rows 131, 141, 136/145 and A8, A10, A13 say where the detail is visible.
- [Medium] Transient resolver failure had no code — Definite `errcode.Unavailable` with a fixed detail; A11 rewritten.
- [Medium] Row 134 differed from Docker's allocator on /31 and /32 — rule follows the allocator; A15 extended.
- [Medium] Row 140 line drop could break nginx configuration — drop only a line holding the directive alone; A18 negative case.
- [Nit] dockerapi path — `internal/platform/dockerapi`.
- [Nit] Container kind missing from row 131 — added.
- [Nit] Row 132 deadline scope — covers resolver lookups; Docker's names read once.
- [Nit] Missing-network refusal had no code — `errcode.NotFound`.
- [Medium] A24 measured the wrong string — 63-character host in the link, 62-character admin hostname.
- [Nit] A22 unmeasurable at 768 px — rewritten to widths and wrapper scroll.
- [Nit] A25 named elements undefined — listed, with their containers.
- [Nit] A19 missing threat-model items — added.
- Round 2, 2026-11-13, report `.dforge/spec-review-2026-11-13-v1-w12b-open-rows-design-2-report.md`: both High answers hold; 4 Nit rulings applied — the revert guards only when it does not restore cleanly; `readStep` dropped (it only reads); `addPolicyCuts` path given in full; the threat-model wording excepts the host's own addresses.
