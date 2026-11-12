# Security re-review — Codex gpt-6-astra, commit f7a6432

## Verdicts

### Row 1: NPM’s own ports as ordinary upstreams

- Verdict: CLOSED
- Fix: [upstream.go:42](internal/modules/npm/proxy/upstream.go:42) reserves every port on NPM’s names and addresses outside the admin-exposure parent. Both add and update invoke this check through [checkForward](internal/modules/npm/proxyhost_add.go:368).
- Regression test: `TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses` in [upstream_test.go:120](internal/modules/npm/proxy/upstream_test.go:120) — reproduces the scenario: yes. It includes port 3000, IPv4, IPv6 and aliases; reverting the port-independent restriction would fail its assertions.
- Residual: none for the direct names-and-addresses scenario. The DNS bypass is assessed separately in row 2.

### Row 2: DNS bypass of reserved upstreams

- Verdict: PARTIAL
- Fix: [proxyhost_add.go:368](internal/modules/npm/proxyhost_add.go:368) resolves names and checks every answer. Answers enter the plan hash; update performs the same validation at [proxyhost_edit.go:311](internal/modules/npm/proxyhost_edit.go:311).
- Regression test: `TestV1r09AddRefusesANameThatResolvesToAReservedAddress` and `TestV1r09AddResolvesAgainAtApply` in [proxyhost_resolve_test.go:35](internal/modules/npm/proxyhost_resolve_test.go:35) — reproduces the scenario: partly. Their mocked lookups catch reserved answers and changes before apply, and would fail if those checks were reverted. They do not exercise NPM’s subsequent resolution.
- Residual: **high** — validated addresses are not pinned. The submitted body still contains the original hostname, including its custom location, at [proxyhost_add.go:161](internal/modules/npm/proxyhost_add.go:161), and apply sends it unchanged at [proxyhost_add.go:556](internal/modules/npm/proxyhost_add.go:556). An attacker-controlled name can answer safely during validation and resolve to loopback, NPM or the panel gateway when NPM resolves it. Rechecking and hashing answers do not close that gap.

### Row 3: First CMD argument disclosed by container inspection

- Verdict: CLOSED
- Fix: [inspect.go:119](internal/modules/docker/inspect.go:119) masks every CMD element; [inspect.go:179](internal/modules/docker/inspect.go:179) uses that function for the inspection response.
- Regression test: `TestV1r13InspectActionShowsNoCommandElement` in [inspect_cmd_test.go:58](internal/modules/docker/inspect_cmd_test.go:58) — reproduces the scenario: yes. It supplies an entrypoint with a secret as CMD element zero and checks the action’s serialized response. Restoring the old masking would fail.
- Residual: none.

### Row 4: NPM advanced configuration disclosed through effects

- Verdict: CLOSED
- Fix: [proxyhost_edit.go:71](internal/modules/npm/proxyhost_edit.go:71) fingerprints host and custom-location configuration using the keyed helper at [effects.go:154](internal/modules/npm/effects.go:154). Update and removal pass their restoration bodies as effect secrets at [proxyhost_edit.go:388](internal/modules/npm/proxyhost_edit.go:388) and [proxyhost_edit.go:582](internal/modules/npm/proxyhost_edit.go:582). The journal encrypts those secrets at [lifecycle/effects.go:109](internal/core/lifecycle/effects.go:109).
- Regression test: `TestV1r10UpdateEffectsHoldFingerprintsAndTheSealedBody` and `TestV1r10RemoveEffectsHoldFingerprintsAndTheSealedBody` in [proxyhost_effects_test.go:109](internal/modules/npm/proxyhost_effects_test.go:109) — reproduces the scenario: yes. They seed credentials in both configuration levels and inspect plans, effects, observations and restoration. Reverting the protection would fail.
- Residual: none.

### Row 5: Docker label values in plans, inputs and effects

