# v1 w12e: close the residuals of the 2026-11-14 panel re-review

**Source:** owner request 2026-11-14 "fix them", on the panel re-review of security rows 1 and 6 at 10a0973a (`docs/dforge/notes/v1-security-rereview.md`, reports `v1-security-rereview-w12d-{fable,codex,verifier}.md`) and the M3 audit rerun at b509e772 (`.dforge/milestone-M3-audit-rerun-report.md`).
**Covers rows:** `docs/dforge/registers/2026-11-06-v1-security-review.md` #1, #6, #142, #176, #177, #178, #179; `docs/dforge/registers/2026-10-21-v1-completion.md` #37 (R73).
**Execution strategy:** sequential — inline execution
**Depth:** full — risk

## Decisions

Quoted answers of the owner: "fix them" (the six findings the re-review left open, and the threat-model sentences they make inaccurate); "Full dforge flow (Recommended)" (the process question, 2026-11-14). The approach choices below are the recommendations of this session, put to the owner at the spec review.

- **D1, residual A (row 176): refuse while a forward host has no answer.** `forwardTargets` (`internal/modules/npm/network_claims.go`) today drops a name with no answer, on the premise that it can reach NPM only after it is re-pointed. That premise is false: nginx resolved the name when it loaded its configuration and keeps that address while the name has no answer, so NPM joining a network at that address is the exposure. The guard cannot know the address nginx holds, so it refuses to let NPM join or re-join any network while a forward host is a name with no answer. Rejected: recording the addresses each name resolved to at add or update time in a new table and treating them as targets. It closes the re-answered-name variant too, but adds a migration and write paths in add, update, undo and remove; that variant (a name re-answered at another address) stays under the accepted `forward host` residual risk, because the caller re-points a name after apply in both. Rejected: restarting NPM so nginx re-resolves, because it interrupts every proxied site.
- **D2, residual B (row 177): check the undo of a finished upgrade.** `Inverse` of `npm.instance.upgrade` in phase `done` runs `forwardCheck` before `rollback` and `restore`. The reason of ruling W12a-R1, that a refusing rollback would leave NPM down, holds for the rollback of a failed upgrade and for recovery, which stay unchecked; it does not hold for a requested undo of a finished upgrade, where NPM is up on the new image and a refusal leaves it up.
- **D3, residual C (row 142) and the same window in attach: hold the `docker` lock.** `npm.network.attach`, `npm.network.detach` and `npm.instance.upgrade` take lock keys `npm` and `docker`, so `docker.container.stop`, `docker.network.disconnect` and every other Docker mutation (all hold `docker`) cannot free a forwarded address between the check and the connect, in the action or in its undo. Locks are taken together or not at all (`internal/core/lifecycle/locks.go`), so there is no deadlock. Cost: while one of the three runs, a Docker mutation is refused `lock.held`, and an upgrade holds it for minutes. Rejected: re-checking after the new container is connected, because a refusal then needs the unchecked rollback.
- **D4, row 6: the guard builds the IPv6 leg of a `[::]` binding on a link-local-only host.** `npmPublicFlows` (`internal/modules/firewall/guard/flows.go:420`) and `wildcardDsts` (`guard/route.go:245`) leave link-local addresses out, so a host whose only IPv6 addresses are link-local, typically an IPv4-only VPS, gets no IPv6 flow and a change that drops IPv6 443 passes. Both stop excluding link-local for IPv6. The flow is `standIn`, so a cell whose reply has no route is skipped: on such a host the cells of global sources have no route and are skipped, and the link-local cells, whose reply leaves by the on-link route, are evaluated. Rejected: an `unknown` flow on such hosts, which would refuse every guarded change on every IPv4-only host.
- **D5, row 178: the Tailscale link guard protects the client of an NPM exposure.** For a request through an exposure the guard adds the client's flow to each public binding, as `linkListenFor` does for a direct client; a client that is a host address adds none, because it is the hidden client of row 6.
- **D6, row 179: cut the forward host at its first `/` in `checkRestoredLocation`** (`internal/modules/npm/proxyhost_edit.go:241`), as `forwardTargets` does.
- **D7: the accepted DNS risk stays.** A caller who controls a DNS name can still publish NPM's own API through it (Known residual risks, `forward host`); fixing rows 1, 142, 176 and 177 closes the paths that need no name the caller controls, not that one. The threat model says so next to each fix. Rejected: pinning the checked addresses by rewriting the forward host to a literal, which breaks forwarding to a container by name across a recreate.
- **D8: no harness change.** The six fixes are guard and lifecycle logic covered by unit tests on captured state; gate 7 reruns on the merge.

## What changes

| Fix | Files |
|---|---|
| D1 | `internal/modules/npm/network_claims.go` (`forwardTargets`, `forwardGuard`, `forwardTaken` and their callers at the attach plan and apply, the undo of a detach), `internal/modules/npm/instance_upgrade.go` (`forwardCheck` reads the same result) |
| D2 | `internal/modules/npm/upgrade_recover.go`, `internal/modules/npm/instance_upgrade.go` |
| D3 | `internal/modules/npm/network_claims.go` (attach and detach specs), `internal/modules/npm/instance_upgrade.go` (spec), `internal/modules/npm/deps.go` (a `dockerLockKey` constant with its reason) |
| D4 | `internal/modules/firewall/guard/flows.go`, `internal/modules/firewall/guard/route.go` |
| D5 | `internal/modules/tailscale/linkguard.go` |
| D6 | `internal/modules/npm/proxyhost_edit.go` |
| threat model | `docs/security/threat-model.md` lines 215, 229, 281, 292, 295 and the residual-risk list |

