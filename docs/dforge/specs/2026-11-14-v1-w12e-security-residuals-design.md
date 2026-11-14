# v1 w12e: close the residuals of the 2026-11-14 panel re-review

**Source:** owner request 2026-11-14 "fix them", on the panel re-review of security rows 1 and 6 at 10a0973a (`docs/dforge/notes/v1-security-rereview.md`, reports `v1-security-rereview-w12d-{fable,codex,verifier}.md`) and the M3 audit rerun at b509e772 (`.dforge/milestone-M3-audit-rerun-report.md`).
**Covers rows:** `docs/dforge/registers/2026-11-06-v1-security-review.md` #1, #6, #142, #176, #177, #178, #179; `docs/dforge/registers/2026-10-21-v1-completion.md` #37 (R73).
**Execution strategy:** sequential — inline execution
**Depth:** full — risk

## Decisions

Quoted answers of the owner: "fix them" (the six findings the re-review left open, and the threat-model sentences they make inaccurate); "Full dforge flow (Recommended)" (the process question, 2026-11-14). The approach choices below are this session's recommendations after the architecture critic's round (`.dforge/critic-2026-11-14-v1-w12e-security-residuals-design-report.md`), put to the owner at the spec review.

- **D1, residual A (row 176): remember the addresses nginx holds.** nginx resolves a custom location's static `proxy_pass` when it loads its configuration and keeps the answers while the name has none, or has moved: the premise of `forwardTargets` (`internal/modules/npm/network_claims.go:823-826`) that a name with no answer "can reach NPM only after it is re-pointed" is false, for a name with no answer and for one that now answers another address (the critic's variant: the alias gains a holder on a network NPM stays on). The agent keeps, in a new table `npm_forward_seen` (migration `0003`), the addresses each forward name answered when Darkpanel last wrote to NPM, and `forwardTargets` adds those addresses to the targets of the names NPM's proxy hosts and locations forward to now, beside the answers read now. Every Darkpanel write that makes NPM reload nginx (`npm.proxyhost.add`, `update`, `remove` and their undos) replaces the table before the write: for each name forwarded after the write, the answers read then, or the earlier record when the name has no answer then (nginx fails to resolve it and keeps its earlier workers); a name no proxy host forwards is dropped. An upgrade rebuilds it from the answers read after the recreate, with no earlier record kept, because nginx starts afresh. A host with no record reads as today (current answers only), until its first write. Rejected: refusing a join while a name has no answer (the critic's High: it leaves the re-answered variant open and blocks `panel.exposure.add`'s attach step while any proxy host names a stopped container); accepting A as part of the `forward host` residual (an alias is not a DNS name, and the accepted text at `docs/security/threat-model.md:338` says DNS); restarting NPM so nginx re-resolves (it interrupts every proxied site). A write through NPM's own admin UI is not recorded; it needs the NPM administrator.
- **D2, residual B (row 177): check the undo of a finished upgrade.** `Inverse` of `npm.instance.upgrade` in phase `done` runs a forward check before `rollback` and `restore`, on what `restore` recreates: the networks and static addresses of the upgrade row's `Config` (`instance_upgrade.go:181-207,683-689`) and `darkpanel-npm`, with the upgraded container's addresses counted as free. The reason of ruling W12a-R1, that a refusing rollback would leave NPM down, holds for the rollback of a failed upgrade and for recovery, which stay unchecked; it does not hold for a requested undo of a finished upgrade, where NPM is up on the new image.
- **D3, residual C (row 142): a late check in the upgrade, no lock.** The upgrade runs a second forward check after the archives and immediately before it removes the old container, so the window in which an `operate` caller can free a forwarded address shrinks from the pull, the stop and the archives (minutes) to the remove, create and connect (seconds), and the old container still exists to be restarted by the rollback if the late check refuses. The risk becomes the raced one the threat model already lists ("an address another container held when it was checked can be freed before NPM connects"), no longer one a caller forces. Rejected: adding the `docker` lock key to attach, detach and upgrade (the critic's High: `takeLocks` bumps the epoch of every key taken, so any Docker action would close the undo window of those actions, and a composite unwind such as `panel.exposure.add`'s would end in `needs_repair`; the Cloudflare and panel network actions free addresses under other keys anyway).
- **D4, row 6: the guard builds the IPv6 leg of a `[::]` binding on a link-local-only host.** Only `npmPublicFlows` (`internal/modules/firewall/guard/flows.go:420`) stops excluding link-local addresses; `wildcardDsts` (`guard/route.go:245`) is not changed, because it already keeps the reply route's preferred address (`route.go:249`), a link-local one on such a host. The flow is `standIn`, so a cell whose reply has no route is skipped. Rejected: also dropping the exclusion in `wildcardDsts` (the critic's High: it changes every IPv6 wildcard flow on every host and probes the link-local addresses of `docker0` and every veth, which security rows 27 and 28 left out on purpose); an `unknown` flow, which would refuse every guarded change on every IPv4-only host.
- **D5, row 178: the Tailscale link guard protects the visible client of an NPM exposure.** For a request through an exposure whose client is not a host address, the guard adds the client's flow to each public binding, as `linkListenFor` does for a direct client. A client that is a host address (a tailnet client that Docker's userland proxy carries) is the hidden client; it gets no flow, and the threat model names that case as a residual, recorded as a new low register row. Rejected for now: a stand-in tailnet flow per binding for the hidden client, which needs its own design in `linkguard.go`.
- **D6, row 179: cut the forward host at its first `/` in `checkRestoredLocation`** (`internal/modules/npm/proxyhost_edit.go:241`), as `forwardTargets` does.
- **D7: the accepted DNS risk stays.** A caller who controls a DNS name that nginx's resolver answers differently from the agent's can still publish NPM's own API (Known residual risks, `forward host`); D1 to D3 close the paths that need no such name, including a Docker alias the caller controls. The threat model says so next to each fix.
- **D8: no harness change.** The fixes are guard and lifecycle logic covered by unit tests; D4's tests are built from route answers and addresses captured read-only from real hosts (`ip -6 addr`, `ip -6 route`, `ip -6 route get fe80::1`) rather than written by hand; gate 7 reruns on the merge.