- Verdict: CLOSED
- Fix: Plan diffs show keys only at [network_manage.go:44](internal/modules/docker/network_manage.go:44); both input types mark labels secret at [wire.go:276](internal/modules/docker/wire.go:276). Effect documents use keyed fingerprints at [dstate/object.go:66](internal/modules/docker/dstate/object.go:66). Recorded inputs pass through redaction at [lifecycle/apply.go:245](internal/core/lifecycle/apply.go:245).
- Regression test: `TestV1r14PlanDiffsShowLabelKeysOnly`, `TestV1r14LabelInputsAreRedacted` and `TestV1r14EffectsHoldKeyedFingerprints` in [label_secrets_test.go:62](internal/modules/docker/label_secrets_test.go:62) — reproduces the scenario: yes. Together they cover both object types, input redaction, create/remove effects and guessable hashes. Reverting the corresponding protections would fail.
- Residual: none.

### Row 6: Missing browser-to-NPM firewall protection

- Verdict: PARTIAL
- Fix: The listener records a dual-stack binding at [panel/listener.go:167](internal/modules/panel/listener.go:167), runtime requests carry it at [api/runtime.go:30](internal/controller/api/runtime.go:30), and [guard/flows.go:271](internal/modules/firewall/guard/flows.go:271) builds public flows or refuses missing bindings.
- Regression test: `TestW11b09BlockedPublicBindingIsRefused` and `TestW11b09IPv6LegOfADualStackBinding` in [exposure_test.go:58](internal/modules/firewall/guard/exposure_test.go:58) — reproduces the scenario: yes. They exercise public-binding drops with the real evaluator, including IPv6; removing the public-flow protection would fail. They do not cover private-source clients.
- Residual: **high** — an actual client using the NPM exposure from a private or tailnet address remains unprotected on this leg. [panelFlows](internal/modules/firewall/guard/flows.go:240) skips the concrete client whenever `Via` is present; [npmPublicFlows](internal/modules/firewall/guard/flows.go:303) substitutes public-source partitions. Those partitions exclude RFC 1918, CGNAT and IPv6 outside `2000::/3` at [eval/cells.go:17](internal/modules/firewall/eval/cells.go:17). A source-specific deny of that client’s HTTPS access can therefore pass while public sources and the proxy-to-controller leg remain allowed.

### Row 7: DERP service files redirect root reads

- Verdict: CLOSED
- Fix: Production uses `certFileReader` at [derp/deps.go:128](internal/modules/derp/deps.go:128). Its parent-relative traversal refuses links at or beneath the certificate directory at [deps.go:295](internal/modules/derp/deps.go:295). [files.go:229](internal/platform/files.go:229) opens beneath that descriptor, and [files.go:251](internal/platform/files.go:251) rejects special files and bounds reads.
- Regression test: `TestV1r15ProductionCertReadRefusesPlantedFiles` in [cert_files_test.go:39](internal/modules/derp/cert_files_test.go:39), `TestW11c05CertFileReaderRefusesALinkAtTheDirectory` in [cert_reader_test.go:50](internal/modules/derp/cert_reader_test.go:50), and `TestW11c05ReadRegularFileBeneath` in [files_beneath_test.go:13](internal/platform/files_beneath_test.go:13) — reproduces the scenario: yes. They cover file/directory links, FIFO rejection and size limits; reverting the protection would fail.
- Residual: none.

### Row 8: Global username login lockout

- Verdict: CLOSED
- Fix: [ratelimit.go:93](internal/controller/auth/ratelimit.go:93) keys username failures by username and normalized source; both checks and failure recording use that pair.
- Regression test: `TestV1r17LoginLockIsPerSource` in [loginlock_test.go:56](internal/controller/auth/loginlock_test.go:56) — reproduces the scenario: yes. Five failures block the attacking source while a valid login from another source succeeds. Restoring the global username key would fail.
- Residual: none for the cross-source username-lockout scenario.

### Row 9: Private-network DERP probes and raw TLS errors

