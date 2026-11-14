# Security re-review of rows 1 and 6 — Fable, commit 10a0973a

Taken from the reviewer's reply: the review-seat hook refused its report-file write.

Commit read: `10a0973a` on `main` (last code commit `d4f6281b`), working tree clean in the session snapshot.

Row 1 is PARTIAL: its fix holds for the paths it was written for, but an `operate` caller still reaches the row's effect by three paths that use only Darkpanel's own actions. One is new; two are recorded as residual risks and under-graded. Row 6 is CLOSED, with one narrow design limit the threat model does not state.

Method limits: no shell, so no `git`, no `go test` and no harness run. Every "fails on revert" below is judged by reading. Statements about nginx and about the Docker Engine's address allocator are inference from their known behaviour, not read in this repository; each is marked.

## Row 1: an `operate` user publishes NPM's API or admin UI

- Verdict: PARTIAL
- Fix:
  - `internal/modules/npm/proxy/upstream.go:42-51` refuses, outside the `npm.admin.expose` parent, the loopback and every port of NPM's names and addresses. `npm.proxyhost.add` and `update` and the undos reach it through `checkForward` (`internal/modules/npm/proxyhost_add.go:398-443`, `proxyhost_edit.go:226-250,329`). The parent comes only from a composite (`internal/core/lifecycle/composite_run.go:554`); composites need `admin` (`internal/core/scope.go:6-15`).
  - `internal/modules/npm/network_claims.go:316-319` runs `forwardGuard` (`:1050-1060`, `forwardTaken` `:1065-1079`) whenever an attach connects NPM: it refuses the static address asked for, or any address the Engine could assign, that a proxy host or location forwards to. The undo of a detach runs it too (`:784`), and the upgrade runs the same check at plan and apply (`instance_upgrade.go:113-146,154,217`).
  - The w12d edits (`network_claims.go:834-836,853-881,897-907`) change only which error a cancelled caller or a late Docker read gets and parse each literal once. Both failures still refuse; no path where they pass the guard.
- Regression tests:
  - `TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses`, `internal/modules/npm/proxy/upstream_test.go:120` — reproduces the original scenario: yes (ports 22 to 65535, names, aliases, IPv6, shortened IPv4). Fails on revert: yes.
  - `TestNwAttachPlanRefusals`, rows at `internal/modules/npm/network_claims_test.go:328-347,362` — reproduces the attach half of the sibling path: yes (static address, Engine-picked address, nginx address form, location, IPv6 beside a static IPv4, a network with no subnet). The proxy host is seeded in the fake NPM, not added through `npm.proxyhost.add`. Fails on revert: yes.
  - `TestW12aAttachChecksTheProxyHostsAgainAtApply`, `network_claims_test.go:1092` — yes; fails on revert: yes.
  - `TestW12aAttachResolvesForwardHostNames`, `network_claims_test.go:1113` — partly: its second half pins that a name with no answer passes, which is the behaviour residual A below uses. Fails on revert: yes for the first half.
  - `TestW12aUndoOfADetachRefusesAForwardedAddress`, `network_claims_test.go:1136` — yes; fails on revert: yes.
  - `TestW12aUpgradeRefusesAForwardedAddress`, `internal/modules/npm/instance_upgrade_test.go:1203` — yes; fails on revert: yes. `TestW12aUpgradeAllowsForwardsNPMCannotTake` (`:1238`) pins that a forwarded address another container holds leaves the upgrade alone, the starting point of residuals B and C.
  - `TestW12dCancelledListingIsNotUnhealthy` and `TestW12dDockerNamesPastTheDeadline`, `internal/modules/npm/w12d_forward_test.go:44,71` — test the error class only; fail on revert of the w12d change: yes.
  - No test covers residual A, B or C.
