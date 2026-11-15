# v1 security re-review, 2026-11-12

Re-review of rows 1-15 of `docs/dforge/registers/2026-11-06-v1-security-review.md` at `main` f7a6432, run because the M3 audit found no tracked record of the re-review their Acceptance requires (owner ruling 2026-11-12: rerun the panel on all 15 rows). The two panel reviewers worked from one shared brief, independently and read-only:

- Fable (`dforge:judge-fable`): `v1-security-rereview-fable.md`
- Codex `gpt-6-astra`: `v1-security-rereview-codex.md`

Residuals raised by one reviewer only, or graded differently, were verified the same day by `dforge:judge-opus` (read-only, against the code and the spec's rulings).

## Verdicts

| Row | Severity | Fable | Codex | Verifier | Result |
|---|---|---|---|---|---|
| 1 | high | PARTIAL | CLOSED | residual confirmed, high | open: new sibling path, below |
| 2 | high | PARTIAL | PARTIAL | not needed (both agree) | closed; residual is the accepted risk of W10b-R3 |
| 3 | high | CLOSED | CLOSED | - | closed |
| 4 | high | CLOSED | CLOSED | - | closed |
| 5 | high | CLOSED | CLOSED | - | closed |
| 6 | high | CLOSED | PARTIAL | residual confirmed, high | open: sibling path, below |
| 7 | high | CLOSED | CLOSED | - | closed |
| 8 | medium | CLOSED | CLOSED | - | closed |
| 9 | medium | CLOSED | CLOSED | - | closed |
| 10 | medium | CLOSED | PARTIAL | partly, medium | residual for owner ruling |
| 11 | medium | CLOSED | CLOSED | - | closed |
| 12 | medium | CLOSED | PARTIAL | partly, medium | residual for owner ruling |
| 13 | medium | CLOSED | CLOSED | - | closed |
| 14 | medium | CLOSED | PARTIAL | partly, medium | residual for owner ruling |
| 15 | medium | CLOSED | PARTIAL | confirmed, medium | residual for owner ruling |

## Residuals

- **Row 1, high.** An `operate` caller adds a proxy host forwarding to a free address A on a user-defined network at port 3000 or 81, then runs `npm.network.attach` with `address: A`. The check of `npm.proxyhost.add` reads NPM's addresses at that moment, and attach never reads the proxy hosts (`internal/modules/npm/network_claims.go`), so the hostname proxies to NPM's API or admin UI without `npm.admin.expose`. Not accepted anywhere; W10b-R2 states the opposite intent.
- **Row 6, high.** `panelFlows` (`internal/modules/firewall/guard/flows.go`) skips every request that came through an exposure, and the NPM public leg is built only from any-address flows over the internet cells, which leave out RFC 1918, CGNAT and IPv6 outside `2000::/3`. A deny of a private or tailnet client's HTTPS access to NPM passes the guard without an override. `docs/security/threat-model.md` says a concrete client "is checked whatever its range", which the code does not hold.
- **Row 10, medium.** A malformed `Location` header from a compromised NPM is quoted by Go's HTTP client in its error, which `npmapi/client.go` wraps and the agent logs (`internal/agent/api/handler.go`). Agent log only; contradicts W10b-R8.
- **Row 12, medium.** A secret interpolated into the image of a Compose service with `build:` reaches the `docker.compose_unavailable` detail through `compose/validate.go` when config succeeds but the image is absent; refused network and volume names are quoted the same way. W10b-R14 covers stderr only.
- **Row 14, medium.** Cancellation or a transport failure during NPM's first-admin creation fails the deploy without removing the instance; W11-R14 accepts only the HTTP 400 branch. Whether NPM 2.16.0 answers a lost race with 400 is unknown.
- **Row 15, medium.** An operator's edit to the "/" location is replaced on every `npm.proxyhost.update` and the plan diff does not show it; undo restores it from the sealed body. Contradicts W10b-R7.

## Other observations (ungraded)

- Fable: other modules still copy a stderr line into errors (`internal/modules/tailscale/deps.go`, `internal/modules/firewall/effects.go`, `internal/modules/firewall/restore.go`, `internal/modules/derp/server_install.go`); `internal/modules/derp/binary_update.go` wraps a raw TLS probe error into the change's error; under the `npm.admin.expose` parent `CheckUpstream` allows every port of NPM and the loopback, not only 81.
- Low notes on closed rows (rows 7, 8, 9, 10, 11, 12) are in the Fable report.

# v1 security re-review of rows 1 and 6, 2026-11-14

Re-review of rows 1 and 6 of `docs/dforge/registers/2026-11-06-v1-security-review.md` at `main` 10a0973a (last code commit d4f6281b, after w12a to w12d merged), run on the owner's ruling of 2026-11-14 because the last panel result on both rows (4172841) was PARTIAL from both reviewers. The reviewers worked from one shared brief, independently and read-only:

- Fable (`dforge:judge-fable`): `v1-security-rereview-w12d-fable.md` (its report-file write was refused; the reply is saved verbatim)
- Codex `gpt-6-astra`: `v1-security-rereview-w12d-codex.md`
- Verifier of the disputed residuals (`dforge:judge-opus`, read-only, against the code): `v1-security-rereview-w12d-verifier.md` (reply saved verbatim)

## Verdicts

| Row | Severity | Fable | Codex | Verifier | Result |
|---|---|---|---|---|---|
| 1 | high | PARTIAL | PARTIAL | residual A confirmed, high; B partly, high conditional; C confirmed, high | open: fix or owner acceptance needed |
| 6 | high | CLOSED (low residual) | PARTIAL (high residual) | residual confirmed, not an accepted limit, high | open: fix or owner acceptance needed |

Every original scenario of both rows is fixed, each with a regression test that fails on revert. The residuals are sibling paths.

## Residuals

- **Row 1, A (new, Fable only, high).** An `operate` caller gives a container X an alias and address A on a network N, attaches NPM to N, adds a proxy host forwarding the alias at port 3000 or 81 (the check passes: A is not NPM's), disconnects X, detaches NPM, then re-attaches NPM with `address: A`. `forwardTargets` skips a name with no answer (`internal/modules/npm/network_claims.go:962`, comment at 823-826), so NPM takes A while nginx still holds the old answer (inference) and forwards to NPM's API or admin UI. Every step needs only `operate`; no race, no Engine choice. The skip's premise, "a name with no answer can reach NPM only after it is re-pointed", is false here. `TestW12aAttachResolvesForwardHostNames` pins the skip.
- **Row 1, B (high, conditional).** Undoing a finished `npm.instance.upgrade` runs `restore` (`instance_upgrade.go:661-711`), which reconnects NPM with no forward check (comment at 112). The owner rulings (W12a-R1) accept this because a refusing rollback would leave NPM down, which holds for the rollback of a failed upgrade but not for a requested undo of a done upgrade. It needs the Engine to pick the freed address (or a race), and a static endpoint address defeats it. Not in the threat model's owner-accepted list.
- **Row 1, C (high; register row 142 recorded low).** `docker.container.stop` and `docker.network.disconnect` take the docker lock, the upgrade holds only the npm lock, so a caller can free a forwarded address between `forwardCheck` (`instance_upgrade.go:217`) and the new container's start (`:336`). Row 142 is re-graded high.
- **Row 1, accepted.** The DNS re-pointing risk (security row 2) still reaches the effect; it stays the accepted risk.
- **Row 6 (high; Fable graded it low).** On a host whose only IPv6 addresses are link-local, `npmPublicFlows` (`internal/modules/firewall/guard/flows.go:420-425`) builds no IPv6 flow, so a change that drops IPv6 TCP 443 passes without the override. A same-link client that sends the exposure's hostname to the host's link-local address reaches NPM through Docker's userland proxy; a browser practically cannot. The w12b design (spec line 54) carries the predicate over without naming it as a risk, so it is not an accepted limit, and `docs/security/threat-model.md` lines 215 and 229 ("checked on both families", "every unicast source") are not accurate for such a host. The 60-second auto-revert does not change the grade.

## Other observations

- `internal/modules/tailscale/linkguard.go:178-185` gives a tailnet client of an NPM exposure no flow of its own (stale comment since `clientPeer`); the verifier confirmed it. Low; whether the controller's Tailscale-address listener masks it is unverified.
- `internal/modules/npm/proxyhost_edit.go:241-250`: the undo's location check does not cut the forward host at its first `/`, so `<NPM address>/x` is restored unchecked. Low; writing such a location needs an NPM administrator.
- Security register row 1's note says both reviewers closed it at 4172841 and row 6's says both rated it PARTIAL in the same pass; the tracked record is the 2026-11-12 note above, so row 1's wording is wrong.

## Register changes

Rows 1 and 6 stay open with this result; row 142 goes back to open as high; new rows record residuals A and B and the two low observations.

# v1 security re-review of rows 1, 6, 142 and 176-179 after w12e and w12f, 2026-11-15

Re-review at `main` 6c41959f (w12e merged at ba881523, w12f at 6c41959f) of the rows whose Acceptance requires it, run from one shared brief (`.dforge/rereview-w12f-brief.md`, untracked) by two independent read-only reviewers, with a verifier for the disputed items:

- Fable (`dforge:judge-fable`): `v1-security-rereview-w12f-fable.md` (its report-file write was refused; the reply is saved verbatim)
- Codex `gpt-6-astra`: `v1-security-rereview-w12f-codex.md`
- Verifier (`dforge:judge-opus`, read-only, against the code): `v1-security-rereview-w12f-verifier.md` (reply saved verbatim)

## Verdicts

| Row | Severity | Fable | Codex | Verifier | Result |
|---|---|---|---|---|---|
| 1 | high | PARTIAL | PARTIAL | PARTIAL (attach window, medium, not owner-accepted) | open: fix or owner acceptance of the attach window |
| 6 | high | CLOSED | CLOSED | CLOSED | closed |
| 142 | high | CLOSED | CLOSED | CLOSED | closed |
| 176 | high | PARTIAL | PARTIAL | PARTIAL (same window) | open: same residual as row 1 |
| 177 | high | CLOSED | CLOSED | CLOSED | closed |
| 178 | low | CLOSED | CLOSED | CLOSED | closed |
| 179 | low | CLOSED | CLOSED | CLOSED | closed |

Every original scenario and each 2026-11-14 residual (A, B, C and the IPv6 link-local gap) is fixed, with regression tests that fail on revert by reading and, for the nine w12e fixes, by the recorded revert check. Rows 181-185: both reviewers found each change present and meeting its Acceptance.

## The residual that keeps rows 1 and 176 PARTIAL

The attach window (both reviewers; Fable medium, Codex high; verifier: confirmed in code, **medium**, not owner-accepted). An `operate` caller connects a container X it owns to a network so X holds address A, adds a proxy host forwarding to `A:3000`, starts `npm.network.attach` of NPM without a static address, and during the first reload (`network_claims.go:383`, up to 10 s) frees A with `docker.network.disconnect` or `docker.container.stop`. Those take the docker lock; the attach takes the npm lock. `ConnectNetwork` (`:388`) then lets the Engine pick the address (inference: A), and nginx forwards the caller's host to NPM's API until `joinedProblem` has resolved every forwarded name (`:870-882`, before the literal comparison at `:886-895`) and `disconnectAgain` has run. A forwarded name with no definite answer stretches the window to about 6 s. The same holds for the undo of a detach. Spec D1 residual (b) covers only a name that loses its answer between `nginx -t` and the reload, "not forced"; spec R20 rejects a design that would publish NPM's port 81 repeatably between connect and disconnect; `threat-model.md:298` and `:306` cover starts and upgrades only. It closes by a fix (compare literal targets before any lookup in `joinedProblem`, as `verifyHeld` does, and re-read the network just before the connect) or by an explicit owner acceptance.

## Other residuals (new register rows 190-199)

- Starts Darkpanel does not make (`unless-stopped` restart, Engine or host restart) are not verified (Fable: owner-accepted by D3/U2(a); Codex and verifier: no recorded owner sentence). Medium; needs an owner ruling.
- The `reloadNginx` `.err` rename (row 188, graded low and deferred) is reachable by an `operate` caller and can take down a host the caller may not change, the panel exposure included. Medium (verifier); row 188 is raised.
- An `operate` caller stops a forwarded container whose name gets a definite not-found and runs `npm.instance.upgrade`: the checks skip the name, NPM is recreated, and nginx cannot load the static location. The spec's "refuse up front" (self-review) was never implemented. Medium. Fable's claim that the upgrade finishes "done" is refuted by the verifier: the probe goes through nginx on port 81, so the upgrade rolls back instead (inference from the image layout).
- One forwarded name with no definite answer, on any proxy host including a disabled one, refuses every attach, undo of a detach, upgrade and undo of an upgrade; the client sees only "Unavailable" because 5xx details are dropped (`agent/api/handler.go:871-877`). Medium for the blocking, low for the hidden detail.
- Codex only: the harness `reload-follows-name` step passes the NPM bearer token in curl command text and failure errors (`test/harness/m6_npm_test.go:1073`, `vm.go:319,342`). Codex graded it high; the verifier graded it low because the token opens NPM only inside a throwaway test VM.
- Threat-model sentences the code does not hold: line 298 ("no caller can stretch it for a literal address" holds for starts only), line 305 (still says the proxy-host update against a running NPM was not run), line 284 (a reload is confirmed by `meta.nginx_online`, but an absent field counts as confirmed, `nginx_reload.go:43`), and the "not forced" wording of spec D1 (b).
- Low defects and test weaknesses listed in both reports: `qaSpan` accepts non-ranges (`guard/w12e_security_residuals_acceptance_test.go:21`); the harness failure half re-implements the reload call; an attach cut off between the connect and `afterJoin` has no recovery; `joinedProblem` treats a missing endpoint as holding nothing; `verifyHeld` failures are only logged although R29 says they are reported; `reloadBeforeJoin` codes every failure `npm.reserved`; the hidden-client omission of the Tailscale guard (row 180) and the missing-binding case of an NPM exposure; the reload can overwrite a concurrent NPM UI edit (no version condition).

## Register changes

Rows 6, 142, 177, 178 and 179 stay `verify` with the closed re-review recorded; rows 1 and 176 go back to `open`; row 188 is re-graded medium; new rows 190-199 record the residuals above.

# v1 security re-review of rows 1, 176, 188 and 190-199 after w12g and w12h, 2026-11-15

Re-review at `main` af933cf5 (w12g merged at 8d6fa72, completion ff4d54b9; w12h at af933cf5) of the rows whose Acceptance requires it, plus the nine w12h rows, run from one shared brief (`.dforge/rereview-w12g-brief.md`, untracked) by two independent read-only reviewers, with a verifier for the disputed item and the one PARTIAL residual:

- Fable (`dforge:judge-fable`): `v1-security-rereview-w12g-fable.md` (its report-file write was refused; the reply is saved verbatim)
- Codex `gpt-6-astra`: `v1-security-rereview-w12g-codex.md`
- Verifier (`dforge:judge-opus`, read-only, against the code): `v1-security-rereview-w12g-verifier.md` (reply saved verbatim)

## Verdicts

| Row | Severity | Fable | Codex | Verifier | Result |
|---|---|---|---|---|---|
| 1 | high | CLOSED | PARTIAL (reconcile memory, medium) | reconcile memory low: CLOSED | closed |
| 176 | high | CLOSED | PARTIAL (same) | CLOSED | closed |
| 188 | medium | CLOSED | CLOSED | - | closed |
| 190 | medium | CLOSED | CLOSED | - | closed (interval owner-accepted, U2) |
| 191 | medium | CLOSED | PARTIAL (same) | CLOSED | closed (limits owner-accepted, U3) |
| 192 | medium | PARTIAL | PARTIAL | PARTIAL, medium, not owner-accepted | open |
| 193 | medium | CLOSED | CLOSED | - | closed |

Rows 194-199 and the w12h rows 202-207, 209, 216 and 218: both reviewers found each change present and meeting its row.

## The disputed item: the reconcile memory (row 217 reopened)

`npm.reconcile` remembers a hit key on any `npm.reserved` failure (`reconcile.go:285-290`). When Apply finds no name hit (its lookups failed, so only literals were compared, or the name answered differently) and Verify finds it, the change fails `npm.reserved` and later passes skip the same hit on an external network until the hit set changes, a pass sees no hit or the agent restarts. The chain is confirmed by all three. Codex graded it medium on a Docker-alias framing; the verifier shows a Docker name cannot produce a name hit (`proxyhost_add.go:342` leaves NPM out of the members), so it needs a name the host resolver answers, the DNS risk D10 accepts, and such a caller gets the same lasting exposure by pointing the name at NPM's `darkpanel-npm` address, which reconcile never leaves by design. Low; D3 limits the memory to `darkpanel-npm` hits, so the code should too. The premise of the 2026-11-15 n/a ruling of row 217 (a different key next pass) is wrong for an identical key.

## The residual that keeps row 192 PARTIAL

An `operate` caller starts `npm.instance.upgrade` and, after the check after the pull (`instance_upgrade.go:256`), stops or disconnects the container a custom location's name forwards to (`docker` lock only; the upgrade holds `npm`). Nothing reads unanswered names again: `verifyHeld` treats a name with a definite not-found as holding nothing, and the rollback restores the same configuration and starts it with no unanswered check or nginx health check. The new container's and the restored original's nginx cannot load the location (inference), so every proxied site, the panel exposure included, stays down until the name answers. The undo of a finished upgrade has the same window (`upgrade_recover.go:40-43`), with no second rollback after it. A custom location forwarding to a DNS name the caller controls does the same with no docker action. Medium (availability), not owner-accepted: `threat-model.md:300` promises the up-front refusal. It closes by a fix (an unanswered check, or an nginx health check, after the start and before the rollback's start) or a recorded owner acceptance.

## Register changes

Rows 1, 176, 188, 190, 191, 193, 194-199 and the w12h rows stay `verify` with the closed re-review recorded; row 192 goes back to `open`; row 217 is reopened; new rows 226-236 record the low residuals both reports list (threat-model sentences, the exec reload's pending and worker witness, `joinGuard`'s order, `lostJoin` with a connect still in flight, the attach's 4xx, the undo's driver rule, the 4 MiB listing, the D7(a) disconnect, a wrong comment, a retried undo of an upgrade, and tests weaker than their names).
