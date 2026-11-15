# Verifier of the security re-review after w12g/w12h — judge-opus, commit af933cf5

(reply saved verbatim)

Both items check out against the code. **Item 1:** the chain holds, but I grade it **low**, so it does not keep rows 1, 176 and 191 PARTIAL. **Item 2:** confirmed, **medium**, not owner-accepted, so row 192 stays PARTIAL. I read files only and changed none. Statements about nginx and Docker behaviour are marked as inference.

## Item 1: the reconcile task remembers external-network hits

**(a) Does the chain hold: confirmed.**
- **Pass:** `reconcileHits` finds the hit (`reconcile.go:265`) and `tc.Apply` runs the action through `ApplySystem` (`lifecycle/system.go:31-40`).
- **Re-plan before the lock:** `begin` calls `recheck` (`apply.go:193`), which re-runs Plan and compares the state hash (`apply.go:334`). The lock is taken only after that, at `apply.go:231`. So the name lookups must succeed for the pass, its Plan and the recheck, then fail for Apply alone.
- **Apply:** `forwardAnswers` returns an error on any name that gets neither an answer nor a definite not-found (`network_claims.go:1274-1275`). `reconcileScan` then drops every answer and compares only literal addresses (`reconcile.go:94-101`). The handler finds no name hit, drops nothing and returns success (`reconcile.go:163-211`).
- **Verify:** it scans again, finds the hit and returns `npm.reserved`. That error is not marked Definite (`reconcile.go:233-240`; Verify runs at `apply.go:394-395`).
- **Recording the failure:** `failApply` (`apply.go:402-403`) runs settle with no effects journaled. The action has no Recover handler, so `failSettled` calls `failure` (`recoverer.go:39`), which ends in `StateFailed` with the lock released. The error is built by `changeError` (`apply.go:459-472`), which keeps the `npm.reserved` code (`settle.go:103-109`).
- **Back in the task:** the view comes back synchronously (`apply.go:103-104`). The task turns `v.Error` back into an `npm.reserved` error and stores the key whatever network the hit is on (`reconcile.go:285-290`).
- **Later passes:** they return at `reconcile.go:280-282`. That return counts as a successful pass, so the backoff resets and the next pass waits the full 5 minutes. Suppression lasts until the hit set changes, a pass sees no hit, or the agent restarts.
- **One exception:** if the answer arrives after the engine's synchronous answer timeout (`SyncAnswer`, `apply.go:113-115`), the task sees state applying, remembers nothing, and no suppression follows.

