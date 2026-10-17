# Review of the M1 ADRs and open design questions

**Date:** 2026-10-17
**Reviewed:** docs/adr/ at f11e813, with register rows 12-14 of docs/superpowers/registers/2026-10-17-m1-adr.md
**Reviewer:** Fable 5.1 (`dr-superpowers:judge-fable`)
**Outcome:** the owner accepted every recommendation on 2026-10-17; applied on branch docs/adr-review-fixes.

**Outcome:** every Part A gap is real except none; four of them (D3, D4, D8, D12) are one problem and get one combined resolution. No ADR needs rejecting; 0003, 0004, 0005, 0006, 0007, 0009 and the README need named fixes before acceptance. Part B adds one Critical finding (an old binary cannot start on a migrated database as the ADRs are written, which defeats rollback) and several Important ones that M2 install and self-update need.

Path labels used below (all under `/root/repositories/darkpanel/`):
- `spec` = `docs/superpowers/specs/2026-10-17-darkpanel-design.md`
- `adrspec` = `docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md`
- `README` = `docs/adr/README.md`; `0001`..`0009` = `docs/adr/000N-*.md`
- `register` = `docs/superpowers/registers/2026-10-17-m1-adr.md`

---

# Part A: open questions

## R34 (register row 12): rollback restores the backup vs expand-then-contract

**Real?** Yes. `spec:243-244` says "a rollback restores the backup"; `spec:499` (compatibility table, Database row) says "restore on update rollback". `0005:36-40` says "A binary rollback therefore runs the old binary on the new schema without restoring data"; `0005:41-44` says the backup is "for disaster recovery only" and the agent journal "never restored"; `0007:47-48` rolls back by swapping the binary only. `adrspec:370-371` records the rejection of restore-on-rollback, but `adrspec:329-339` lists only R10 and R31 as spec amendments, so the spec was never updated.

**Recommendation.** Amend the spec; keep the ADR.
- `spec:243-244` R34 replacement: "Database migrations during an update run after a database backup that is kept for disaster recovery. Every release's migrations keep the schema usable by the previous minor release's binary (expand, then contract), so a rollback runs the previous binary on the migrated schema; the backup is not restored automatically."
- `spec:499` Database row replacement: "Forward-only, per-module migrations; backup before migrate; expand-then-contract so the previous minor's binary runs on the new schema; no automatic restore on rollback."
- Add R34 and R12 to the amendment list at `adrspec:329-339`.
- ADR 0005: accept as-is for this item, but see Part B finding 1, which is required for the ADR's own rollback claim to hold.

**Timing.** Before M2 (self-update epic). It is a text edit; do it now.

## R12 (register row 12): key readable only by root vs `controller.key` owned by `darkpanel`

**Real?** Yes. `spec:167-169`: "a key readable only by root". `0005:47-49`: `/etc/darkpanel/controller.key` owned by `darkpanel`, 0600. `0001:33-35`: the controller runs as the unprivileged `darkpanel` user and `0001:57-59` rejects a root controller. The controller must decrypt Tailscale, Cloudflare and Nginx Proxy Manager credentials to call those APIs, so a root-only key is unbuildable with an unprivileged controller.

**Recommendation.** Amend the spec; keep the ADR.
- `spec:167-169` R12 replacement: "Third-party credentials (Tailscale, Cloudflare, Nginx Proxy Manager) are encrypted at rest with a per-role key file, mode 0600, readable only by the service that uses it (root for the agent, the `darkpanel` service user for the controller), and never returned by the API after being saved."
- Rationale to record: a root-only key would require either a root controller (rejected, `0001:57-59`) or a root-held decryption broker that a compromised controller process could still call; the broker adds a privileged component without removing the exposure.
- If the owner wants to keep the literal wording, the only honest way is systemd `LoadCredential=`, which delivers a root-owned 0600 file into `$CREDENTIALS_DIRECTORY` of the unprivileged service. It costs a systemd dependency on the key path and a development-mode fallback. I do not recommend it.

**Timing.** Before M1 epic "Module framework" implements secrets (R12 is an M1 requirement), so now.

## D3 + D4 + D8 + D12 (rows 13 and 14): the update and rollback state machine

These four are one design hole: the ADR describes the happy path of one transaction but not who runs it, what survives a reboot, what happens across more than one minor, and which binary reads which record. One combined resolution follows the evidence.

