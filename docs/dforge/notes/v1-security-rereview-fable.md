# Security re-review — Fable, commit f7a6432

Thirteen rows are closed; rows 1 and 2 are only partly closed, each with a residual that is high by the brief's rubric. Row 1's is a new sibling path through `npm.network.attach`; row 2's is the DNS re-pointing risk the owner already accepted.

## Verdicts

Method limits: I had no shell, so no `git log`/`git show`; I read the working tree, which the session snapshot shows clean at f7a6432. "Would fail on revert" is judged by reading, not by running. CLOSED rows may carry a low hardening note; PARTIAL means the row's own effect is still reachable.

### Row 1: operate user publishes NPM's API on port 3000
- Verdict: PARTIAL
- Fix: `internal/modules/npm/proxy/upstream.go:42-51` refuses every port of NPM's names and addresses outside the `npm.admin.expose` parent. The reserved set comes from `internal/modules/npm/proxyhost_add.go:259-298`; update uses it at `proxyhost_edit.go:311` and the undos at `proxyhost_edit.go:208-218`.
- Regression test: `TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses` in `internal/modules/npm/proxy/upstream_test.go:120` — reproduces the scenario: yes (`darkpanel-npm:3000` and `172.18.0.2:3000` under non-admin parents; fails if the port-81 condition returns). No test covers the path below.
- Residual: high (an `operate` scope reaches the `admin` effect). The address check runs once, against the endpoints NPM has at that moment (`proxyhost_add.go:264-290`), and the caller can change NPM's addresses afterwards:
  1. `npm.proxyhost.add` with `forwardHost` set to a free address A on a user-defined bridge network and `forwardPort` 3000 (or 81). A is not yet NPM's, so `CheckUpstream` passes, and a literal is never resolved (`proxyhost_add.go:377-379`).
  2. `npm.network.attach` with `network` and `address: A`. It needs only `operate` (`network_claims.go:26-35` sets no `MinScope`; `internal/core/scope.go:10-11`; `docs/api/npm.md:562`). It takes a caller-chosen IPv4 address (`wire.go:57-62`, `network_claims.go:215`, `360-366`), and its check never reads the proxy hosts (`network_claims.go:258-317`).
  3. The public hostname now proxies to NPM's own backend or admin UI, with no `npm.admin.expose` and no banner.

  The `operate` caller can also create the network with its own subnet (`internal/modules/docker/network_manage.go:87-91`, `128-139`). The threat model does not list this path (`docs/security/threat-model.md:284-288`). Closing it needs attach to refuse an address (given or assigned) that any proxy host or location forwards to, or `address` restricted to the composites.

### Row 2: DNS name resolving to loopback, NPM or the panel gateway
- Verdict: PARTIAL
- Fix: `checkForward` at `internal/modules/npm/proxyhost_add.go:368-413` resolves names through Docker's answers (`:304-356`) or the host resolver and checks every answer (`proxy/upstream.go:71-79`). The answer set is in the plan hash (`proxyhost_add.go:485-489`, `proxyhost_edit.go:378`), and apply runs the check again (`proxyhost_add.go:548`).
- Regression test: `TestV1r09AddRefusesANameThatResolvesToAReservedAddress`, `TestV1r09AddResolvesAgainAtApply` and `TestV1r09UpdateResolvesForwardHosts` in `internal/modules/npm/proxyhost_resolve_test.go:35,105,183` — reproduces the scenario: yes for a static record; would fail on revert.
- Residual: high by the rubric, and recorded as accepted by the owner (W10b-R3; `docs/security/threat-model.md:286`). The caller who controls the name points it at a public address for plan and apply, then re-points it to 127.0.0.1 or NPM's address. Darkpanel writes a static `proxy_pass` host in the "/" location (`proxy/location.go:15-29`), which nginx resolves itself (`upstream.go:81-84`). That nginx resolves again on every reload, and that another proxy-host change by the same caller forces a reload, are my inference about NPM, not read in this repository. The precondition is the original one, control of a DNS name. Row 1's attach path also reaches port 81 by address.

