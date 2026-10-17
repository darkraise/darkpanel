# Review of the M1 platform core spec

**Date:** 2026-10-17
**Reviewed:** docs/superpowers/specs/2026-10-17-m1-platform-core-design.md at dc5ac8e
**Reviewer:** Fable 5.1 (`dr-superpowers:judge-fable`), requested by the owner
**Verdict:** ready after fixes. The design conforms to the ADRs in substance and every pinned version resolves; the shared foundation interfaces and core tables are named but not defined, which is where parallel waves would diverge.
**Outcome:** the owner accepted every recommendation on 2026-10-17; applied to the spec in the revision that follows dc5ac8e.

## Recommendations

Every finding is real. Accept as the reviewer proposed, except where the
recommendation column says otherwise.

| Id | Severity | Topic | Recommendation |
|---|---|---|---|
| F1 | high | `otpauthUri` output leaks the TOTP secret into audit and change results | Drop `otpauthUri`; the CLI builds it from the secret, username and issuer `Darkpanel` |
| F2 | medium | sealed passwords persist in plan rows; sessions and tokens never pruned | Accept |
| F3 | medium | change state machine not written down | Accept: transition table |
| F4 | medium | no way to recompute the state hash at apply | Accept: engine re-runs `Plan` with the stored input |
| F5 | medium | `synchronous` and transaction lock mode unpinned | Accept: `synchronous(FULL)`, `_txlock=immediate` |
| F6 | medium | controller-side revertible actions not refused | Accept |
| F7 | medium | `RedactJSON` underspecified | Accept: resolve `$defs`, arrays element-wise, unknown shape redacted whole |
| F8 | medium | block read follows symlinks; cap behaviour; `--`; `Ownership` implementer | Accept: P2 owns the ownership table and its implementation |
| F9 | medium | `__Host-`/Secure cookie on the plain-HTTP loopback listener | Modified: `__Host-darkpanel_session` (Secure) on HTTPS listeners, `darkpanel_session` (HttpOnly, SameSite=Strict, not Secure) on loopback HTTP; works in every browser |
| F10 | low | bearer vs cookie precedence; `/healthz` with a bad credential | Accept: bearer wins and never falls back; `/healthz` ignores bad credentials |
| F11 | low | idle expiry writes on GET | Accept: touch at most once a minute |
| F12 | low | token creation rule self-contradictory; list, revoke, user delete | Accept |
| F13 | low | hash semaphore wait; rate-limiter growth | Accept |
| F14 | low | break-glass action over the network: code; OpenAPI | Accept: `auth.forbidden` 403; excluded from OpenAPI |
| F15 | low | HSTS ignored for IP hosts | Accept: keep the header, drop any claim |
| F16 | low | `reset-totp` returns the user to `pending` | Accept |
| F17 | low | key creation crash path | Accept |
| F18 | medium | README shared contracts and ADR 0006 reserved names edited | Accept: ADR 0011 "Actors, reserved CLI names and layout additions" |
| F19 | low | layout deviations from ADR 0001 | Accept: ingestion under `internal/controller/audit/`; deviations recorded in ADR 0011 |
| F20 | low | `core_journal` never pruned | Accept: prune after ack |
| F21 | low | interrupted compensable change does not offer its compensating action | Accept |
| F22 | low | backup names need the previous binary version | Accept: `binary_version` in `core_meta` |
| F23 | medium | full nmap scan too slow; nmap not installed | Accept: pinned fast-scan options, nmap in the scanner's cloud-init |
| F24 | low | bridge mechanism unspecified | Modified: QEMU `-netdev socket,mcast=` link between target and scanner; no bridge, no sysctl, NICs matched by MAC |
| F25 | low | vendor apt pins can vanish | Accept: fail with a clear "pinned package missing" message |
| F26 | low | `tsc --noEmit` skips project references | Accept: `tsc -b` |
| F27 | low | `version`/`commit` live in `package main` | Accept: `internal/buildinfo` in P0 |
| F28 | low | `harness` build tag never linted | Accept |
| F29 | low | "library mode" is v6 wording | Accept: "declarative mode" |
| F30 | low | `statfs` and arm64 `/proc/cpuinfo` | Accept |
| F31 | high | foundation interfaces named but not defined | Accept: a Foundation interfaces section with Go signatures and DTOs |
| F32 | high | core table DDL incomplete; `core_audit.seq` missing | Modified: each part owns its own numbered migration file (P0 0001, P4 0002, P5 0003, P11 0004 on the controller; P0 0001, P4 0002, P2 0003 on the agent) with the columns listed in the spec |
| F33 | medium | `Env` lacks the principal | Accept |
| F34 | medium | module preflight and state seeding across the agent boundary | Accept |
| F35 | high | changes on the agent cannot be listed, confirmed or reverted from the controller | Accept: controller change index plus `AgentClient.Change` and `Changes` |
| F36 | medium | JSON bodies undefined; duplicate host routes | Accept: define bodies; drop `GET /api/v1/hosts*` in favour of `core.host.list` |
| F37 | medium | no error-code-to-status map | Accept |
| F38 | medium | CA fingerprint pinning algorithm | Accept |
| F39 | low | `schemagen` needs the core module from a later wave | Accept: `internal/registry.Build()` in P0 |
| F40 | low | OpenAPI and routes not tied | Accept |
| F41 | low | `Clock` fake; gate 7 must find dispatch runs | Accept |
| F42 | low | "every id is a ULID" wording | Accept |
| F43 | low | repeated apply returns a redacted token | Accept: state it |
| F44 | low | `Content-Type` requirement; 405 code | Accept |
| F45 | low | loopback binds IPv4 only | Accept: also `[::1]:8080` |
| F46 | low | explicit HTTPS bind address not in SANs | Accept |
| F47 | low | unbounded username in failed-login audit | Accept |
| F48 | low | enabled-but-unavailable module tab | Accept |
| F49 | low | input hash cannot be reproduced by a client | Accept: drop the claim |
| F50 | low | audit never pruned in v1 | Accept: state it |
| F51 | note | YAGNI check found only the duplicate host routes | No action beyond F36 |

## Notes

The table above carries each finding's substance and fix; the reviewer's line
references were against dc5ac8e. Severity counts: 4 high (F1, F31, F32, F35),
15 medium, 31 low, 1 note.