**D12 real?** Yes. `0007:39-40`: "Run by the currently installed (old) binary as a transient systemd unit" names no user or launcher. Swapping the binary and restarting units needs root; the controller is unprivileged (`0001:33-35`); UI-, API- and fleet-triggered updates (`0007:52-54`, R36) therefore have no stated path to a root process. `0007:52` "the controller updates first, then its agent" is also ambiguous because both services run from one binary file (`0001:33`): the swap updates both at once, so the order can only mean restart order.

**D3 real?** Yes. The health check is step 4 of the transient unit (`0007:47-48`). A transient unit does not survive a reboot. `0007:49-51` covers only the case where the new service enters systemd's failed state (`OnFailure`). A new binary that starts but is unhealthy after a reboot (for example an agent that cannot reach the controller) is never rolled back, and the transaction record stays open forever.

**D4 real?** Yes. `0005:36-40` guarantees schema compatibility only with "the previous minor release's binary". `spec:86` (S5) requires upgrades from every prior v1.x release, so a host can go 1.2 to 1.5 directly; 1.5's migrations contain 1.4's contractions of what 1.2 read, and `darkpanel.prev` is 1.2, so rollback starts a binary on a schema that no longer carries what it reads. `0004:51-52` (record formats readable from the previous minor only) and `0007:35-37` (two-minor dual-signing window) have the same shape; the key case is already handled by stepping ("updates through the last dual-signed release first"), the other two are not.

**D8 real?** Yes, with a narrower window than the register implies. `0004:51-52`: "new binaries read all formats written by the previous minor release" is forward-only; the rollback binary is older. Inside the automatic two-minute window the problem only arises if the new agent accepts mutating actions, which `0007:41-42` ("take all lock keys for the duration") is meant to prevent, but ADR 0004 never says locks are persisted, so they are lost on the restart the update performs (Part B finding 5). After commit, a manual `darkpanel update rollback` (R31) days later definitely hands the old binary records it may not read; recovery runs before serving (`0004:43-45`), so it either refuses to start or skips a pending revert.

**Combined recommendation.** Replace `0007:39-54` with this decision; add two sentences to ADR 0004 and one to ADR 0006.

1. Who runs it. The update is the agent action `update.apply` (class `compensable`, compensating action `update.rollback`), reachable over the agent protocol and over the agent socket as root. The agent starts the transaction as a root transient unit through systemd D-Bus (`StartTransientUnit`), running the currently installed binary with `update run <transaction id>`. The controller never swaps binaries; on its own host it goes through its local agent (R26). A fleet rollout is the controller issuing `update.apply` to one host at a time, its own host first.
2. Transaction record. `/var/lib/darkpanel/update/<id>.json`, states `staged`, `swapped` (with a wall-clock deadline), `committed`, `rolled-back`; each state change is written with fsync by the old binary. Transaction, snapshot and journal record formats change only additively within a major version and carry a format version; every binary in the major reads every format in the major. (This replaces `0004:51-52`.)
3. Steps. (a) Refuse while any revertible change is unconfirmed; take the `update` lock, persisted in `agent.db` so it survives the restart. (b) Download, verify signature, trusted comment and checksum, run the new binary's self-test. (c) Record `swapped`, keep `darkpanel.prev`, swap atomically, restart `darkpanel-controller` if present, then `darkpanel-agent`. (d) Health check within two minutes: the controller's `/healthz` reports the new version; the agent reports the new version over its socket and, when enrolled, a connected controller session. Pass: record `committed`, release the lock. Fail or deadline: roll back.
4. Rollback is one-shot per transaction: if the record is `swapped`, swap `darkpanel.prev` back, keep the failed binary as `darkpanel.failed`, record `rolled-back`, restart the services; if the record is `committed` or `rolled-back`, do nothing. `OnFailure=darkpanel-rollback.service` on both units and a persistent `darkpanel-update-recover.service` (`Before=` both units, runs every boot) both execute `darkpanel.prev update rollback --pending`. A reboot during the window therefore always ends in a rollback, never a half-finished update; the operator re-runs the update. `darkpanel update rollback` typed by root re-executes `darkpanel.prev` when it exists, so the binary that wrote the record is the one that reads it. (ADR 0006 `0006:59-60` keeps "needs neither service".)
5. Stepping. An update never crosses more than one minor. From 1.2 to 1.5 the agent runs three transactions (latest patch of 1.3, then 1.4, then 1.5), each committed before the next; a failure leaves the host on the last committed minor. This satisfies the schema window (ADR 0005), the record-format window (ADR 0004) and the key-rotation window (this ADR) by construction. A manual rollback after commit goes back one step and uses the same refusal as step (a).
6. Order. On the controller host "controller first" means restart and health-check order; one swap serves both roles. If either role fails its check, both are rolled back together.