- Residual: three paths, each high by the rubric (an `operate` caller gets what only `npm.admin.expose` should allow, with no banner). Shared preconditions: `operate` scope, a deployed and reachable NPM, and one container the Docker module does not own (X). Every step below needs `operate` only (`docker.network.create` `internal/modules/docker/network_manage.go:87-156`; `docker.network.connect` with a static address and aliases, `network_connect.go:22-34,208-239`; the npm actions declare no `MinScope`).

  **A. A name whose answer is withdrawn passes the attach guard. New, not recorded.**
  1. Create network N with a subnet. Connect X to N with address A and an alias such as `up.invalid`.
  2. `npm.network.attach` N without an address. No forward exists yet, so the guard passes and NPM gets another address.
  3. `npm.proxyhost.add` with `forwardHost: up.invalid` and port 3000 or 81. `checkForward` takes Docker's answer A (`proxyhost_add.go:375-386,410`); A is not NPM's, so it passes at plan and apply. Darkpanel's "/" location is a static `proxy_pass` (`proxy/location.go:20-34`; `proxy/upstream.go:81-83`), which nginx resolves once, when it loads its configuration (inference).
  4. `docker.network.disconnect` X from N. A is free and no container answers to the name.
  5. `npm.network.detach` the claim, so NPM leaves N (`network_claims.go:523,569-586`). Nothing reloads nginx.
  6. `npm.network.attach` N with `address: A`. `forwardTargets` reads Docker's names only on NPM's current networks (`proxyhost_add.go:324-371`), finds none, asks the host's resolver, gets "no such name" and skips the forward (`network_claims.go:962`). No target is left, and NPM takes A.
  7. nginx still forwards the hostname to A:3000, now NPM's own API, or to A:81, its admin UI. A later reload fails on the unresolvable name and leaves the old workers running (inference).

  The skip rests on the comment at `network_claims.go:823-826` and on line 406 of the w12a plan: a name with no answer "can reach NPM only after it is re-pointed", so it was filed under the accepted DNS risk. That premise is wrong: the address nginx holds is the one the name had, and here NPM moves onto it. The path needs no DNS name the caller controls, and no race. Confidence: plausible by reading, not run; it depends on the nginx behaviour above.

  **B. Undoing an upgrade recreates NPM with no forward check. Recorded in the threat model (line 292) and ruled "record, not fix" (W12a-R1); no register row; an `operate` caller can drive it deterministically.**
  1. X is on N at the lowest free address A; NPM joins N after it with an Engine-picked address.
  2. `npm.proxyhost.add` forwarding to the literal A:3000. X holds A, so the check passes.
  3. `npm.instance.upgrade` (it runs with an unchanged image too). `forwardCheck` passes because X holds A (`network_claims.go:1005-1009`). The archives now hold the proxy host.
  4. `docker.network.disconnect` X from N. This takes the docker lock, so the undo window of the npm lock stays open.
  5. Undo the upgrade change. Undo needs the scope of the change's action, `operate` (`internal/controller/api/changes.go:100-106`, `actions.go:421-426`). `Inverse` runs `rollback`, then `restore`, which recreates the original container and reconnects N with no static address and no check (`upgrade_recover.go:28-36`, `instance_upgrade.go:661-711`, comment at `:112`). The Engine hands out the lowest free address, A (inference about the Engine's allocator).
  6. NPM is at A and the restored proxy host forwards to its API.

  The ruling's reason, that a refusing rollback would leave NPM down, covers the rollback of a failing upgrade. It does not cover a requested undo of a finished upgrade: NPM is up, and refusing leaves it up.

  **C. Freeing the forwarded address during an upgrade. Register row 142, recorded low, state verify, closed by a threat-model sentence (line 295).** As B, steps 1 to 3, but disconnect or stop X after `forwardCheck` (`instance_upgrade.go:217`) and before the new container starts (`:336`). The window spans NPM's stop, both archives and the recreate, several seconds, and the docker lock does not wait for the npm lock. The recorded severity is wrong by this rubric: the row describes an `operate` caller forcing an `admin`-only effect, which is high, not low.

  The accepted DNS risk (a name the caller controls, re-pointed after apply; Known residual risks, `forward host`) also still reaches the effect; not counted against the row.

  Neither B nor C is in the list the threat model calls owner-accepted (lines 332-338); both sit under Exposure, Residual risk. If the owner accepts them at high, that list should say so. Otherwise the smallest changes: run `forwardCheck` in the `Inverse` of a finished upgrade; re-check after the recreate or hold the docker lock across it; and refuse a join while a forward host is a name with no answer.

