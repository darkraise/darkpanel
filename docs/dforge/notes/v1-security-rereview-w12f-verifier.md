# Verifier of the disputed residuals after w12e/w12f (dforge:judge-opus, read-only), commit 6c41959f

I found nothing that changes the panel's overall result: rows 1 and 176 stay PARTIAL on the attach window, and rows 6, 142, 177, 178 and 179 can count as CLOSED. Two of my gradings differ from the reviewers. V2 (Codex's high) is low, because the token only opens NPM on a throwaway test VM. V5 (Fable's "upgrade finishes done") is wrong as stated, but a real outage path sits underneath it. I edited and created no files, and ran no builds, tests or network commands.

## V1. The window between the attach's check and NPM's connect (rows 1 and 176)

- **Status:** confirmed in code. Two things are inference: that the Engine hands NPM the freed address, and that nginx forwards to it during the window.
- **Severity:** medium.
- **Owner-accepted:** no.

**Every step needs only `operate`:**
1. The caller connects a container they own to a network so that it holds address A. `docker.network.connect` has no minimum scope, so it defaults to operate (`core/scope.go:11`). The docker module refuses only module-owned objects (`docker/network_connect.go:64-68`).
2. The caller adds a proxy host forwarding to A:3000 (or A:81). This passes because A is not NPM's address yet.
3. The caller starts `npm.network.attach` with no static address. Apply runs the check again (`network_claims.go:368`), and the forward guard at `:320` passes because the caller's container still holds A. The plan hash covers only NPM's endpoint, the target and the claims (`:359`), not other containers.
4. `reloadBeforeJoin` runs (`:383`, up to 10 s, `nginx_reload.go:12`). During it the caller disconnects or stops their container.

**The locks do not exclude each other.** The attach locks `npm` (`network_claims.go:43`, `npm/deps.go:34`). The docker actions lock `docker` (`docker/network_connect.go:25,32`, `docker/container_run.go:41`, `docker/deps.go:16`). Locks are taken per key (`lifecycle/apply.go:182,231`).

**The window is real.** `ConnectNetwork` (`:388`) runs with nil settings, so the Engine picks the address. After that, `joinedProblem` reads the container, lists the proxy hosts and resolves every forwarded name (`:870-882`, `forwardTargets` → `forwardAnswers`, deadline `resolveTimeout+1s` at `:999`). Only then does it compare literal addresses (`:886-895`) and disconnect (`:857`).
- With no slow lookup, NPM sits on A for a fraction of a second.
- A name with no definite answer, from a proxy host the caller added earlier, stretches this to about 6-7 s. Disconnection still happens in the end, because the `unavailable` result still triggers `disconnectAgain`.
- The attach fails `npm.reserved` and can be repeated.
- `TestA3_…/"a held address that is a target"` (`w12e_security_residuals_acceptance_test.go:293-302`) models exactly this interleaving and asserts only the eventual disconnect.

**Why it is not accepted.**
- Spec D1 residual (b) covers a different race: a name that loses its answer between `nginx -t` and the reload being applied, "(not forced)".
- Spec D1's R20 explicitly rejects a design in which "each refused attach would publish NPM's port 81 between connect and disconnect, repeatably". That is this effect.
- `threat-model.md:298`'s "no caller can stretch it for a literal address" is about starts Darkpanel makes; for the attach, the literal check waits behind name lookups.
- `threat-model.md:306` covers the upgrade only.
- Neither line 299 (accepted, about old workers) nor the Known residual risks section (`:343-348`) covers it.

**Why medium.** The effect is row 1's admin-scope effect. But it lasts seconds per attempt, needs the caller to win a timing race and the Engine to choose A, and exposes only an authentication surface (the 32-byte admin password still stands, `threat-model.md:346`).

**Verdict:** this keeps rows 1 and 176 PARTIAL.

## V2. The NPM bearer token in the harness's curl commands

- **Status:** confirmed.
- **Severity:** low.
- **Owner-accepted:** no.

**What the code does.** `m6_npm_test.go:1073-1074,1100` puts `Authorization: Bearer <token>` into the curl command text. `vm.go:319` and `:342` put the whole command into the error. That error reaches `t.Errorf` at `m6_npm_test.go:1016` when the GET fails (curl `-f`) or the context ends. The token also appears in the VM's process list while curl runs. The password itself goes in over standard input (`:1062`), which is correct.

**What the token protects.** NPM's administrator inside a QEMU VM booted from a fresh overlay of a pinned image for each run (`vm.go:87,103`). NPM's port 81 is bound to the VM's loopback. Nothing real is at risk.

**Grading.** The rubric's "secret reaches an error" literally says high, but graded by what it breaks this is hygiene. Worth fixing by passing the header through standard input or a file.

## V3. reloadNginx can take an unrelated proxy host offline

- **Status:** plausible. NPM's rename is quoted from the image source (`w12e-evidence.md:262`); the timing is inference.
- **Severity:** medium (availability).
- **Owner-accepted:** no. `threat-model.md:301` states the mechanism with no "The owner accepted this", and register row 188 is graded low and `deferred`.

**Reachable by `operate`:**
- Attach and the undo of a detach (both operate) call `reloadNginx`.
- The caller's own proxy host forwarding to a Docker alias becomes a static `/` location (spec R1).
- The caller disconnects the alias's container between NPM's two `nginx -t` runs, using `docker.network.disconnect` on the `docker` lock.

**The victim** is whichever enabled host has the lowest id (`nginx_reload.go:30-39`), not one the caller picks. It can be an admin-only host or the panel exposure's reserved host. The join's refusal says nothing changed (`network_claims.go:838-842`).

**Recommendation:** raise row 188 to medium.

## V4. Starts Darkpanel does not make (row 142)

- **Status:** plausible (Docker's address behaviour across a restart is unknown; `threat-model.md:297` says so).
- **Severity:** medium. The effect is admin-scope, but it depends on an Engine or host restart the caller cannot trigger: `docker/container_run.go:65` refuses owned containers, and no action restarts the Engine.
- **Owner-accepted:** no. Codex is right.

**Why there is no acceptance.**
- Spec D3 (line 14) contains "Starts Darkpanel does not make … are not verified" as a statement of scope inside the D3 text, not as an owner quote.
- The owner's recorded answer is only the option label "Verify after join (Recommended)" (spec line 10). The question text does not exist in `docs/` or `.dforge/`.
- Contrast D1 residual (a), which says "the owner accepted it at the spec review (U4 (a))".
- `threat-model.md:297` has no acceptance mark, unlike `:299` and `:345-348`.

**Effect on row 142.** It does not keep row 142 PARTIAL. The path involves no upgrade, and A13 kept this sentence unchanged from before row 142 existed. It belongs under row 1, which is PARTIAL anyway, and in a new row asking the owner to rule.

## V5. "An upgrade finishes done while nginx cannot start"

- **Status:** refuted as stated (inference); a different outage is plausible.
- **Severity:** medium (availability).
- **Owner-accepted:** no.

**Why "done" is wrong.** The agent's API client is `http://127.0.0.1:81` (`npm/deps.go:50`). In the NPM image, port 81 is served by nginx, which passes `/api` on to the backend on port 3000. That is inference from the image layout, though consistent with the harness reaching `/api/tokens` on port 81. So an nginx that cannot start fails `probe` (`instance_upgrade.go:604-620`) and `Verify` (`:962`). The upgrade then rolls back after 120 s rather than finishing done.

**The outage that does exist:**
- On a resolver that answers a definite "not found", the checks skip the unanswered name (`network_claims.go:1066`).
- An operate caller can stop the forwarded container and run `npm.instance.upgrade`. It has no minimum scope, and the precheck at `:86-103` does not refuse a same-image run.
- That recreates NPM, and the restored original faces the same configuration (inference). The likely end state is NPM down with every proxied site down, the panel exposure included.

**What the spec recorded.** The spec's self-review (line 75) promised "if so, `forwardCheck` refuses the upgrade up front". `w12e-evidence.md:298` concludes nginx does not start, yet notes the late check refuses such a name "only when it can resolve it". The promise was dropped without a ruling. `threat-model.md:302` states that nginx does not start after a restart, with no acceptance.

## V6. One broken forwarded name blocks every new join and arrives as only "Unavailable"

- **Status:** confirmed.
- **Severity:** medium (availability) for the blocking, low for the hidden detail.
- **Owner-accepted:** partly. Spec D1's "Cost" sentence (line 12) covers refusal at the reload while a static name has no answer. Nothing accepts the resolver 503 or the hidden detail.

**What the code does.**
- Any lookup result other than success or a definite not-found refuses with `unavailable` (`network_claims.go:1060-1071`).
- `forwardTargets` reads every proxy host and location, disabled hosts included (no enabled filter, `:949-955`).
- The detail naming the host is dropped from 5xx answers (`agent/api/handler.go:871-877`). The harness saw `503 unavailable` (`w12e-evidence.md:326`).
- So it blocks attach, the undo of a detach, upgrade and the undo of an upgrade.

**Existing rows.** None covers it. Row 143 (register line 150) introduced this fail-closed behaviour on purpose. M2 register row 77 notes generally that "5xx drops field errors".

## Final verdicts

Rows 6, 177, 178 and 179 follow the two reviewers' agreeing reports. I did not re-trace those fixes this session beyond the files listed above.

| Row | Verdict |
|---|---|
| 1 | PARTIAL |
| 6 | CLOSED |
| 142 | CLOSED |
| 176 | PARTIAL |
| 177 | CLOSED |
| 178 | CLOSED |
| 179 | CLOSED |

## Residuals that should become new register rows

- **Medium:** the attach and undo-of-detach window. The caller frees a forwarded literal address or alias between the forward guard and the connect, and NPM's API is published until `joinedProblem` disconnects it; name lookups run before the literal comparison (`network_claims.go:320,383,388,882-895`). It needs a fix or an explicit owner acceptance.
- **Medium:** starts by the `unless-stopped` restart policy or an Engine/host restart are not verified (`deps.go:461`, `threat-model.md:297`). It needs an explicit owner ruling.
- **Medium:** raise row 188. The `reloadNginx` `.err` rename can be forced by an operate caller and can hit a host they may not change, the panel exposure included.
- **Medium:** with a forwarded name that got a definite "not found", an operate-triggered upgrade recreates NPM with an nginx that cannot start. The spec's "refuse up front" was never implemented (spec line 75, `w12e-evidence.md:298`).
- **Medium:** one forwarded name with no definite answer, even on a disabled host, blocks every join and upgrade (`network_claims.go:949,1067-1069`).
- **Low:** the 503 detail is reduced to "Unavailable", so the failing forward host reaches only the agent log (`agent/api/handler.go:871-877`).
- **Low:** the harness puts the NPM bearer token in command text and failure errors (`m6_npm_test.go:1073`, `vm.go:319,342`).
- **Low:** `threat-model.md:298`'s "no caller can stretch it for a literal address" should say it applies to starts only. For the attach, a caller can stretch it.