- Verdict: CLOSED
- Fix: [node_probe.go:61](internal/modules/derp/node_probe.go:61) requires `operate`; [node_probe.go:147](internal/modules/derp/node_probe.go:147) rejects private and special-purpose addresses, including resolved answers. The transport connects to the selected literal at [deps.go:207](internal/modules/derp/deps.go:207), and responses use categorical errors at [node_probe.go:335](internal/modules/derp/node_probe.go:335).
- Regression test: `TestV1r16ProbeRefusesPrivateLiterals`, `TestV1r16ProbeResolveDropsPrivateAnswers` and `TestV1r16ProbeErrorsUseTheFixedVocabulary` in [node_probe_private_test.go:85](internal/modules/derp/node_probe_private_test.go:85) — reproduces the scenario: yes. They cover mapped forms, DNS answers and certificate-name disclosure; reverting the corresponding protections would fail.
- Residual: none.

### Row 10: NPM responses reflect secrets into errors and logs

- Verdict: PARTIAL
- Fix: [npmapi/client.go:244](internal/modules/npm/npmapi/client.go:244) discards error-message text and retains only status and an allowlisted numeric code.
- Regression test: `TestV1r11LoginFailureCarriesNoNPMText` and `TestV1r11MutationFailureCarriesNoNPMText` in [answer_text_test.go:45](internal/modules/npm/answer_text_test.go:45) — reproduces the scenario: yes. They reflect submitted credentials through JSON error messages and check the error chain. Reverting that sanitization would fail; transport-level reflections are not covered.
- Residual: **high** — transport errors remain wrapped verbatim at [npmapi/client.go:213](internal/modules/npm/npmapi/client.go:213). A compromised NPM can return a redirect with malformed `Location: http://[<password>`: the locally installed HTTP client includes that header in its parsing error before invoking `CheckRedirect` at [net/http/client.go:662](/usr/local/go/src/net/http/client.go:662). [deps.go:231](internal/modules/npm/deps.go:231) preserves the cause, and [agent/api/handler.go:872](internal/agent/api/handler.go:872) logs it. The same secret-reflection outcome therefore remains reachable through a sibling response path.

### Row 11: Fixed samples miss internet destination restrictions

- Verdict: CLOSED
- Fix: [eval/cells.go:44](internal/modules/firewall/eval/cells.go:44) partitions public addresses by matching prefixes. [guard/cells.go:38](internal/modules/firewall/guard/cells.go:38) additionally incorporates route and policy boundaries; [guard/cells.go:275](internal/modules/firewall/guard/cells.go:275) checks the resulting cells. Tunnel and Tailscale destinations are wildcard flows rather than documentation-address samples.
- Regression test: `TestV1r21TunnelFlowNamesTheFirstBlockedCell` and `TestV1r21TailscaleFlowChecksEveryIPv6Cell` in [internet_cells_test.go:72](internal/modules/firewall/guard/internet_cells_test.go:72) — reproduces the scenario: yes. They deny real public destination ranges while leaving the former sample addresses unaffected. Restoring fixed samples would fail.
- Residual: none for the original public-internet destination scenario. Private-source exposure coverage is addressed in row 6.

### Row 12: Compose diagnostics disclose interpolated secrets

- Verdict: PARTIAL
- Fix: Failed configuration and execution discard stderr at [compose_check.go:147](internal/modules/docker/compose_check.go:147) and [compose_run.go:279](internal/modules/docker/compose_run.go:279).
- Regression test: `TestV1r05FailedConfigReportsOnlyTheExitStatus` and `TestV1r05FailedRunReportsOnlyTheVerbAndStatus` in [compose_failure_test.go:31](internal/modules/docker/compose_failure_test.go:31) — reproduces the scenario: yes. Injected stderr contains an interpolated secret, and restoring stderr-derived details would fail. Successful config output followed by Darkpanel validation is not covered.
- Residual: **high** — the sibling validation path still quotes resolved values. The parser retains the resolved image at [compose/config.go:139](internal/modules/docker/compose/config.go:139); a build service whose image is absent produces an error containing the entire image reference at [compose/validate.go:141](internal/modules/docker/compose/validate.go:141). Thus `image: app:${SECRET}` can still put the interpolated secret into an error when configuration succeeds but the local-image check fails. [compose_check.go:462](internal/modules/docker/compose_check.go:462) returns that error unchanged apart from its definite mark.