## What changes

| Fix | Files |
|---|---|
| D1 | `internal/modules/npm/migrations/agent/0003_npm_forward_seen.sql`, `internal/modules/npm/store.go` (or a new `forward_seen.go`), `internal/modules/npm/network_claims.go` (`forwardTargets`), `internal/modules/npm/proxyhost_add.go`, `proxyhost_edit.go` (the record before each write), `internal/modules/npm/instance_upgrade.go` (the rebuild after the recreate) |
| D2 | `internal/modules/npm/upgrade_recover.go`, `internal/modules/npm/instance_upgrade.go` (`forwardCheck` takes the networks and static addresses it checks) |
| D3 | `internal/modules/npm/instance_upgrade.go` (`steps`) |
| D4 | `internal/modules/firewall/guard/flows.go` |
| D5 | `internal/modules/tailscale/linkguard.go` |
| D6 | `internal/modules/npm/proxyhost_edit.go` |
| threat model | `docs/security/threat-model.md` lines 215, 229, 281, 292, 295 and the residual-risk list |

The registers: rows 176, 177, 142, 178, 179 get the plan as Assigned and, once merged and re-reviewed, `verify`; a new low row records the hidden tailnet client (D5).

## Acceptance

A1. [test, neg, high] After a proxy host forwarding to the name `up.invalid` is written while `up.invalid` answers address A, `npm.network.attach` of NPM to a network at A (static, or Engine-chosen) is refused with `npm.reserved` naming the proxy host and the name, once the name has no answer any more, and once it answers another address instead.
A2. [test, high] The undo of a detach and `npm.instance.upgrade` (plan and apply) read the same record and refuse the same way.
A3. [test, high] A write replaces the record: a name that answers keeps its new answers only; a name with no answer keeps its earlier record; a name no proxy host forwards is dropped; an upgrade rebuilds it from the answers read after the recreate. A host with no record gets the targets it gets today (the first half of `TestW12aAttachResolvesForwardHostNames` stays green).
A4. [test, neg, high] The undo of a finished `npm.instance.upgrade` is refused with `npm.reserved` when a proxy host forwards to an address the restored container could take on the networks of the upgrade row's `Config` or on `darkpanel-npm`, including a network the upgraded container has left since; the new container keeps running and the change stays `done`.
A5. [test, high] The undo of a finished upgrade with no such forward restores the original as today, and the rollback of a failed upgrade and recovery run no forward check.
A6. [test, neg, high] An upgrade whose proxy host gains a forward to a free address between the early check and the old container's removal fails at the late check with `npm.reserved`, and the rollback restarts the original container, whose volumes and networks are unchanged.
A7. [test, neg, high] On a host whose only non-loopback IPv6 addresses are link-local, a change that drops IPv6 TCP 443 is refused without the override when the request came through an NPM exposure with a `[::]:443` binding, naming the exposure and the blocked range.
A8. [test, high] On that host a change that touches no IPv6 rule passes, and on a dual-stack host with `docker0` and two veths holding `fe80::` addresses the number of probes of an unrelated change is unchanged, as are the guard's existing tests on a host with a global IPv6 address and on a host with no IPv6 address.
A9. [test, neg, medium] On an IPv4-only host (link-local IPv6 only), a change that sets the IPv6 INPUT policy to DROP or turns IPv6 off in UFW is refused without the override when the request came through an NPM exposure whose client is a host address. Q: is that refusal acceptable? It is the usual first hardening step on an IPv4-only VPS and the override applies; the alternative is that the guard passes it, which leaves row 6's gap open for a link-local client.
A10. [test, neg, high] For a request through an NPM exposure whose client is a tailnet address, a change that takes the Tailscale link down is refused by the Tailscale link guard without the override; a client that is a host address adds no flow.
A11. [test, neg, high] Undoing a proxy host update or removal is refused with `npm.reserved` when a restored location's forward host is an NPM address followed by `/` and a path.
A12. [test, high] Every fix has a named regression test that fails when the fix is reverted: `TestW12eAttachRefusesWhatNginxStillHolds`, `TestW12eForwardSeenFollowsTheWrites`, `TestW12eUpgradeUndoChecksForwards`, `TestW12eUpgradeLateForwardCheck`, `TestW12eLinkLocalOnlyHostGuardsIPv6443`, `TestW12eTailnetClientOfAnExposure`, `TestW12eRestoredLocationCutAtSlash`.
A13. [demo, high] `docs/security/threat-model.md` says only what the code holds: lines 215 and 229 (the IPv6 leg on a link-local-only host), line 281 (a name's remembered addresses count), lines 292 and 295 (the undo of a finished upgrade is checked, the upgrade checks again before it removes the container, the freed-address risk is a raced one), the hidden tailnet client, an NPM write made through NPM's own admin UI that is not recorded, and what D7 leaves.
A14. [test, high] Gates 1-5 pass, and gate 7 passes on all four OS after the merge.

## Riskiest assumption

On an IPv4-only host (IPv6 addresses link-local only, no IPv6 default route), the one-line change of D4 does not make the guard refuse its ordinary changes: the global-source cells of the `[::]` leg have no reply route and are skipped by `standIn`, and only link-local sources are evaluated. Evidence: `standIn` skips a wild source in a cell of a route without a path that no reply reaches (`internal/modules/firewall/guard/flows.go:65`, `route.go:121`); how the kernel answers a route lookup for a link-local source with no output interface is unverified — the plan's first task captures `ip -6 addr`, `ip -6 route` and `ip -6 route get fe80::1` read-only from a real IPv4-only Docker host (this host, if it is one) and builds the fixture from the capture before D4 is built on it. If wrong, D4 fails closed on every IPv4-only host, and A9's question widens to every guarded change.

## Out of scope

The accepted `forward host` residual risk (D7); the hidden tailnet client of D5; an NPM write through NPM's own admin UI; NPM's own admin hardening; the confirm window; a harness scenario with a link-local-only IPv6 VM (D8).

## Self-review (round 1 of 1)

- Placeholders: none; each file and symbol was read this session or by the critic.
- Contradictions: none after the critic's round; D3 no longer touches locks.
- Ambiguity: A1 and A2 say which record they read; A4 and A5 split the undo of a finished upgrade from rollback and recovery; A6 names the late check's failure path.
- Scope: five fixes in three modules plus the threat model and one migration; D1 is the one design with persistence.
- Acceptance: A13 is `demo` because no script reads the threat model; A9 is medium with its question; the NPM template question (whether a proxy host's own forward host is resolved per request, so only custom locations hold stale addresses) is settled in the plan's second task by reading the pinned image's templates, and only narrows what D1 records.
- Riskiest assumption: the one above; the plan verifies it first.

## Critic findings

Critic: 0 Blocker, 3 High (3 fixed), 8 Medium, 1 Nit. One change: adopt the recorded-addresses design in place of "refuse while a name has no answer".

- [High] D1 left a re-answered-name variant open — fixed: D1 is the recorded-addresses design; A1 covers both variants.
- [High] D3's `docker` lock key couples undo windows and turns composite unwinds into `needs_repair` — fixed: D3 is a late check, no lock; the `lock.held` cost, the panel and Cloudflare keys and A6's lock cases are gone with it.
- [High] D4's `route.go` change was not needed and reverses rows 27 and 28 — fixed: only `flows.go` changes; A8 adds the dual-stack veth case.
- [Medium] "every Docker mutation holds `docker`" is false — fixed by dropping the lock.
- [Medium] D2 checked the wrong networks — fixed: D2 and A4 check the upgrade row's `Config`, including a network the upgraded container left.
- [Medium] A4's example returned `undo_conflict` under D3 — gone with the lock; A4's frees now go through any route.
- [Medium] D1 refused more than needed and A1's question omitted `panel.exposure.add` — gone with the refusal; the NPM template question is settled in the plan.
- [Medium] D5 left the hidden tailnet client unprotected without saying so — fixed: D5, A10 and A13 name it as a residual with a new register row.
- [Medium] the riskiest assumption cannot be checked with a hand-written fixture — fixed: D8 and the assumption's check use captures from a real host.
- [Medium] D4 also refuses IPv6 hardening on IPv4-only hosts — fixed: A9 states it and asks the owner.
- [Medium] D3's lock cost had no queue — gone with the lock.
- [Nit] A3 says nothing of the second half of `TestW12aAttachResolvesForwardHostNames` — the second half pins the skip D1 removes and is rewritten to expect the remembered addresses; A3 now names what stays green.
