# Review of the M1 architecture decisions spec

**Date:** 2026-10-17
**Reviewed:** docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md at 2e5a914
**Reviewers:** Fable 5.1 (`dr-superpowers:judge-fable`), Codex `gpt-6-astra` high, same brief
**Raw counts:** Fable 2 critical, 6 high, 10 medium, 6 low/nit; Codex 16 high, 6 medium, 2 low.

Both reviewers asked to keep: dial-out mTLS with an internal authority and
URI-SAN host ids, compiled-in modules with explicit action versions, the agent
revert timer persisted across restarts, plan state hash and expiry, committed
schema snapshots, argv-only execution, forward-only checksummed migrations,
the approval-gated signing job, write-only secrets and the external scanner.

## Merged findings

Raised by: F = Fable, C = Codex. Every finding was accepted; the Resolution
column names the revised spec section.

| # | Sev | By | Finding | Resolution |
|---|---|---|---|---|
| 1 | critical | F, C | Agents cannot tell the controller from another agent: one authority, chain-only checks | 0003 Identity: EKUs and fixed SANs per role, agents pin the controller SAN |
| 2 | critical | F, C | Restoring the database on update rollback erases pending reverts, enrollments and revocations | 0005 expand-then-contract, journal never restored; 0007 update refuses while a change is unconfirmed |
| 3 | high | C | Crash between mutation and persisting the revert deadline leaves a permanent lockout | 0004 apply ordering: persist snapshot, intent and deadline first; startup recovery |
| 4 | high | C | A new binary that cannot start cannot roll itself back | 0007 old binary runs the update as a transient unit; `OnFailure` rollback unit |
| 5 | high | F, C | Enrollment: who assigns the host id, token entropy, atomic use, pin before token, mixed-auth listener | 0003 Enrollment and ALPN split |
| 6 | high | F, C | Revocation per host not per certificate; live sessions survive revocation | 0003 serial revocation, renewal revokes previous, revocation closes session |
| 7 | high | C | No deny-by-default boundary across listeners, methods and transports (CyberPanel class) | 0006 Listeners and Authentication boundary |
| 8 | high | C | Plans lack identity and idempotency; a lost apply response can act twice | 0004 plan binding, apply by plan id, stored result, re-authorization |
| 9 | high | C | Per-module locks miss shared resources across modules | 0004 lock keys; 0002 cross-module changes go through the owning module |
| 10 | high | C | Universal rollback impossible (prune, deletes); controller-side actions lack a lifecycle | Shared contracts action classes; 0004 controller-side actions; R10 amended |
| 11 | high | C, F | Argv alone does not stop option injection, PATH or environment abuse; ownership unenforced | New ADR 0009 host execution safety |
| 12 | high | C | Secret key lifecycle, nonce and associated data, redaction across logs and snapshots | 0005 Secrets; `secret` tag in shared contracts |
| 13 | high | F, C | Build provenance: a compromised build gets signed (Webmin class) | 0007 Build provenance and attestation check before signing |
| 14 | high | F, C | Key rotation strands hosts that skip releases; replayed old signed files allow downgrade | 0007 dual signing window, trusted comment with version and channel |
| 15 | high | C | "Additive" changes can be silently ignored by an older agent | 0002 schema hash in hello, controller refuses unknown fields; stricter rules |
| 16 | high | F, C | Upgrade ordering and skew direction undefined | Shared contracts version skew; 0007 controller first, resumable rollout |
| 17 | high | F, C | Break-glass `reset-totp` and `update rollback` cannot go through the agent socket | 0006 Local access with controller socket; R31 amended |
| 18 | high | F | Standard router rejects `v{n}:plan` path segments | 0006 `/{version}/plan` routes |
| 19 | high | F | Controller privilege and authority key custody undecided | 0001 controller runs as `darkpanel`; 0005 authority key and backup |
| 20 | medium | F, C | Module enablement, migration failure isolation, per-role migrations | 0002 `core_module_state`; 0005 migration failure policy |
| 21 | medium | C | Offline detection, journal acknowledgement and bounded queues | 0003 Liveness and Journal delivery |
| 22 | medium | F | Local socket scope contradiction; file mode is the only check | 0006 all agent actions locally with `SO_PEERCRED` |
| 23 | medium | F | 90-day certificates lock out hosts offline for over 30 days | 0003 one-year certificates, re-enroll when expired |
| 24 | medium | F | Snapshot and journal formats unversioned across updates | 0004 format version |
| 25 | medium | F | Web TLS and agent-port exposure undecided | 0006 Listeners; 0003 agent listener on tailnet by default |
| 26 | medium | F, C | Harness: nested KVM not guaranteed, scanner may sit in an exempt private range, IPv6, fault injection, test tailnet | 0008 revised |
| 27 | medium | F, C | Parallel ADRs share hidden contracts (schema generator, naming, secret tag) and can diverge | Shared contracts in wave 0; wave 2 reconciliation |
| 28 | medium | C | Generated CLI lacks nested input, secret input, async and `--yes` semantics | 0006 CLI |
| 29 | medium | C | Dependencies unqualified | yamux maintenance verified by Fable; exact pins in 0001, 0005, 0007 |
| 30 | low | F, C | `toolchain` directive does not pin | 0001 exact toolchain, `GOTOOLCHAIN=local` |
| 31 | low | F | Whole-host state hash causes spurious refusals | 0004 hash covers lock-key state only |
| 32 | low | F | Dotted action names to subcommands, reserved names | 0006 CLI |
| 33 | low | F | modernc caveats (pragmas, one writer, libc pin) | 0005 |
| 34 | low | F | `/dev/kvm` absence is a Proxmox setting | 0008 note |
| 35 | low | C | ADRs immutable on first write; DERP runtime decision unrecorded | Goal: Proposed until the epic closes; Out of scope names M5 and M6 |

## Not adopted

- Reverse HTTP/2 instead of yamux (Fable, YAGNI): see the spec's Rejected
  alternatives.
- Inlining minisign verification (Fable, nit): the pinned library stays; the
  format is stable and the library small.