The registers: rows 176, 177, 142, 178, 179 get the plan as Assigned and, once merged and re-reviewed, `verify`.

## Acceptance

A1. [test, neg, medium] `npm.network.attach` is refused with `npm.reserved` while a proxy host or custom location forwards to a name with no answer from Docker or the host's resolver, with a static or an Engine-chosen address, naming the proxy host and the name; NPM is not connected and nothing is recorded. Q: is it acceptable that attaching NPM to a network, undoing a detach and upgrading NPM are refused while any proxy host forwards to a name with no answer, for example the name of a stopped container, until the host is removed or the container started?
A2. [test, high] The undo of a detach and `npm.instance.upgrade` (plan and apply) are refused the same way, since they run the same guard.
A3. [test, high] A name that has an answer is checked as before: a name answered with an address NPM cannot take still passes (`TestW12aAttachResolvesForwardHostNames`'s first half stays green), and a host with no proxy host forwarding to a name with no answer attaches as before.
A4. [test, neg, high] Undoing a finished `npm.instance.upgrade` is refused with `npm.reserved` when a proxy host forwards to an address the restored container could take on the networks it rejoins, such as the address of a container disconnected after the upgrade; the new container keeps running and the change stays `done`.
A5. [test, high] Undoing a finished upgrade with no such forward restores the original as today, and the rollback of a failed upgrade and recovery run no forward check.
A6. [test, neg, high] Each of `npm.network.attach`, `npm.network.detach` and `npm.instance.upgrade` takes the lock keys `npm` and `docker`, with each undo holding the same keys: while a change holds `docker`, each is refused `lock.held`, and while one of them is applying, `docker.network.disconnect` and `docker.container.stop` are refused `lock.held`; the registry validation of the actions passes.
A7. [test, neg, high] On a host whose only non-loopback IPv6 addresses are link-local, a change that drops IPv6 TCP 443 is refused without the override when the request came through an NPM exposure with a `[::]:443` binding, naming the exposure and the blocked range.
A8. [test, high] On that host a change that touches no IPv6 443 rule, and a host with no IPv6 address at all, pass as before (no over-refusal), and the guard's tests on a host with a global IPv6 address are unchanged.
A9. [test, neg, high] For a request through an NPM exposure whose client is a tailnet address, a change that takes the Tailscale link down is refused by the Tailscale link guard without the override; a client that is a host address adds no flow.
A10. [test, neg, high] Undoing a proxy host update or removal is refused with `npm.reserved` when a restored location's forward host is an NPM address followed by `/` and a path.
A11. [test, high] Every fix has a named regression test that fails when the fix is reverted: `TestW12eAttachRefusesAForwardNameWithNoAnswer`, `TestW12eUpgradeUndoChecksForwards`, `TestW12eNetworkActionsHoldTheDockerLock`, `TestW12eLinkLocalOnlyHostGuardsIPv6443`, `TestW12eTailnetClientOfAnExposure`, `TestW12eRestoredLocationCutAtSlash`.
A12. [demo, high] `docs/security/threat-model.md` says only what the code holds: lines 215 and 229 (the IPv6 leg on a link-local-only host), line 281 (a name with no answer now refuses a join), lines 292 and 295 (the undo of a finished upgrade is checked and an address cannot be freed by another Docker mutation during the three actions; rollback and recovery stay unchecked), and each residual that D7 leaves.
A13. [test, high] The gates 1-5 pass, and gate 7 passes on all four OS after the merge.

## Riskiest assumption

On an IPv4-only host (IPv6 addresses link-local only, no IPv6 default route), including link-local in D4 does not make the guard refuse its ordinary changes: the global-source cells of the `[::]` leg have no reply route and are skipped by `standIn`, and only link-local sources are evaluated. Evidence: `standIn` skips a wild source in a cell of a route without a path that no reply reaches (`internal/modules/firewall/guard/flows.go:65`, `route.go:121`); unverified on a captured IPv4-only host — the plan's first task verifies it with a fixture of such a host before D4 is built on it. If wrong, D4 fails closed on every IPv4-only host.

## Out of scope

The accepted `forward host` residual risk (D7); NPM's own admin hardening; changing the confirm window; a harness scenario with a link-local-only IPv6 VM (D8).

## Self-review (round 1 of 1)

- Placeholders: none; each file and symbol was read this session.
- Contradictions: D3 holds the `docker` lock in an NPM action; the registry rule that a repair action takes only locks its parent holds (`internal/core/registry.go:412`) concerns repair actions, and the undo actions here hold the same keys as their parents.
- Ambiguity: A1 and A2 name the refusal code and the content of the detail; A4 and A5 split the undo of a finished upgrade from rollback and recovery.
- Scope: six fixes in three modules plus the threat model; independent of each other except D1 and D3, which share `network_claims.go`.
- Acceptance: A12 is `demo` because no script reads the threat model; A1 is rated medium: the fix follows from the verifier's reading, but its operational cost (a stopped container named by a proxy host blocks attach and upgrade) is a decision for the spec review, asked with A1's Q.
- Riskiest assumption: the one above; the plan verifies it first.