**(b) Other ways Verify can find a hit Apply missed.**
- **A name answering differently:** if a name answers address A at the pass, Plan and recheck, B at Apply and A again at Verify, the key is the same and the same suppression follows (confirmed in code). This only works with a name whose answers someone can steer.
- **An Apply hit dropped while another appears:** this heals itself. The network Apply dropped leaves the next pass's hit set, so the key differs and the task applies again; the failed pass also retries after a 1-second backoff (`background.go:181-183`). This is the only case the row 217 ruling ("the next pass sees a different hit key") is right about.
- **Literal hits cannot do this:** literal addresses are always compared (`network_claims.go:1148-1149`), and Apply drops them or stops NPM. So suppression needs a name hit.
- **Docker names cannot produce a name hit:** `dockerMembers` leaves out NPM's own container (`proxyhost_add.go:342`). It maps a name only to the address its own holder has on one of NPM's networks, and that holder cannot share an address with NPM on the same network (inference about Docker's allocator). So Codex's Docker-alias framing does not apply; an overlapping-subnet setup was not established (plausible at most).
- **What remains:** names answered by the host's resolver, meaning a DNS zone the caller controls or `/etc/hosts`. That is the risk D10 and `threat-model.md:347` accept.

**(c) What an `operate` caller can do, and the consequence.** The caller forwards a proxy host by a DNS name they control to NPM's address on an external network. They then make some forwarded name fail to resolve for exactly the Apply lookup. A DNS server that counts queries could do that; this is inference, and the host resolver's caching may get in the way. After that, NPM stays on that network indefinitely, and nginx forwards the caller's public host to NPM's ports 81 and 3000 (inference about nginx), which is the row 1 attack.

The same caller gets the same lasting exposure without the trick: point the name at NPM's `darkpanel-npm` address. Reconcile never takes NPM off `darkpanel-npm` (`reconcile.go:171-173,204-207`) and remembers that hit by design. So the defect gives the caller nothing beyond what is already accepted.

**(d) Is it owner-accepted: no, not as such.**
- D3 limits the remembered hit to `darkpanel-npm`, and the comment at `reconcile.go:257-260` says the same; the code at `:289` does not hold to it.
- The row 217 ruling rests on the false "different key" premise.
- The exposure itself falls within D10 / `threat-model.md:347` (caller-controlled names), plus the `darkpanel-npm` exception that U3 accepts.
- The sentence at `threat-model.md:297`, "takes NPM off that network within a pass", is inaccurate here.

**(e) Severity and rows: low.** By the rubric this is "a threat-model claim that is inaccurate without being exploitable" beyond accepted risk. I agree with Fable over Codex: Codex's medium depends on Docker-alias hits, which the code does not produce. The fix is still worth making: store the key only for a Definite failure, or only when every hit is on `darkpanel-npm`.

Resulting verdicts:
- **Row 1: CLOSED.** The attach interval Codex also lists is accepted under U2.
- **Row 176: CLOSED.**
- **Row 191: CLOSED.** Codex's other row 191 residuals are accepted under U3 and `threat-model.md:297`.

## Item 2: a docker action after the upgrade's last check

**The interleaving: confirmed.**
- **Locks:** the upgrade takes only `npm` (`instance_upgrade.go:60`). `docker.container.stop` (`docker/container_run.go:30-41`) and `docker.network.connect` / `disconnect` (`network_connect.go:25,32`) take only `docker`. They refuse only containers or networks another module owns (`network_connect.go:64-68`), and an ordinary container on a user network is not owned.
- **After the last check** (`instance_upgrade.go:256`), nothing reads the unanswered names again. The steps run stop, archive, remove, create, connect and start (`:349-388`).
- **`verifyHeld`** resolves only names that were recorded with an answer. A name that now gets a definite not-found is skipped as holding nothing (`network_claims.go:1273`, `instance_upgrade.go:528-544`). A lookup that gets no answer at all marks the start unverified, which fails the "verify" step and also leads to rollback (`:395-403`).
- **The rollback** (`fail` → `rollback` → `restore`, `:290-329`, `:906-964`) restores the same volumes, so the same configuration. It starts the original (`:942`) and runs `verifyStart`, which does not wait for the API when targets were recorded (`:795-798`). Nothing checks unanswered names or nginx's health.
- **The outcome:** the new container's nginx fails to load and the restored original fails the same way, so every proxied site is down, the panel exposure included (inference, from D4 / row 192). If port 81 is served by nginx, the 120-second wait fails and the rollback runs (inference).
- **The window:** from the lookup at `:256` to the start, which includes the stop and up to 256 MiB of archive copying, so seconds. A stop that lands too early only produces a clean refusal, so the caller can retry.

**The undo of a finished upgrade: same window, confirmed.**
- The check runs at `upgrade_recover.go:40`. The restore then force-removes the upgraded container and starts the original (`:43` → `instance_upgrade.go:916-944`) with no further check.
- Here no second rollback follows, so NPM stays down until the name answers again and nginx is started again (inference).
- Adjacent (low): an undo retried after a cut-off one skips the unanswered refusal entirely (`upgrade_recover.go:84-91`). Reaching that needs an interruption, which a caller cannot cause.

**Sibling with no docker action at all (confirmed in code, nginx outcome inference):** a custom location that forwards to a DNS name the caller controls, made to stop resolving after `:256`, has the same effect. D10 accepts DNS re-pointing as an address risk, not this outage.

**Severity: medium.** An `operate` caller can take down every proxied site; the impact is availability only.

**Owner-accepted: no.**
- `threat-model.md:300` promises an up-front refusal that leaves NPM running.
- `threat-model.md:304` records a stop during an upgrade only for its address effect.
- D4, U2, U3 and D10 do not cover it.

**Row 192: PARTIAL.** Both reviewers agree. It closes by a fix, such as an nginx health check, or the unanswered check again after the start and before the rollback's start, or by a recorded owner acceptance.

## Verdicts

| Row | Verdict | Deciding point |
|---|---|---|
| 1 | CLOSED | Item 1 is low |
| 176 | CLOSED | Item 1 is low |
| 191 | CLOSED | Item 1 is low; the rest is accepted under U3 |
| 192 | PARTIAL | Item 2 is medium, not owner-accepted |

Low follow-ups, not owner-accepted:
- The memory at `reconcile.go:289` should be limited to `darkpanel-npm` hits, as D3 says.
- `threat-model.md:297` "within a pass" is inaccurate.
- The premise of the row 217 ruling is wrong for an identical key.