### Row 3: container inspect shows the first CMD element
- Verdict: CLOSED
- Fix: `internal/modules/docker/inspect.go:119-121`, `:179` (every `Cmd` element masked; `Entrypoint` keeps element 0).
- Regression test: `TestV1r13InspectMasksEveryCommandElement` and `TestV1r13InspectActionShowsNoCommandElement` in `internal/modules/docker/inspect_cmd_test.go` — reproduces the scenario: yes; fails if `redactCmd` keeps element 0.
- Residual: none. No other non-test code in `internal/modules/docker` reads `Cmd` or the list's `Command` field (`internal/platform/dockerapi/containers.go:18` is unused there).

### Row 4: NPM advanced configuration in plaintext effect documents
- Verdict: CLOSED
- Fix: documents hold keyed fingerprints (`internal/modules/npm/proxyhost_edit.go:71-81`, `proxyhost_add.go:203-224`, `effects.go:154-158`). The restorable body is sealed with the effect (`proxyhost_edit.go:388-392`, `582-587`), and undo reads only that (`:193-204`).
- Regression test: `TestV1r10UpdateEffectsHoldFingerprintsAndTheSealedBody`, `TestV1r10RemoveEffectsHoldFingerprintsAndTheSealedBody`, `TestV1r10AddObservationHoldsFingerprints` and `TestV1r10InverseNeverRestoresFromTheDocument` in `internal/modules/npm/proxyhost_effects_test.go` — reproduces the scenario: yes (an `Authorization` header credential at host level and in a location; plan, effect and observation checked).
- Residual: none. Access lists and certificates carry no credential fields in the module's types (`npmapi/types.go:66-119`).

### Row 5: Docker label values in plans, inputs and effects
- Verdict: CLOSED
- Fix: diffs show keys only (`internal/modules/docker/network_manage.go:44-50`, `volume_manage.go:72`); inputs carry the secret tag (`wire.go:276,283`); documents and plan hashes use keyed HMAC (`dstate/object.go:66-81`, `network_manage.go:175,302,324`, `volume_manage.go:81,195,217`, `observe.go:40,69-73`).
- Regression test: `TestV1r14PlanDiffsShowLabelKeysOnly`, `TestV1r14LabelInputsAreRedacted` and `TestV1r14EffectsHoldKeyedFingerprints` in `internal/modules/docker/label_secrets_test.go` — reproduces the scenario: yes.
- Residual: none. The unkeyed callers record empty values (`npm/effects.go:255,312`, `panel/network.go:99,272`).

### Row 6: guard omits the public leg of an NPM panel exposure
- Verdict: CLOSED
- Fix: the binding is recorded when the listener opens (`internal/modules/panel/listener.go:168-179`), validated and stored (`internal/controller/listen/runtime.go:427-433`, `runtime_store.go:35-36,128-129`), carried to the guard (`internal/controller/api/runtime.go:30-33`, `internal/controller/dispatch/guard.go:51-56`) and turned into flows (`internal/modules/firewall/guard/flows.go:270-277,296-326`). A missing binding is an unknown flow, which fails the guard (`guard.go:90-108`).
- Regression test: `TestV1r19RuntimeHandlerCarriesThePublicBinding` in `internal/controller/api/runtime_public_test.go`; `TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding`, `TestW11b09BlockedPublicBindingIsRefused` and `TestW11b09IPv6LegOfADualStackBinding` in `internal/modules/firewall/guard/exposure_test.go` — reproduces the scenario: yes (a ruleset dropping the forwarded 443 is refused for both binding forms). The guard tests build the context by hand; among tests, `PublicBinding` at the recording end appears only at `internal/modules/panel/listener_test.go:188`, which I did not read.
- Residual: none found. Low note: a firewall step run as a composite child carries no exposure path (`dispatch/guard.go:28-29`); the composites with such steps send fixed inputs, and I did not trace them further.

