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