**Timing.** All four must be settled before M2 starts; the self-update epic implements exactly this state machine and the fleet and install epics depend on who holds root. The rule in item 2 also changes ADR 0004, which M1 epic "Module framework" implements, so write it now.

## D5 (row 13): renewal revokes the old serial before the agent saved the new certificate

**Real?** Yes. `0003:55-56`: "renewal revokes the previous serial"; `0003:57-58`: revocation "closes the host's live session". If the agent crashes, loses the connection or fails to write the file between issuance and persisting the new certificate, its only valid credential is revoked, the certificate is not expired so the re-enrollment rule at `0003:58-59` does not apply, and an admin must issue a new token by hand.

**Recommendation.** Replace `0003:55-59` with: "Agent certificates last one year. After two thirds of their life the agent requests renewal over its existing session; the controller issues a new certificate and keeps both serials valid. The agent stores the new key and certificate atomically (temporary file, fsync, rename) before using them. The controller revokes the previous serial when the agent first completes a handshake with the new certificate, or when the previous certificate expires, whichever comes first. Revocation is by serial, checked at every handshake, and closes the host's live session. An agent whose certificate has expired re-enrolls with a new token." Add to `0003:82-83` Consequences: tests cover renewal interrupted before the agent saved the certificate.

**Timing.** Before M2 (fleet epic writes the certificate code). The failure cannot occur before month eight of production, but the fix is a design rule, not a feature, and retrofitting it later means touching protocol code in M7.

## D6 (row 13): agent listener on Tailscale only vs the local agent over loopback

**Real?** Yes. `0003:33-35`: listener "bound to the Tailscale interface by default; any other bind is explicit". `0001:35-36`: the agent on the controller's host "connects over loopback like any other agent". Loopback is not listening, so the controller cannot manage itself with the default configuration (R26 is an M2 exit criterion, `spec:428`). A controller host without Tailscale has no agent listener at all.

**Recommendation.** Replace `0003:33-35` first sentence with: "The agent dials the controller's agent listener (default port 7443). The listener binds 127.0.0.1 and the Tailscale interface by default, the same default as the web listener (ADR 0006); any other bind address is explicit configuration. The agent on the controller's host dials 127.0.0.1." ADR 0001 needs no change. Do not bind all interfaces: the `darkpanel-enroll/1` ALPN accepts connections without a client certificate (`0003:36-37`) and must not face the internet. See Part B finding 8 for the related problem that the Tailscale address may not exist when the service binds.

**Timing.** Before M2 (fleet epic). One-line edit; do it now.

## D7 (row 13): recovery has no snapshot for compensable or irreversible actions

**Real?** Yes. `0004:43-45`: "an intent without a result is reverted from its snapshot" is written for every intent, but `0004:46-47` says compensable and irreversible actions "record intent and result but arm no timer", and the apply ordering at `0004:33-39` is explicitly "for revertible actions" only. A crash between intent and result for a non-revertible action leaves recovery with an instruction it cannot execute; whether it refuses to start, loops, or leaves the lock held is undefined.

**Recommendation.** Add to ADR 0004 Decision: "Apply ordering for compensable and irreversible actions: persist intent (fsync); mutate; run verify; persist result." And to Startup recovery: "A compensable or irreversible intent without a result is marked `interrupted` with the action's verify output, its locks are released and the entry is journaled; the agent never retries or compensates on its own. For a compensable action the UI and CLI offer the named compensating action. A repeated apply with an interrupted plan id returns the interrupted result." Simplest safe behaviour: report, do not guess.

**Timing.** Before M1 epic "Module framework" implements the lifecycle and recovery (R10 is M1). Now.

## D9 (row 13): secret redaction in snapshots blocks rollback of secret changes

