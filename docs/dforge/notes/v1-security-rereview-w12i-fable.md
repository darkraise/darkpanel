# Security re-review after w12i — Fable, commit 9a9388ab

Method note: I had no shell, so `git log` and `git show` were not run. `.git/HEAD` names `main` and `.git/refs/heads/main` holds `9a9388ab7576a91ab00ed541d94e95f07834ec0b`. I reviewed the w12i commits through the code, tests, register notes and threat model at that commit, not through diffs. "Fails on revert" is judged by reading each test against the earlier behaviour as the register rows and the w12g reports describe it. Statements about nginx, s6, the Docker Engine or DNS are marked as inference.

## Row 192
- Verdict: CLOSED
- Acceptance: `docs/security/threat-model.md:300` states the residual as the code has it and ends with "The owner accepted this on 2026-11-15 (w12i)", which covers all four parts. The register row records the same owner ruling.
  - **The window:** the last unanswered check is the `forwardCheck` after the pull (`instance_upgrade.go:256`). Only the journal entry and the upgrade row sit between it and the stop (`:265-269`, `:349-352`), so "runs right before NPM is stopped" holds. Nothing reads unanswered names afterwards: the verification after the start compares only recorded targets, and a name with no answer is not recorded (`:260-261`, `:395`, `:516-545`).
  - **The rollback:** `fail` runs `rollback`, which restores the original from the same configuration and archives (`:290-305`, `:906-964`). With a recorded target list `verifyStart` does not wait for the API (`:793-812`), so the rollback ends `rolled_back` with nginx down (the nginx outcome is inference, as the text says).
  - **The undo of a finished upgrade:** `upgrade_recover.go:40-46` checks, then runs the rollback, with nothing after it.
  - **The retried undo:** `upgrade_recover.go:74-91` takes the recorded-targets branch when the container named `darkpanel-npm` is not the one the instance row names, and that branch has no unanswered refusal.
- Residual: an `operate` caller stops or disconnects the container a custom location's static name answers to (or a DNS name stops answering) after the check after the pull. The recreated NPM and the restored original cannot load the location, so every proxied site and the panel exposure stay down until the name answers. The undo has the same window after its check, and a retried undo skips the up-front refusal. Code paths confirmed by reading, nginx outcome inference; medium (availability); owner-accepted yes, `docs/security/threat-model.md:300`.