### Row 7: DERP certificate and key reads follow links
- Verdict: CLOSED
- Fix: `internal/modules/derp/deps.go:128,295-303` reads through `platform.ReadRegularFileBeneath` (`internal/platform/files.go:229-274`): no link at or beneath the certificate directory, regular file only, non-blocking open, size cap. Callers: `server_status.go:107`, `cert_renew.go:73,113,210`, `server_install.go:317`.
- Regression test: `TestV1r15ProductionCertReadRefusesPlantedFiles` in `internal/modules/derp/cert_files_test.go:39` — reproduces the scenario: partly (symlink and oversize; its comment defers the other planted files to `TestW11c05ReadRegularFileBeneath`, which I did not read).
- Residual: none. Low note: the read does not refuse a hard link (`files.go:251-274`), so it relies on `fs.protected_hardlinks`. The remaining `os.ReadFile` callers (`deps.go:127`) read only paths in root-owned directories.

### Row 8: five failures lock a username for everyone
- Verdict: CLOSED
- Fix: `internal/controller/auth/ratelimit.go:91-95,101-125,146-154` (the lock is on username plus source key); `login.go:70,118-123`.
- Regression test: `TestV1r17LimiterLocksTheUserAndSourcePair` and `TestV1r17LoginLockIsPerSource` in `internal/controller/auth/loginlock_test.go` — reproduces the scenario: yes (five failures from one source, the user logs in from another).
- Residual: low. An attacker sharing the admin's source key (same IPv4 egress or IPv6 /64) still locks that pair. The table-full refusal (`ratelimit.go:119-123`) needs 10,000 simultaneously locked keys, and I did not quantify whether the hashing slot allows that.

### Row 9: DERP probe scans private networks and returns raw TLS errors
- Verdict: CLOSED
- Fix: scope is `operate` (`internal/modules/derp/node_probe.go:67`); private and special-purpose literals and answers are refused (`:37-50`, `:107-134`, `:147-154`, `:208-238`); failures use a fixed vocabulary (`:344-376`); the probe connects only to the checked literal and follows no redirect (`deps.go:205,211-213`).
- Regression test: `TestV1r16NodeProbeNeedsOperate`, `TestV1r16ProbeRefusesPrivateLiterals`, `TestV1r16ProbeResolveDropsPrivateAnswers` and `TestV1r16ProbeErrorsUseTheFixedVocabulary` in `internal/modules/derp/node_probe_private_test.go` — reproduces the scenario: yes (`10.0.0.1` refused; an internal certificate name does not reach the result).
- Residual: none. Low note: region diagnostics still return tailscaled's raw text (`node_probe.go:256-262`), for regions of the DERP map, not caller-chosen targets.

### Row 10: NPM answer text in errors and logs
- Verdict: CLOSED
- Fix: `internal/modules/npm/npmapi/errors.go:11-22` and `client.go:223-225,244-257` keep only the status and a numeric code. Wrappers add the status alone (`deps.go:224-231`, `proxyhost_add.go:249-255`, `instance_deploy.go:495-505`).
- Regression test: `TestV1r11LoginFailureCarriesNoNPMText` and `TestV1r11MutationFailureCarriesNoNPMText` in `internal/modules/npm/answer_text_test.go` — reproduces the scenario: yes (an NPM that reflects the request body and bearer token; the whole error chain is checked).
- Residual: none. Low note: a decode error of a 2xx answer keeps Go's JSON error as cause (`client.go:236-238`); I did not verify what that text can quote.

### Row 11: fixed stand-in addresses for internet destinations
- Verdict: CLOSED
- Fix: `internal/modules/firewall/eval/cells.go:44-109` partitions the public space by rule prefixes. `internal/modules/firewall/guard/cells.go:38-90,275-304` checks each wild flow in every cell, also cut by routes and policy rules. Address matches the walk cannot decide end as unknown (`eval/eval.go:174-188`, `eval/match.go:72,204`).
- Regression test: `TestV1r21TunnelFlowNamesTheFirstBlockedCell` and `TestV1r21TailscaleFlowChecksEveryIPv6Cell` in `internal/modules/firewall/guard/internet_cells_test.go:72,123` — reproduces the scenario: yes (edge ranges dropped while the documentation address is allowed).
- Residual: none. Low note: special-purpose IPv4 ranges are outside the cell space (`eval/cells.go:21-39`); over the cell limit the flow is unknown, so it fails closed (`guard/cells.go:65-68`).