**Real?** Yes. `README:39-42` (and `adrspec:42-44`): the `secret` tag means "redacted from logs, audit, snapshots, diffs, events and errors". `0004:33-37`: a revertible action reverts from its snapshot. Any revertible action whose previous state contains a secret field (Nginx Proxy Manager credentials R65, a Cloudflare tunnel token in a container environment R70, a DERP verify-clients setting) cannot be reverted, because the value it needs was removed from the only record that holds it. The same problem hits the plan record once it stores the input (D11).

**Recommendation.** Change the shared contract at `README:39-42` to: "The struct tag `secret:"true"` marks a field that is encrypted at rest wherever it is stored, including inside plan records and snapshots, is write-only through the API, and is redacted from logs, audit, diffs, events and errors." Mirror the wording at `adrspec:42-44`. ADR 0005 Secrets already defines the cipher and associated data per row, so snapshot and plan rows need no new mechanism. Rejected alternative to record: forbidding secret fields in revertible actions forces every module touching credentials into the compensable class and loses the confirm-or-revert safety for exactly the changes most likely to lock an operator out of a service.

**Timing.** The contract text must be fixed before M1 epic "Module framework" implements the tag. The encrypted-snapshot code path is first exercised in M3 and first carries a secret in M5/M6; that implementation can wait until M3.

## D10 (row 14): `O_NOFOLLOW` covers only the last path component

**Real?** Yes. `0009:33-36`: "refuses symlinks (`O_NOFOLLOW`)". `O_NOFOLLOW` rejects a symlink only as the final component; a symlinked parent directory is followed, and the temporary-file-plus-rename step at `0009:34-35` operates on the resolved directory. Severity is limited because declared paths are fixed in the manifest (`0009:33-34`) and the v1 targets (`/etc/ufw`, systemd units, `/etc/darkpanel`) are root-owned, so only root could plant the link. The ADR nonetheless claims a guarantee it does not deliver, and `0009:47-48` relies on it.

**Recommendation.** Replace `0009:33-36` with: "The file provider writes only paths and marked blocks declared in the module manifest. It opens the parent directory with `openat2(RESOLVE_NO_SYMLINKS)`, which fails if any component is a symlink, requires that directory to be owned by root and not writable by others, then creates the temporary file and renames it over the target relative to that directory descriptor, preserving the target's mode and owner. It records ownership in the ownership manifest that `darkpanel uninstall` reverses (R30). Declared paths are fixed, so this defends against a symlink planted by an unprivileged process, not against root." `openat2` needs Linux 5.6; every supported host (`spec:109-110`) ships 5.15 or later, and `golang.org/x/sys/unix` exposes it.

**Timing.** Text now (cheap). Implementation lands with the first writer: the ownership manifest in M2 install/uninstall, marked blocks in M3.

## D11 (row 14): the plan record keeps hash and diff, not the input apply executes

**Real?** Yes. `0004:26-30`: the plan "records ... canonical input hash ... a diff" and "Apply takes the plan id only, never new input". Nothing in the ADR stores the input, so apply has nothing to execute. The diff cannot stand in for it (it is redacted and descriptive).

**Recommendation.** Replace `0004:26-30` with: "Plan records host, action and version, principal, the canonical input (secret fields encrypted at rest, shared contracts) and its hash, guard results (for example the lockout check), a diff, and a hash of the state the action's lock keys cover. It is stored in the database of the side that executes it (agent or controller) and expires after 10 minutes. Apply takes the plan id only and executes the stored input; the hash is what the API and audit expose so a client can check the plan matches what it submitted. The controller re-authorizes the caller on apply and confirm; the agent trusts the controller and the local root actor." This depends on D9 (encrypted secret fields in stored records).

**Timing.** Before M1 epic "Module framework" (the plan store is core lifecycle code). Now.

---

# Part B: acceptance findings, most severe first

**1. Critical. An old binary stops on `core_migrations` rows it does not know, so every rollback after a migration fails to start.** `0005:32-35`: "A changed checksum or failed core migration stops startup." `0005:38-40` promises "runs the old binary on the new schema". After an update that migrated, `core_migrations` holds rows numbered above anything the old binary embeds. Unless the ADR says those rows are ignored, the old binary either treats them as unknown/mismatched and refuses to start, or tries to re-run numbering it does not have. With `OnFailure` wired (`0007:49-51`) that start failure invokes rollback again. Breaks S5 entirely. Fix: add to ADR 0005 Migrations: "A binary ignores `core_migrations` rows numbered above its own highest migration; it verifies checksums only for migrations it embeds. New columns are nullable or carry defaults so an older binary's inserts succeed."