## w12i rows
- 217: present yes; meets the row yes; regression test reproduces yes and fails on revert yes.
  - `reconcile.go:289` remembers a key only when the code is `npm.reserved` and `onOwnNetwork(hits)` (`:305-314`) holds for the pass's own scan.
  - I walked the mixed cases (own-network and external hits, Apply or Verify failing, the scan's lookups failing). The memory can only suppress a pass whose hit set is exactly a remembered all-`darkpanel-npm` set, and Apply does nothing for such a set but fail.
  - `TestW12gReconcileRetriesAnExternalHitWhoseVerifyFailed` (`reconcile_task_test.go:246`) fails the lookup of the first pass's Apply only, so Verify fails `npm.reserved`. Under the old rule the second pass would skip (`applies` stays 1, NPM stays on the network) and the test fails.
  - `w12gTask` now runs Verify after Apply (`:34-37`).
- 226: present yes; meets the row yes for the listed sentences, with one inaccuracy left in a rewritten sentence; no test applies.
  - Corrected and matching the code:
    - the 10-second disconnect and the apply deadline (`threat-model.md:298`; `network_claims.go:917`, `:1010`);
    - "normally within 5 minutes; a pass skipped for a held lock, or failing, waits longer" (`:298`; `background.go:172-194`);
    - the reconcile memory (`:297`);
    - `PUID:PGID` or root (`:284`; `nginx_exec.go:102-104`);
    - "checked again only by the passes of `npm.reconcile`" (`:295`, `:348`).
  - Spec D3's ordering sentence is left, and the register says so.
  - The remaining inaccuracy (line 348) is under Other observations.
- 227: present yes; meets the row yes; regression tests reproduce yes and fail on revert yes.
  - A start error that is not a 4xx, any start error at an ended deadline, and an inspection error with the deadline alive now set `pending` (`nginx_exec.go:177-183`, `:188-192`).
  - The baseline is taken after `nginx -t` (`:78-84`).
  - A worker counts only when its PPID is the PID of a process titled `nginx: master process` (`:153-168`).
  - `TestNginxReloaderKeepsAnExecPendingAfterAnUncertainAnswer` (both cases) gets a non-busy error from the second reload under the old code. `TestNginxReloaderNeedsANewWorkerOfTheMasterAfterTheSignal` (both cases) is confirmed under the old code.
  - The bare `nginx` name and the worker restarted for another reason are owner-accepted at `threat-model.md:299`.
  - Not pinned: that a refused (4xx) start is not kept pending.
- 228: present yes; meets the row yes; regression test reproduces yes and fails on revert yes.
  - `joinGuard` reads the targets, then the network, then applies the rules (`network_claims.go:889-910`). Only the journal's `List` and `Intend` sit between it and the connect (`effects.go:104-115`).
  - `TestA1_AttachMakesNoListingOrLookupBetweenConnectAndInspection` requires `inspectnet` as the last event before the connect; the old order ends on a lookup.
  - It pins the attach only. The undo of a detach shares `joinGuard` and has no order assertion of its own (`TestW12gUndoOfADetachChecksInTheSameOrder` does not log the network read).
- 230: present yes; meets the row yes; regression test reproduces yes and fails on revert yes.
  - `network_claims.go:397-405`: a 4xx with NPM on the network falls through to `afterJoin`.
  - `TestW12gAttachChecksANetworkNPMIsOnWhenTheConnectIsRefused` makes the connect join and answer 403; the old code returns the 403 and leaves NPM at the forwarded address.
  - Covered: the failing check only. Not covered: the passing check (claim and effect recorded), NPM not on the network, a failed inspection.
- 231: present yes; meets the row yes; regression test reproduces yes and fails on revert yes.
  - `joinGuard` applies `proxy.Target` to the re-read network (`network_claims.go:898-901`, `proxy/target.go:15-20`).
  - `TestW12gUndoOfADetachRefusesANetworkThatIsNoLongerABridge` swaps the network for a macvlan one of the same name; without the rule the undo connects and returns nil.
- 234: present yes; meets the row yes; no test applies. The comment at `network_claims.go:1126-1130` now says a name re-pointed between the first reload and the read leaves nginx with the older answer until the second reload (D10).
- 236: present partly; meets the row partly; no code to revert (test-only).
  - Fixed: `w12gWantLostJoin` asserts not Definite and the Engine error's code (`w12g_attach_test.go:349-362`); the undo caller has its own test (`:364`); `w12gTask` runs Verify.
  - Unchanged: `TestA11` "lock held" still compares declared lock keys (`w12g_security_followup_acceptance_test.go:968-982`). The behaviour is covered by `TestBackgroundSkipsHeldLock` (`internal/agent/runtime/background_test.go:342`), which I read and which does skip on a held key.
  - Unchanged: `sqPass` still runs Apply without Verify (`:712-726`).
  - A6, A20 and `TestW12gAttachDeadlines` are as they were, recorded in the register row as owner-accepted at design. The register is the only place that acceptance is written.
- 229 (accepted): recorded yes, accurate yes — `threat-model.md:298` ("When a connect's answer is lost and the one inspection after it fails or does not yet show NPM on the network ..."), matching `lostJoin` (`network_claims.go:952-957`). I did not re-read that the change ends in `needs_repair` and keeps the lock; the w12g report cites `lifecycle/settle.go` for it.
- 232 (accepted): recorded yes, accurate yes — `threat-model.md:297`; `npmapi/client.go:26,232` (4 MiB). A failed listing fails `reconcileScan` (`reconcile.go:90-93`) and makes each guard refuse.
- 233 (accepted): recorded yes, accurate with one omission — `threat-model.md:301`; `network_claims.go:413-420`, `:1006-1017`. The text says NPM is disconnected "when either fails" (the held-address check or the reload). The code also disconnects when a check could not be made: the inspection, or the second listing or lookups (`:917-923`, `:939-942`). Low.
- 235 (accepted): recorded yes, accurate yes — `threat-model.md:300`, last sentence; `upgrade_recover.go:84-91`.

## New findings in the w12i changes
- **Refused connect, what an `operate` caller gains:** nothing new found. The docker module refuses a connect of a module-owned container (`internal/modules/docker/network_connect.go:64`), so the join "made in between" has to come from outside Darkpanel. The branch then either disconnects NPM (failed check) or records the claim. Confirmed by reading; no finding at medium or above.
- **Refused connect skips the static-address rule.** `check` refuses an attach whose `address` differs from the one NPM holds (`network_claims.go:304-312`). A connect refused with NPM on the network at another address passes `afterJoin`, and the attach answers success with a claim while NPM holds an address other than the one asked for. Confirmed by reading; low (correctness; needs an outside join between the check and the connect); `network_claims.go:400-408`.
- **Refused connect adopts a join it did not make.** The claim is written with `preexisting=false` (computed while NPM was off the network, `:327`) and the endpoint effect is journaled as made with a null Before. A later detach, or the undo of this attach, then disconnects NPM from a network something else put it on. Had the outside join come a moment earlier, `check` would have marked the claim preexisting. Plausible; low; `network_claims.go:393-409`, `:422-423`.
- **Refused connect is a second Definite refusal after a host change.** A failed `afterJoin` disconnects NPM (`leave`); `run` records the effect skipped and returns Definite (`effects.go:117-125`). The accepted text (`threat-model.md:301`) names only the attach on a network with no claim. This path can run on a network that has claims, and neither it nor its twin in the undo of a detach (`network_claims.go:838-848`) is in the text. Confirmed by reading; low (threat-model gap, same class as row 233).
- **`joinGuard`'s order and network rule:** no defect found.
  - Attach: the network is re-read by id, so a network removed and created again is refused before the connect; the rule duplicates `check`'s there.
  - Undo of a detach: the re-read is by name and now refuses any driver but a user-defined bridge, Definite, before the connect.
  - Targets cannot change through Darkpanel between their read and the connect (the `npm` lock). A name re-pointed in that gap is the accepted U2/D10 interval.
  - Cosmetic: in the attach, a failed re-read names the network by its id in the error (`network_claims.go:389`, `:896`). Low.
- **Pending rule, "busy forever":** not by the code's own doing.
  - `pending` is cleared when the Engine answers 404 or not running (`nginx_exec.go:63-73`), and an agent restart clears the field. An exec whose start never reached the Engine reports not running (inference from the Engine's exec state).
  - A reload stays busy for exactly as long as the Engine reports that exec running; the reloader has no expiry. An exec whose exit the Engine missed (inference) would block every join with `npm.unhealthy` until the agent restarts or the container is recreated.
  - I found no caller-controlled way into that state. Plausible; low; `nginx_exec.go:63-73`.
- **Pending rule, opposite direction:** a start whose answer was lost, and which the Engine has not yet begun when the next reload inspects it, reads as not running. It is forgotten and can then run beside the new reload. Needs a start in flight across two calls. Plausible (inference); low; `nginx_exec.go:64-72`.
- **Master-parent rule, "never confirms":** not for a respawned master.
  - Masters are read from each listing, so workers of a new master count as new (`nginx_exec.go:154-168`). That is a confirmation the signal did not cause, the class `threat-model.md:299` accepts.
  - The same holds for late workers of an earlier, unconfirmed reload that appear after the next reload's baseline (inference: the master applies a reload asynchronously).
  - Not evidenced: that on the pinned image and the supported hosts `docker top` shows the master's title starting `nginx: master process` and the workers' PPID equal to its PID. The recorded harness run is the w12e one (`threat-model.md:304`), from before this rule. If either assumption failed, every reload would fail closed and every join be refused. Plausible; low (availability); `nginx_exec.go:153-168`.
- **Reconcile memory:** no defect found (see row 217). One consequence: a Verify failure on an external hit now fails the pass every time. While Apply's lookups keep failing and the scan's and Verify's answer, one failed `npm.instance.reconcile` change is made per backoff step (at most one per 5 minutes, `background.go:22-23`), and wakes are ignored while the failure count is above zero (`background.go:190-193`). Plausible; low; `reconcile.go:284-293`.

## Other observations
- **`threat-model.md:348` overstates reconcile.** It says a re-pointed name "can later resolve to an address that was refused at apply until the next pass whose lookups answer". The passes compare only the addresses NPM holds (`reconcile.go:112-121`). A name re-pointed to the panel gateway, which apply refuses (`proxy/upstream.go:55`), is never checked by any pass. A name re-pointed to NPM's address on `darkpanel-npm` is reported once and left (`reconcile.go:204-208`, `:289-291`). Line 295 states it correctly. Confirmed; low.
- **`threat-model.md:297`, "takes NPM off that network and reloads nginx".** When that reload fails (busy, test failure, Engine error) the change fails after the disconnect (`reconcile.go:193-203`). The next pass finds no hit and returns without a reload (`:271-274`), so nginx keeps its old resolution until something else reloads it. Predates w12i; the wider pending rule makes a busy reload more likely. Confirmed by reading; low.
- **Two w12i behaviours are missing from the mitigations:** the undo of a detach refuses a network that is no longer a user-defined bridge, and a refused connect with NPM on the network is checked. Omission, not an inaccuracy. Low.
- **No w12i evidence note or revert check is recorded.** w12g had a `## Revert check` section that `TestA23` enforces (`w12g_security_followup_acceptance_test.go:1426`). The register names the tests; the revert results above are by reading only. Low.
- **Test gaps:** no case for a 4xx start staying not pending (`nginx_exec_test.go:253-276`); the refused-connect test covers one of four outcomes; `TestA11`'s "lock held" subtest still asserts lock keys only; `TestW12gAttachDeadlines` still compares constants. Low.
- **Wording:** `Apply`'s doc comment says a join "reads the network and the forward targets again" (`network_claims.go:367-369`), the reverse of the order now used. "; The owner" is capitalised after a semicolon at `threat-model.md:297` and `:298`. Cosmetic.

Intended report path, not written: `/root/repositories/darkpanel/docs/dforge/notes/v1-security-rereview-w12i-fable.md`