- Threat model, row 1 parts:
  - Line 281, "a name with no answer is not counted": true, but nothing under Residual risk says that such a name can still be forwarding to the address it had, which is residual A.
  - Lines 292 and 295: true as far as they go. Line 292 does not say an `operate` caller can drive the undo case.
  - Line 280, "port 81 stays allowed inside that composite": the code allows every port of NPM and the loopback under that parent (`proxy/upstream.go:42`). Harmless today, because the composite sends a fixed `127.0.0.1:81` (`composite_admin.go:74-84`). Already noted in `v1-security-rereview.md`.

## Row 6: the firewall guard and the browser-to-NPM leg

- Verdict: CLOSED
- Fix:
  - The exposure's record carries the published binding, `[::]:443` for NPM (`internal/modules/panel/listener.go:167-179`), into every request's `ProxyPath` (`internal/controller/api/runtime.go:30-33`) and into the guard context together with the client NPM named (`internal/controller/api/server.go:224-227`, `internal/controller/dispatch/guard.go:48-58`). A caller cannot supply the context: `withGuard` drops any `guard` member of the input (`dispatch/guard.go:88-108`). A composite's steps get both back (`internal/core/lifecycle/composite_run.go:68-85`).
  - `viaFlows` (`internal/modules/firewall/guard/flows.go:269-305`) adds the public leg from every internet cell (`npmPublicFlows`, `:408-438`) and refuses an NPM exposure with no binding as unknown (`:285-287`).
  - `npmClientFlows` (`flows.go:448-472`) adds the concrete client, whatever its range, to each binding of its family, not compared with the rules before the change. A client that is a host address or loopback is treated as hidden (`:452-457`): each binding gets the public leg's flows over the unicast space, private, CGNAT, tailnet, ULA and link-local sources included (`internal/modules/firewall/eval/cells.go:41-53,70-82`; `guard/cells.go:64-69`), and a cell fails only when the change itself blocks it (`guard/cells.go:296-325`).
  - `relayFlows` (`flows.go:314-402`) adds the leg from Docker's userland proxy to NPM's container and fails closed when the holder or its command line cannot be read.
  - Every mutating firewall action runs this guard at plan and again at apply (`internal/modules/firewall/handler.go:59-73`, `plan.go:136-158`).
  - `internal/modules/firewall/exposure/verdict.go` is not on the guard's path: only `exposure_resource.go` imports the package. The w12d change there does not touch this row.
- Regression tests:
  - `TestV1r19RuntimeHandlerCarriesThePublicBinding`, `internal/controller/api/runtime_public_test.go:12` — reproduces the cause (a path with no bindings): yes; fails on revert: yes.
  - `TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding`, `internal/modules/firewall/guard/exposure_test.go:32` — the fail-closed case: yes; fails on revert: yes.
  - `TestW11b09BlockedPublicBindingIsRefused`, `exposure_test.go:58` — reproduces the original scenario (a ruleset that drops the forwarded 443, both binding forms): yes; fails on revert: yes.
  - `TestW11b09IPv6LegOfADualStackBinding`, `exposure_test.go:77` — yes; fails on revert: yes.
  - `TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused`, `exposure_test.go:105` — reproduces the f7a6432 residual (RFC 1918, CGNAT and ULA clients): yes; fails on revert: yes.
  - `TestW12bHiddenNPMClient`, `guard/w12b_hidden_client_test.go:56` — reproduces the 4172841 residual (binding `[::]:443`, client the bridge gateway, a new drop of a ULA range): yes; fails on revert: yes. The w12d rows at `:68-71` use a concrete host-address binding, which production never records (`listener.go:170`): partly; the refusal row fails on revert: yes.
  - `TestW12bHiddenClientOnABusyHost` (`:113`) and `TestW12bHiddenClientOverTheCellLimitIsUnknown` (`:142`) — the negative case and the fail-closed limit: yes; fail on revert: yes.
  - `TestW12cRelayGuard`, `guard/w12c_relay_test.go:47` — the relay leg: yes; fails on revert: yes.
  - `TestM6Panel/ipv6-client-guard`, `test/harness/m6_panel_test.go:1040` — reproduces the 4172841 scenario end to end on a VM: yes by reading; not run. The register records it green in harness runs 43562660200 and 43596603456, both before w12d.