**2. Important. Rollback is not guarded against running twice or in the wrong direction.** `0007:47-51`: "swap back and restart" and `OnFailure` runs `darkpanel.prev update rollback`. After a swap-back, `darkpanel.prev` is the failed binary (if the swap is a two-way exchange) or absent; if the restored old binary then fails for any reason, `OnFailure` either reinstalls the broken binary or runs nothing. Fix: items 2 and 4 of the combined Part A resolution (one-shot rollback keyed on the transaction record, failed binary kept as `darkpanel.failed`). Also state the unit settings that make `OnFailure` fire promptly (`Restart=on-failure` with a `StartLimitBurst` that is exhausted well inside the two-minute window).

**3. Important. Where migrations run during an update, and who owns the backup directory, are undecided.** `0005:41-44` backs up to `/var/lib/darkpanel/backups/<update id>/` "before migrating"; `0007:39-48` never mentions migrations; services run as different users (`0001:33-35`). If migrations run at service start, the controller (user `darkpanel`) must write the backup, does not know the update id, and needs write access to a root-owned tree; if they run inside the transaction, the record must say so and a migration failure must abort before the swap. Fix: decide one. Simplest: migrations run at service start (needed anyway for first install and M1 development); each role backs up into its own directory (`/var/lib/darkpanel/controller/backups/<from>-<to>-<timestamp>/`), and the update health check catches a failed migration. Amend `0005:41-44` and the spec R34 wording accordingly.

**4. Important. The shared directories as specified cannot be created with the stated ownerships.** `0005:29-30`: `controller.db` owned by `darkpanel` and `agent.db` owned by root in the same `/var/lib/darkpanel/`; SQLite in WAL mode creates `-wal` and `-shm` files next to the database, so the controller needs write permission on that directory. `0006:53-58`: `agent.sock` (root 0600) and `controller.sock` (`darkpanel` 0600) in one `/run/darkpanel/`; a `darkpanel`-user process cannot create a socket in a root-owned 0755 directory. Breaks M2 install on first boot. Fix: per-role subdirectories (`/var/lib/darkpanel/controller/` owned by `darkpanel`, `/var/lib/darkpanel/agent/` root) and `RuntimeDirectory=darkpanel-controller` / `darkpanel-agent` in the units, giving `/run/darkpanel-controller/controller.sock` and `/run/darkpanel-agent/agent.sock`. Update the paths in ADRs 0005 and 0006 and the R31 traceability line.

**5. Important. Locks are not persisted, so the update's "take all lock keys for the duration" is lost at the restart the update performs.** `0004:31-32` defines locks held "from apply until the change is confirmed, reverted or completed" without saying where they live; `0004:35-36` persists snapshot, intent and deadline but not locks. `0007:41-42` relies on locks surviving the service restart in step 3. Without persistence the new agent accepts mutating actions during the health-check window, writes records the rollback binary may not read (D8), and leaves changes mid-flight when rollback restarts it. Fix: add to ADR 0004 Locks: "Locks are rows in the executing side's database, keyed by lock key, owned by a change id or update transaction id; startup recovery releases locks whose owner has a result." 

**6. Important. Update dispatch to out-of-window agents requires a frozen update path, which no ADR states.** `0003:42-45`: a version "outside the supported window ... only updates are dispatched"; `README:53-55` and `0002:38-49` define compatibility per action version but let any action change meaning across a new version. For "only updates" to work on an agent three minors behind, `GET /agent/v1/hello`, `update.apply/v1` and the transport handshake must be compatible across the whole major. Fix: add to ADR 0003 Hello: "`/agent/v1/hello`, the `update.apply` and `update.rollback` actions and the yamux framing are frozen for the lifetime of protocol version 1; a change to any of them is a new protocol version, and a controller supports the previous protocol version for update dispatch only."

**7. Important. The health check is not defined.** `0007:47-48` says "Health-check within the limit" without saying what passes. For an agent, "process is running" is not health: an agent that starts but cannot complete the mTLS hello with the controller leaves the host unmanaged. Fix: item 3(d) of the combined resolution (controller: `/healthz` reports the new version and schema; agent: new version over its socket plus a connected controller session when enrolled; on the controller host, both).