### Row 13: Runtime login sources evade IPv6 prefix grouping

- Verdict: CLOSED
- Fix: [login.go:100](internal/controller/auth/login.go:100) extracts the parsed proxied client for rate limiting, while [ratelimit.go:74](internal/controller/auth/ratelimit.go:74) normalizes IPv6 sources to `/64`.
- Regression test: `TestV1r17LoginKeysTheProxiedClient` in [loginlock_test.go:83](internal/controller/auth/loginlock_test.go:83) — reproduces the scenario: yes. It rotates addresses and usernames within one proxied `/64`, checks that the shared source limit applies, and preserves the audit description. Reverting source extraction would fail.
- Residual: none.

### Row 14: NPM first-administrator bootstrap race

- Verdict: PARTIAL
- Fix: Detected conflicts trigger cleanup at [instance_deploy.go:287](internal/modules/npm/instance_deploy.go:287); [discardDeploy](internal/modules/npm/instance_deploy.go:460) attempts container, volume and network removal independently of apply cancellation.
- Regression test: `TestV1r12BootstrapConflictRemovesTheInstance` in [instance_deploy_conflict_test.go:39](internal/modules/npm/instance_deploy_conflict_test.go:39) — reproduces the scenario: partly. Its fake models another administrator winning before or during creation and asserts cleanup; reverting cleanup would fail. It does not establish bootstrap isolation or the pinned image’s behavior.
- Residual: **medium** — the initial race remains: port 81 is published at [deps.go:458](internal/modules/npm/deps.go:458), and the container starts before administrator creation at [instance_deploy.go:399](internal/modules/npm/instance_deploy.go:399). Cleanup depends on detecting a conflict; cancellation, transport failure and HTTP 400 return without cleanup at [instance_deploy.go:429](internal/modules/npm/instance_deploy.go:429). Under the repository’s unauthenticated-first-user contract, takeover before detection remains possible, and interrupted bootstrap can leave the instance running.

### Row 15: Proxy updates erase operator configuration

- Verdict: PARTIAL
- Fix: [proxyhost_edit.go:95](internal/modules/npm/proxyhost_edit.go:95) preserves host-level advanced configuration and custom locations whose path is not `/`.
- Regression test: `TestV1r10UpdateKeepsTheOperatorsAdvancedConfig` in [proxyhost_effects_test.go:155](internal/modules/npm/proxyhost_effects_test.go:155) — reproduces the scenario: partly. It covers host-level configuration and `/api`; reverting preservation would fail. It seeds no operator modification inside `/`.
- Residual: **medium** — operator edits to the existing `/` location are still silently discarded. [proxyHostEditKeep](internal/modules/npm/proxyhost_edit.go:98) excludes that location, replacing it with the generated configuration from [proxy/location.go:15](internal/modules/npm/proxy/location.go:15). The plan diff at [proxyhost_edit.go:335](internal/modules/npm/proxyhost_edit.go:335) does not disclose that configuration loss. This can remove manually configured restrictions or required upstream headers during an otherwise ordinary update.

## Summary

**9 CLOSED; 6 PARTIAL.** High residuals remain in rows **2, 6, 10 and 12**, so the requested security closure is not established.

Severity below is the register severity for closed rows and the residual severity under this brief for partial rows.

| Row | Severity | Verdict |
|---|---|---|
| 1 | high | CLOSED |
| 2 | high | PARTIAL |
| 3 | high | CLOSED |
| 4 | high | CLOSED |
| 5 | high | CLOSED |
| 6 | high | PARTIAL |
| 7 | high | CLOSED |
| 8 | medium | CLOSED |
| 9 | medium | CLOSED |
| 10 | high | PARTIAL |
| 11 | medium | CLOSED |
| 12 | high | PARTIAL |
| 13 | medium | CLOSED |
| 14 | medium | PARTIAL |
| 15 | medium | PARTIAL |

Read-only source review. No files were changed, and no builds, tests or network commands were run. Regression-test and revert-sensitivity assessments are based on inspected assertions, not execution.

## Other observations

- none