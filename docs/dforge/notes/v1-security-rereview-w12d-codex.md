# Security re-review of rows 1 and 6 — Codex gpt-6-astra, commit 10a0973a

Commit reviewed: `10a0973ae593e592342d49028db0e545f5c59923` (`main`; last code commit `d4f6281b`).

Read-only source review. No files were changed, and no tests or host-management commands were run. Reproduction and revert-sensitivity assessments below are based on control flow and test assertions, not executed mutations.

## Row 1 — NPM administrative upstreams

- **Verdict: PARTIAL**, using the brief’s definition that the administrative-exposure effect remains reachable. The original direct-upstream bypass and the sequential “forward to free A, then attach at A” bypass are blocked. The remaining paths below are already documented risks; they are not new failures introduced by w12d.

- **Fix:**
  - [proxy/upstream.go:38](/root/repositories/darkpanel/internal/modules/npm/proxy/upstream.go:38) reserves every port of NPM’s names and addresses outside `npm.admin.expose`. This covers aliases, network-qualified names, IPv6, mapped addresses and alternate numeric IPv4 forms.
  - [proxyhost_add.go:398](/root/repositories/darkpanel/internal/modules/npm/proxyhost_add.go:398) checks the current reserved set and every resolved answer. Add, update and checked restoration paths use this validation.
  - [network_claims.go:317](/root/repositories/darkpanel/internal/modules/npm/network_claims.go:317) checks existing forwards before an attachment. Apply repeats that check at line 365. [network_claims.go:784](/root/repositories/darkpanel/internal/modules/npm/network_claims.go:784) checks before undoing a detach.
  - [network_claims.go:827](/root/repositories/darkpanel/internal/modules/npm/network_claims.go:827) collects proxy-host and custom-location targets; [network_claims.go:1065](/root/repositories/darkpanel/internal/modules/npm/network_claims.go:1065) rejects requested static addresses and addresses the Engine could allocate, without restricting the check to particular ports.
  - [instance_upgrade.go:113](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade.go:113) checks all joined networks and `darkpanel-npm`, including when the old container has left it. Plan and apply both invoke it.
  - w12d’s cancellation, shared-deadline and single-parse changes retain refusal on unresolved checks.

- **Regression tests:** “Fails on revert” refers to reverting the corresponding protection or correction.

| Test and location | Reproduces scenario? | Fails on revert? |
|---|---|---|
| `TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses`, [upstream_test.go:120](/root/repositories/darkpanel/internal/modules/npm/proxy/upstream_test.go:120) | Yes, at helper level: ports 22, 80, 81, 443, 3000, 8080 and 65535 across NPM identities. | Yes |
| `TestV1r09AddRefusesANameThatResolvesToAReservedAddress`, [proxyhost_resolve_test.go:35](/root/repositories/darkpanel/internal/modules/npm/proxyhost_resolve_test.go:35) | Partly: plan/apply DNS answers, including NPM’s API address; not NPM’s later resolution. | Yes |
| `TestV1r09AddResolvesAgainAtApply`, [proxyhost_resolve_test.go:105](/root/repositories/darkpanel/internal/modules/npm/proxyhost_resolve_test.go:105) | Yes, for a name becoming reserved between plan and apply. | Yes |
| `TestV1r09UpdateResolvesForwardHosts`, [proxyhost_resolve_test.go:183](/root/repositories/darkpanel/internal/modules/npm/proxyhost_resolve_test.go:183) | Yes, for the update path’s reserved DNS answer. | Yes |
| `TestV1r09UndoesCheckTheForwardHostTheyRestore`, [proxyhost_resolve_test.go:380](/root/repositories/darkpanel/internal/modules/npm/proxyhost_resolve_test.go:380) | Partly: update/remove inverses with a changed main forward hostname. | Yes |
| `TestNwAttachPlanRefusals`, [network_claims_test.go:295](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:295) | Yes: static A, dynamic A, numeric forms, custom locations, concurrent IPv6 allocation, missing subnet data and failed listings. | Yes |
| `TestW12aAttachChecksTheProxyHostsAgainAtApply`, [network_claims_test.go:1092](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:1092) | Yes: adds the dangerous forward after planning and asserts no connection, claim or effect. | Yes |
| `TestW12aAttachResolvesForwardHostNames`, [network_claims_test.go:1113](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:1113) | Partly: checks all supplied DNS answers and NXDOMAIN handling, without exercising NPM’s resolver. | Yes |
| `TestW12aUndoOfADetachRefusesAForwardedAddress`, [network_claims_test.go:1136](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:1136) | Yes: detach, add dangerous forward, attempt inverse; verifies no reconnection. | Yes |
| `TestW12aUpgradeRefusesAForwardedAddress`, [instance_upgrade_test.go:1203](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade_test.go:1203) | Yes: free, released and static addresses; checks plan/apply refusal before mutation. | Yes |
| `TestW12aUpgradeAllowsForwardsNPMCannotTake`, [instance_upgrade_test.go:1238](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade_test.go:1238) | No: permitted-case control. It does not release a held address during the upgrade. | No, if the guard is removed |
| `TestW12aUpgradeNeedsNPMsAPI`, [instance_upgrade_test.go:1256](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade_test.go:1256) | Partly: running/stopped/disconnected fixtures with injected listing failures; checks plan and apply. | Yes |
| `TestW12bUpgradeForwardCheck`, [instance_upgrade_test.go:1297](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade_test.go:1297) | Yes for its sibling cases: absent attachment, missing network, refused listing and unresolved name. | Yes |
| `TestW12bForwardTargets`, [network_claims_test.go:1173](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:1173) | Partly: lookup sharing, deadline/refusal handling, variable/NXDOMAIN exclusions and detach-undo listing failure. | Yes, for the corresponding changes |
| `TestW12bNetworkAssignable`, [network_claims_test.go:1321](/root/repositories/darkpanel/internal/modules/npm/network_claims_test.go:1321) | No administrative-exposure reproduction; checks allocation classification and over-refusal. | Yes, for reserved-address classification |
| `TestW12dCancelledListingIsNotUnhealthy`, [w12d_forward_test.go:44](/root/repositories/darkpanel/internal/modules/npm/w12d_forward_test.go:44) | No exposure reproduction; yes for cancellation misclassification. | Yes |
| `TestW12dDockerNamesPastTheDeadline`, [w12d_forward_test.go:71](/root/repositories/darkpanel/internal/modules/npm/w12d_forward_test.go:71) | No exposure reproduction; yes for the deadline error contract. | Yes |