**8. Important. Both listeners bind the Tailscale interface, which may not exist when the service starts or at all.** `0003:33-35`, `0006:26-29`. tailscaled may start after Darkpanel, may be installed later (R57 installs it), or may be absent. A bind to a specific address that is not yet assigned fails, and `After=tailscaled.service` does not help when the unit is missing. Fix: add to ADR 0006 Listeners: "The Tailscale bind uses `IP_FREEBIND`/`IPV6_FREEBIND` so the listener exists before the address does; the service watches for address changes and rebinds; if no Tailscale address is configured the listener is loopback only and the UI shows it." Both ADRs refer to this sentence.

**9. Important. Controller certificate lifetime and renewal are unspecified, and the web certificate path is unbuildable as written.** `0003:27-32` sets a 10-year authority and one-year agent certificates but no lifetime for the controller certificate that every agent verifies; when it expires the whole fleet disconnects at once and the only recovery is re-enrolling every host. `0006:26-29`: "HTTPS with a `tailscale cert` certificate" must be obtained by the controller process, which runs as `darkpanel` and cannot use tailscaled's local API unless that user is set as the Tailscale operator; and an authority-issued certificate with only the URI SAN at `0003:28-29` fails browser hostname checks. Fix: ADR 0003: controller certificate lifetime (one year), renewed automatically by the controller from its own authority at two thirds, with agents pinning the SAN (already the case) so re-issue is transparent. ADR 0006: the local agent (root) runs `tailscale cert` and hands the certificate to the controller over the agent protocol, or the installer sets `tailscale set --operator=darkpanel`; the internal-authority web certificate carries the host's DNS name and Tailscale addresses as SANs, separate from the agent-listener certificate.

**10. Minor. Associated data `<table>.<column>.<row id>` must not mean the SQLite rowid.** `0005:45-46`. Inference from the SQLite documentation: `VACUUM` may renumber rowids for tables without an explicit `INTEGER PRIMARY KEY`, and the backup at `0005:41-42` is a `VACUUM INTO`. A restore would then fail to decrypt every secret whose AD used the rowid. Fix: state "row id means the table's declared primary key (a ULID), never the implicit SQLite rowid."

**11. Minor. The compatibility test's baseline "schema snapshots of every released tag" has no storage decision.** `0002:38-39`; `0001:50` commits one file per action version, which is overwritten by additive changes. The test needs either `git show <tag>:schemas/...` for every tag or a `schemas/released/<tag>/` tree. Fix: pick one in ADR 0002 Consequences (the committed tree is simpler for CI and for the M8 release job).

**12. Minor. `/healthz` is public and carries the version and schema version.** `0006:31-32` public list "login, health"; `spec:173-175` R13. Harmless on the private listeners; once the panel is exposed through Nginx Proxy Manager or a tunnel (R72) it advertises the exact version to the internet. Fix: unauthenticated `/healthz` returns status only; version and schema version require a session or token.

**13. Minor. The "break-glass" action flag has no defined effect.** `0002:29-31` declares it; `0006:53-60` serves "every agent action" on the agent socket regardless. Fix: either define it as "served on the controller socket" for controller-side actions such as `user.reset-totp`, or remove it.

**14. Minor. First-admin creation has no stated path.** `spec:182-184` R15: created by CLI on the host; ADR 0006 names only `reset-totp` for the controller socket. Fix: list `darkpanel user create` as a controller-socket command.

**15. Minor. Forced downgrades are not bounded.** `0007:43-44` "refuse downgrades unless forced". A forced downgrade across more than one minor hits the same schema problem as D4 in reverse. Fix: a forced downgrade is limited to one minor and refused while a revertible change is unconfirmed.

**16. Minor. A controller rollback after its agents were updated violates "the controller is never older than its agents."** `README:53-55`; `0007:52-54`. Fix: `update rollback` on the controller refuses while any enrolled agent reports a newer version, unless forced; a forced rollback then marks those hosts out of window until downgraded.

**17. Minor. ADR 0008's fault list lacks the reboot-during-update case, and its nested-virtualization claim may be stale.** `0008:31-32` lists reboot during an unconfirmed change but not reboot during the update health-check window (D3). Add it. `0008:25-27`: I believe GitHub-hosted Ubuntu runners have exposed `/dev/kvm` since early 2024; the fail-loudly check is right either way, so this is a wording note only. Unverified in this session.

