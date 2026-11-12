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