- Sibling paths tried, all refused or failing closed by reading: an RFC 1918, CGNAT or tailnet IPv4 client (concrete flow on every host address and eligible interface); an IPv6 client of any range through the userland proxy (hidden-client flows in both families, plus the relay); a browser on the host; a client named by another bridge's gateway (any host address is hidden); a missing binding; an incomplete topology; a composite's firewall step; a plan made through one route and applied through another (both contexts are merged, `flows.go:112-120`); the container rules, defaults, Docker integration and snapshot actions (all go through `defineMutation`).
- Residual: one narrow path, graded low.
  - On a host whose only IPv6 addresses are link-local, a `[::]:443` binding gets no IPv6 flow at all, public or hidden-client (`flows.go:418-425`). A client that reaches NPM over the host's link-local address is carried by the userland proxy and named by a host address; a change that removes the IPv6 allowance of 443 then passes without the override.
  - Preconditions: no routable IPv6 address on the host, a UFW rule that admits 443 over IPv6, and a client that sends the public hostname to a scoped link-local address (`curl --resolve`; a browser cannot write the zone in a URL).
  - Why low: the w12b design states this condition (spec line 54), so it is a reviewed limit, like the two the threat model already lists (other clients' private ranges, line 234; a disabled userland proxy, line 235); and the unconfirmed change reverts by itself. By the letter of the rubric the effect is in the high class; if the owner does not regard it as an accepted design limit, it needs a ruling.
- Threat model, row 6 parts:
  - Line 215, "an NPM exposure recorded on `[::]:443` is checked on both families", and lines 215 and 229, "every unicast source of each binding's family": not true on a host with only link-local IPv6 addresses, as above. One sentence would fix it.
  - Lines 229-235 otherwise match the code read.
  - Unknown: whether a host with the userland proxy disabled shows no listener on `[::]:443` (no relay flow, as line 235 says) or one held by `dockerd` (relay flow unknown, every guarded change refused). Reading `ss -ltnp` on such a host would settle it.

## Other observations (ungraded)

- `internal/modules/tailscale/linkguard.go:178-185`: for a request through an exposure the Tailscale link guard adds only the proxy-to-listener flow, and its comment says the context's client "is then the proxy's address". Since `clientPeer` (`internal/controller/api/server.go:224-227`) the client is the address NPM named. A tailnet client of an NPM exposure therefore gets no flow of its own in that guard, so a change that takes Tailscale down is not refused for it. On the controller's host the default listener on the Tailscale address already makes that guard refuse such a change, which masks this. Not recorded in either register.
- `internal/modules/npm/proxyhost_edit.go:241-250`: the undo's location check does not cut the forward host at its first `/`, as `forwardTargets` does (`network_claims.go:854`). A location such as `<NPM address>/x` is read as a name, fails to resolve, and is restored unchecked. Only an NPM administrator can write one. Related: rows 26, 85 and 144.
- Register row 160 records gates 1 to 5 at `d4f6281b` and no harness run, although w12d changed `internal/modules/firewall/exposure/verdict.go`, which the harness's scan comparison exercises.
- Row 1's note says both panel reviewers closed it at 4172841, while row 6's note says both rated PARTIAL in the same pass; the 2026-11-14 audit entry already says no tracked record of that pass exists.
- `flows.go:368`: a userland proxy whose target is an IPv6 container address gives no relay flow. The default deployment has no such target; a hand-enabled IPv6 on `darkpanel-npm` would leave that leg unchecked.
- A client that reaches NPM through another container on the host (a VPN sidecar) is named by that container's address. The guard then protects the container-to-NPM flow, not the client's own leg into that container. Inference; the guard only ever knows the last network hop.