- **Residual:**
  1. **High — DNS resolution can still produce the administrative-exposure effect.** An `operate` caller controlling a hostname can provide permitted answers during Darkpanel’s checks and a reserved answer when NPM resolves it. The checked addresses are not pinned: [proxyhost_add.go:165](/root/repositories/darkpanel/internal/modules/npm/proxyhost_add.go:165) retains the original hostname, and line 586 submits that body. Preconditions are control of the name’s answers and NPM resolving the reserved answer. This is already **security register row 2**, explicitly accepted and documented at [threat-model.md:290](/root/repositories/darkpanel/docs/security/threat-model.md:290). Its recorded high severity is correct; its `done` state represents risk acceptance, not elimination of the effect.

  2. **High, conditional on Engine allocation — an address can become available after the guard checks it.** [network_claims.go:1005](/root/repositories/darkpanel/internal/modules/npm/network_claims.go:1005) excludes addresses another container holds. Upgrade checks at [instance_upgrade.go:217](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade.go:217), then pulls/archives before recreating and reconnecting NPM at lines 329–334. An `operate` caller can arrange a forward to another container’s address A, release A during that interval, and obtain the exposure if the Engine assigns A to NPM. No post-allocation check closes that interval. This is already **security register row 142**, and [threat-model.md:295](/root/repositories/darkpanel/docs/security/threat-model.md:295) now acknowledges that an `operate` caller can force the release. The row’s low grade is reasonable for its documentation correction, but would understate the underlying exposure effect under this brief’s rubric. Its documentation-verification state does not establish security closure. Engine allocation was not reproduced live.

The threat model accurately discloses these limits, including unchecked upgrade rollback/undo and address changes. Its reservation claims must therefore be read as checks at the listed operations, not a continuous guarantee.

## Row 6 — Browser-to-NPM firewall protection

- **Verdict: PARTIAL.** The original missing-binding case, private/CGNAT/ULA requesting-client cases, and the reported hidden-ULA-client case are addressed. A link-local-only IPv6 host still omits the requesting client’s IPv6 ingress leg.