**18. Minor. The file provider does not say how it treats damaged marker blocks or preserves metadata.** `0009:33-36`. Renaming a temporary file over `/etc/ufw/after.rules` loses the original mode and owner unless copied, and a file with duplicated or unterminated `BEGIN/END DARKPANEL` markers must be refused rather than guessed at. The D10 wording above covers mode and owner; add the marker rule. First needed in M3.

**19. Minor. M1's in-process agent handler and the root-owned `agent.db`.** `0001:37-38` runs the agent handler inside the controller process (user `darkpanel`) in M1; `0005:30` makes `agent.db` root-owned. The walking skeleton is read-only so no agent writes happen, but the lifecycle core built in M1 will try to open the agent database. Fix: state that in M1 development mode both roles use the invoking user's paths, and that the split applies from M2 install.

**20. Minor. The enrollment token does not carry the controller address.** `0003:50-51`: it carries the authority fingerprint and controller SAN. The agent still needs host and port; say the token (or the install command) encodes them so there is one copy-paste secret.

**21. Minor. `darkpanel controller backup` and `restore` have no owning epic and no archive encryption decision.** `0005:52-55`, `0005:74-75` (the ADR itself flags the first half). R81's lost-controller runbook (M8) depends on them. Fix: assign to the M8 "Production rollout and runbook" epic now and decide the archive key (passphrase-derived with argon2id is the simplest match to R15).

**22. Minor. Wall-clock deadlines on restart can be replaced by a simpler rule.** `0004:41-42`, `0004:43-45`. After any restart the only safe action is to revert every unconfirmed revertible change regardless of deadline; the clock may be wrong on a freshly booted VPS. The current text amounts to this in practice; saying it plainly removes a clock dependency.

**23. Minor. The ADR spec's amendment list is incomplete.** `adrspec:329-339` names R10 and R31 only; R34 and R12 (Part A) belong there too.

---

# Per-ADR verdicts

| File | Verdict |
|---|---|
| `/root/repositories/darkpanel/docs/adr/README.md` | Accept after fixes: D9 secret-tag wording (`README:39-42`). |
| `/root/repositories/darkpanel/docs/adr/0001-stack-and-layout.md` | Accept. (Finding 19 is a one-sentence clarification; it does not block.) |
| `/root/repositories/darkpanel/docs/adr/0002-module-contract.md` | Accept. (Findings 11 and 13 are Minor and can be fixed in the same pass.) |
| `/root/repositories/darkpanel/docs/adr/0003-agent-protocol.md` | Accept after fixes: D5 (two-step renewal), D6 (loopback bind), finding 6 (frozen update path), finding 9 (controller certificate lifetime). |
| `/root/repositories/darkpanel/docs/adr/0004-change-lifecycle.md` | Accept after fixes: D7 (non-revertible recovery), D11 (plan stores the input), finding 5 (persisted locks), combined item 2 (additive record formats within a major). |
| `/root/repositories/darkpanel/docs/adr/0005-storage-and-secrets.md` | Accept after fixes: finding 1 (ignore unknown migration rows; this one is Critical), finding 3 (where migrations run and backup path), finding 4 (per-role directories). The R34 and R12 changes are spec-side. |
| `/root/repositories/darkpanel/docs/adr/0006-api-and-cli.md` | Accept after fixes: finding 4 (socket directories), finding 8 (Tailscale bind), finding 9 (web certificate source and SANs). |
| `/root/repositories/darkpanel/docs/adr/0007-release-and-update.md` | Accept after fixes: the combined D3/D4/D8/D12 resolution (launcher, transaction record, boot recovery unit, stepping, restart order), finding 2 (one-shot rollback), finding 7 (health check definition). |
| `/root/repositories/darkpanel/docs/adr/0008-test-harness.md` | Accept. (Add the reboot-during-update fault case, finding 17.) |
| `/root/repositories/darkpanel/docs/adr/0009-host-execution-safety.md` | Accept after fixes: D10 (`openat2(RESOLVE_NO_SYMLINKS)` wording and threat statement). |

Nothing is rejected. Settle R12, D7, D9 and D11 before M1 epics 2-6 continue; settle R34, D3/D4/D8/D12, D5, D6, D10 and Part B findings 1-9 before M2 starts.