### Row 12: Compose stderr in errors and change records
- Verdict: CLOSED
- Fix: `internal/modules/docker/compose_check.go:147-149,530-537` and `compose_run.go:144-149,279-281` report the verb, exit status and command only.
- Regression test: `TestV1r05FailedConfigReportsOnlyTheExitStatus` and `TestV1r05FailedRunReportsOnlyTheVerbAndStatus` in `internal/modules/docker/compose_failure_test.go` — reproduces the scenario: yes.
- Residual: none. Low note: a parse failure of Compose's standard output stays as an error cause (`compose_check.go:150-153`); the parser's own messages name paths and keys (`compose/config.go:195-350`), and I did not check the JSON errors it wraps.

### Row 13: IPv6 clients behind an exposure not grouped by /64
- Verdict: CLOSED
- Fix: `internal/controller/auth/login.go:100-105` keys on the client address the listener parsed; `ratelimit.go:74-89` reduces it to a /64.
- Regression test: `TestV1r17LoginKeysTheProxiedClient` in `internal/controller/auth/loginlock_test.go:83` and `TestV1r17ProxiedLoginLockFollowsTheClientPrefix` in `internal/controller/api/proxied_login_test.go:24` — reproduces the scenario: yes.
- Residual: none.

### Row 14: local user wins NPM's first-admin creation
- Verdict: CLOSED
- Fix: `internal/modules/npm/instance_deploy.go:287-290,418-455` detects an administrator it did not create; `:460-491` removes the container, volumes and network.
- Regression test: `TestV1r12BootstrapConflictRemovesTheInstance` and `TestV1r12BootstrapConflictReportsAFailedRemoval` in `internal/modules/npm/instance_deploy_conflict_test.go` — reproduces the scenario: yes, against the repository's NPM fake, which answers a lost race with 403 (`fake/server.go:174-176`).
- Residual: low. Port 81 is still published on 127.0.0.1 before the administrator exists (`deps.go:458`), so a local user can make every deploy fail. A 400 to the creation is not a conflict (`instance_deploy.go:441`), so closure depends on NPM 2.16.0 answering a lost race with another 4xx. Unknown: I could not check the pinned image; one harness race against it would settle this.

### Row 15: update erases the operator's advanced configuration
- Verdict: CLOSED
- Fix: `internal/modules/npm/proxyhost_edit.go:95-104,331` keeps the host's advanced configuration and every location but "/".
- Regression test: `TestV1r10UpdateKeepsTheOperatorsAdvancedConfig` in `internal/modules/npm/proxyhost_effects_test.go:155` — reproduces the scenario: yes.
- Residual: low. An advanced configuration the operator wrote on the "/" location is still replaced by the module's own, and the plan diff shows no location line (`proxyhost_edit.go:335-365`). Undo restores it from the sealed body.

## Summary
| Row | Severity | Verdict |
|---|---|---|
| 1 | high | PARTIAL (residual high: attach path) |
| 2 | high | PARTIAL (residual high by rubric, owner-accepted) |
| 3 | high | CLOSED |
| 4 | high | CLOSED |
| 5 | high | CLOSED |
| 6 | high | CLOSED |
| 7 | high | CLOSED |
| 8 | medium | CLOSED |
| 9 | medium | CLOSED |
| 10 | medium | CLOSED |
| 11 | medium | CLOSED |
| 12 | medium | CLOSED |
| 13 | medium | CLOSED |
| 14 | medium | CLOSED (depends on NPM 2.16.0's status for a lost race) |
| 15 | medium | CLOSED |

## Other observations
- Other modules still copy a stderr line into errors: `internal/modules/tailscale/deps.go:233`, `internal/modules/firewall/effects.go:109`, `internal/modules/firewall/restore.go:426`, `internal/modules/derp/server_install.go:516`.
- `internal/modules/derp/binary_update.go:211` wraps the raw TLS probe error of the host's own DERP server into the change's error.
- Under the `npm.admin.expose` parent `CheckUpstream` allows every port of NPM and the loopback, not only 81 (`internal/modules/npm/proxy/upstream.go:42`; the w10b plan's contract says port 81).