- **Fix:**
  - [panel/listener.go:170](/root/repositories/darkpanel/internal/modules/panel/listener.go:170) records `[::]:443`. [api/runtime.go:30](/root/repositories/darkpanel/internal/controller/api/runtime.go:30) carries it into `ProxyPath`, and [dispatch/guard.go:52](/root/repositories/darkpanel/internal/controller/dispatch/guard.go:52) preserves it for the guard.
  - [guard/flows.go:285](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:285) makes missing NPM bindings unknown, which refuses the change.
  - [guard/flows.go:448](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:448) checks a non-host requesting client directly. When `$realip_remote_addr` identifies a host address, it adds unicast-source flows for the recorded bindings.
  - [guard/cells.go:65](/root/repositories/darkpanel/internal/modules/firewall/guard/cells.go:65) includes private, CGNAT, ULA, tailnet and link-local source ranges for those hidden-client flows. Line 298 compares them with the before-state.
  - [guard/flows.go:314](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:314) separately protects the IPv4 OUTPUT relay discovered from the IPv6 `docker-proxy` listener.
  - [composite_run.go:70](/root/repositories/darkpanel/internal/core/lifecycle/composite_run.go:70) captures and restores requester context for composite children.

- **Regression tests:**

| Test and location | Reproduces scenario? | Fails on revert? |
|---|---|---|
| `TestV1r19RuntimeHandlerCarriesThePublicBinding`, [runtime_public_test.go:12](/root/repositories/darkpanel/internal/controller/api/runtime_public_test.go:12) | Partly: proves binding propagation, not firewall evaluation. | Yes |
| `TestV1r19NPMExposureWithoutPublicBindingIsUnknown`, [exposure_test.go:20](/root/repositories/darkpanel/internal/modules/firewall/guard/exposure_test.go:20) | Yes, at flow-construction level. | Yes |
| `TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding`, [exposure_test.go:32](/root/repositories/darkpanel/internal/modules/firewall/guard/exposure_test.go:32) | Yes: asserts the missing-binding refusal. | Yes |
| `TestW11b09BlockedPublicBindingIsRefused`, [exposure_test.go:58](/root/repositories/darkpanel/internal/modules/firewall/guard/exposure_test.go:58) | Yes: real evaluator with DNAT and a forwarded HTTPS drop. | Yes |
| `TestW11b09IPv6LegOfADualStackBinding`, [exposure_test.go:77](/root/repositories/darkpanel/internal/modules/firewall/guard/exposure_test.go:77) | Yes, on its host with a non-link-local IPv6 address. | Yes |
| `TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused`, [exposure_test.go:105](/root/repositories/darkpanel/internal/modules/firewall/guard/exposure_test.go:105) | Yes: concrete RFC 1918, CGNAT and tailnet-ULA clients. | Yes |
| `TestW12bHiddenNPMClient`, [w12b_hidden_client_test.go:56](/root/repositories/darkpanel/internal/modules/firewall/guard/w12b_hidden_client_test.go:56) | Yes: gateway-reported client, ULA/private drops, before-state comparison, and w12d’s concrete host-address binding. | Yes |
| `TestW12bHiddenClientOnABusyHost`, [w12b_hidden_client_test.go:113](/root/repositories/darkpanel/internal/modules/firewall/guard/w12b_hidden_client_test.go:113) | Partly: includes link-local addresses, but also non-link-local IPv6; it tests a ULA drop. | Yes, for hidden-client protection |
| `TestW12bHiddenClientOverTheCellLimitIsUnknown`, [w12b_hidden_client_test.go:142](/root/repositories/darkpanel/internal/modules/firewall/guard/w12b_hidden_client_test.go:142) | Yes, for refusing an unevaluated hidden-client flow. | Yes |
| `TestW12bUnicastCellsLeaveOutHostAndSpecialAddresses`, [cells_test.go:184](/root/repositories/darkpanel/internal/modules/firewall/eval/cells_test.go:184) | Partly: proves source-space membership, including link-local; not that an IPv6 flow is created. | Yes |
| `TestW12cRelayGuard`, [w12c_relay_test.go:47](/root/repositories/darkpanel/internal/modules/firewall/guard/w12c_relay_test.go:47) | Yes for relay OUTPUT drops, explicit allowance, before-state comparison and unreadable targets. | Yes |
| `TestW12cRelayFlowsOnlyWhereTheyApply`, [w12c_relay_test.go:95](/root/repositories/darkpanel/internal/modules/firewall/guard/w12c_relay_test.go:95) | Partly: construction and applicability, including mapped bindings and retired exposures. | Yes |
| `TestW12cRelayFlowWithoutCmdlines`, [w12c_relay_test.go:146](/root/repositories/darkpanel/internal/modules/firewall/guard/w12c_relay_test.go:146) | Yes for the unknown-relay refusal input. | Yes |
| `TestA5_ACompositeAppliedThroughAnExposureGuardsEachStepWithTheRequester`, [v1_w12b_open_rows_acceptance_test.go:122](/root/repositories/darkpanel/internal/core/lifecycle/v1_w12b_open_rows_acceptance_test.go:122) | Partly: child plan/apply context propagation, using recording children. | Yes |
| `TestA5_ARepairThatStartsARunCarriesTheRepairer`, [v1_w12b_open_rows_acceptance_test.go:131](/root/repositories/darkpanel/internal/core/lifecycle/v1_w12b_open_rows_acceptance_test.go:131) | Partly: repair requester propagation. | Yes |
| `TestM6Panel/ipv6-client-guard`, [m6_panel_test.go:1040](/root/repositories/darkpanel/test/harness/m6_panel_test.go:1040) | Yes: actual ULA exposure requests, deletion of their allowance, harmless-change control and relay-output refusal. No link-local-only host. | Yes |

- **Residual: High — a link-local-only IPv6 ingress path is omitted.**

  Preconditions: NPM has its normal `[::]:443` userland-proxy listener; the controller host has IPv4 connectivity but its only non-loopback IPv6 addresses are link-local; a same-link authenticated operator reaches the panel exposure over that IPv6 listener. NPM reports the host’s IPv4 bridge address through `$realip_remote_addr`.

  The source-level reproduction is:

  1. [guard/flows.go:452](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:452) recognizes the reported bridge address as a hidden client.
  2. Both public and hidden-client construction call `npmPublicFlows`. For `[::]`, [guard/flows.go:420](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:420) creates the IPv6 leg only if a local IPv6 address is neither loopback nor link-local. This host gets **no IPv6 ingress flow**.
  3. Plan a new IPv6 INPUT deny of TCP 443, leaving IPv4 rules and other protected flows unchanged. The deny cuts the operator’s connection but matches no constructed exposure ingress probe.
  4. The relay flow at [guard/flows.go:369](/root/repositories/darkpanel/internal/modules/firewall/guard/flows.go:369) checks IPv4 OUTPUT to the container. It cannot detect that IPv6 INPUT deny. The guard can therefore pass without the override.

  This is a code-level reproduction, not a live Docker demonstration. None of the inspected regressions supplies this host topology.

  Register searches found related **security rows 27 and 28**, whose recorded scope deliberately excludes link-local wildcard destinations. Their `done` states do not establish protection for this case. The specific omission of the whole IPv6 ingress family is not separately recorded; **row 6 should remain open/high**. The w12b design explicitly retained this predicate, so this is a remaining design gap rather than a w12d regression.

  [Threat-model.md:215](/root/repositories/darkpanel/docs/security/threat-model.md:215) overstates the hidden-client guarantee by saying every unicast source of each binding’s family is checked without stating this whole-family omission. Its non-link-local destination qualification does not explain that a working link-local-only IPv6 exposure receives no ingress check at all.

## Other observations (ungraded)

- **Accepted risk versus closure:** Row 1’s original direct and sequential attach scenarios are fixed. Its PARTIAL verdict here follows the brief’s effect-based definition and includes documented accepted risks. It should not be mistaken for a newly discovered failure of `forwardGuard`.

- **Historical record discrepancy:** Security register row 2 says both earlier reviewers closed the fix. The tracked Fable and Codex reports both label row 2 **PARTIAL**; the synthesis records closure by acceptance. The accepted disposition can remain, but the description of the reviewers’ verdicts is inaccurate.

- **Upgrade rollback/undo remains explicitly unchecked:** [instance_upgrade.go:112](/root/repositories/darkpanel/internal/modules/npm/instance_upgrade.go:112) and [threat-model.md:292](/root/repositories/darkpanel/docs/security/threat-model.md:292) agree on this limitation. The upgrade refusal tests do not prove reservation enforcement during restoration.

- **w12d’s exposure-view change is separate from lockout protection:** [exposure/verdict.go:335](/root/repositories/darkpanel/internal/modules/firewall/exposure/verdict.go:335) excludes bridge-member interfaces from exposure-view probes. `TestExEvaluate`’s concrete-address veth case at [verdict_test.go:189](/root/repositories/darkpanel/internal/modules/firewall/exposure/verdict_test.go:189) exercises that correction and would fail if reverted. It does not address row 6’s missing IPv6 ingress flow.

- The checkout remained unchanged at the reviewed commit.