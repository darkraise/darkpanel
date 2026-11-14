# Verification of the disputed w12d re-review findings — judge-opus, commit 10a0973a

Taken from the verifier's reply: the review-seat hook refused its report-file write. Read-only; no tests run. Statements about nginx, NPM's templates, the Docker Engine and the Go resolver are inference, because none of them can be read in this repository.

| Claim | Verdict |
|---|---|
| 1. Row 1, residual A (a withdrawn name, then a re-attach at A) | CONFIRMED in code; the nginx step is inference |
| 2. Row 1, residual B (undo of a finished upgrade) | PARTLY |
| 3. Row 1, residual C (address freed during an upgrade; row 142) | CONFIRMED that the interleaving is allowed; row 142 deserves high |
| 4. Row 6, IPv6 gap on a link-local-only host | CONFIRMED; not an accepted limit; high |

## 1. Residual A: CONFIRMED (the nginx step is inference)

**Scopes.** `DefaultScope` (`internal/core/scope.go:6-15`) gives `operate` to every agent-side compensable action, and only `MinScope` raises it (`:20-26`). No npm mutating action declares `MinScope`; the only npm action that does is in `reveal.go:19`. All of these therefore need only `operate`: `docker.network.connect` and `disconnect` (`docker/network_connect.go:22-34`), `docker.network.create` (`network_manage.go:89`), `docker.container.stop` (`container_run.go:30-43`), `npm.network.attach` and `detach` (`npm/network_claims.go:36-59`), `npm.proxyhost.add`. `refuseOwned` (`docker/owned.go:13-19`) blocks only containers and networks labelled for another module, so a user container X and a user network N pass.

**Steps walked:**
1. Create N with a subnet, then connect X to it with an alias and a static IPv4 address A (`network_connect.go:209-222`).
2. Attach NPM to N with no address. `forwardGuard` runs only when NPM joins a network (`network_claims.go:316-320`). No proxy host forwards anywhere yet, so it passes.
3. Add a proxy host with the alias as forward host, port 3000 or 81: `dockerAnswers` reads Docker's names on NPM's current networks, X answers with A, and A is not NPM's, so the check passes (`proxyhost_add.go:324-371,398-443`). The body sends the name itself (`:161-173`), as the "/" custom location.
4. Disconnect X from N. A is now free, and no container answers to the alias.
5. Detach NPM's claim (`network_claims.go:523,569-586`). Nothing touches the proxy hosts or nginx.
6. Attach NPM to N with `address: A`: `forwardTargets` (`:827-882`) collects the alias as a name to resolve; `dockerMembers` reads only the networks NPM is on now (`proxyhost_add.go:333`), and N is no longer one of them; the host's resolver answers "not found", which `network_claims.go:962` skips, so `forwardTaken` has no target and passes. Apply connects NPM with the static address A (`:376-386`).
7. Nothing re-validates afterwards. `checkForward` runs only in add, update and the edit undos. `forwardTargets` runs only at attach, at the undo of a detach and at upgrade. There is no periodic re-check.

**Inference about nginx.** NPM renders the location as a static `proxy_pass`, which nginx resolves once at load and keeps. The repo states the same premise in `proxy/upstream.go:81-82`. nginx then forwards to A:3000 or A:81, which is now NPM's own API or admin UI. A later reload fails on the dead name and keeps the old workers; an NPM restart would end the exposure.

**Why the guard skips the name.** The skip rests on the comment at `network_claims.go:823-826` and w12a plan line 406: a name with no answer "can reach NPM only after it is re-pointed". That premise is false here, because nginx still holds the old answer and NPM moves onto it.

There is no Engine address choice and no race, since the attach asks for A as a static address. Severity is **high**. No row records it, and `TestW12aAttachResolvesForwardHostNames` (`network_claims_test.go:1113`) pins the skip.

## 2. Residual B: PARTLY

**What holds:**
- Undo is authorized at the scope of the change's own action (`changes.go:100-106`, `actions.go:421-426`), which for `npm.instance.upgrade` is `operate`.
- `claimUndo` refuses only when a later change took the `npm` lock (`undo.go:148-199`, `locks.go:99-122`). Freeing A takes the `docker` lock, so the undo stays open.
- For phase `done`, `Inverse` (`upgrade_recover.go:14-41`) runs `checkArchives`, then `rollback`, then `restore` (`instance_upgrade.go:661-711`).
- `restore` recreates NPM and reconnects its networks with static addresses only and no forward check (`:170-177,393-400`; comment at `:112`).

**What differs from Fable's account:**
- Not deterministic. At the reconnect (`:689`), the address the upgraded NPM held is also free, because `:671` removed that container. Whether the Engine picks A is unknown. A caller can force it by taking the other freed address with a static `docker.network.connect` between `:671` and `:689`, but that is a race.
- A static address defeats it. If NPM's endpoint on N was static, the restore asks for that address back and never takes A.

**Acceptance.** The owner rulings (w12a plan lines 65 and 68; W12a-R1 at plan line 168) and `threat-model.md:292` record this as "not fixed". Their ground is that "a refusing rollback would leave NPM down". That fits a failed-upgrade rollback, not a requested undo of a `done` upgrade: `Inverse` already refuses with no change when the archives fail (`upgrade_recover.go:30-32`), and NPM is still running at that point. Neither text says an `operate` caller can drive the undo case, and it is not in the owner-accepted list (`threat-model.md:332-338`).

Severity is high by the rubric, conditional on the Engine's choice or on the race.

## 3. Residual C (register row 142): the interleaving is allowed; row 142 deserves high

**Locks.** Each lock key is its own row (`locks.go:21-42`): `npm` for the npm module (`npm/deps.go:34`) and `docker` for the docker module (`docker/deps.go:16`). Only an update operation's `ReserveAll` blocks every key (`reserve.go:123-137`). The upgrade is `LongRunning` and holds only `npm`, so `docker.container.stop` and `docker.network.disconnect` (both operate) run while it runs.

**Window.** It runs from `forwardCheck` (`instance_upgrade.go:217`) through the pull (`:220`), the stop with 30 seconds of grace (`:302`), both archives (`:312-318`), the remove (`:326`), the create (`:329`), the connect (`:333`) and the start (`:336`). Whether the Engine assigns the address at connect or at start is inference, but both fall inside the window. The residual B caveat applies too: the old container's address is also free after `:326`.

**Severity.** Row 142 was graded low because it was framed as a threat-model understatement and fixed with a sentence at `threat-model.md:295`. The effect it describes, an `operate` caller forcing NPM onto a forwarded address and so publishing NPM's API, is high under the rubric. It is not in the owner-accepted list. Row 142 should be high, or the owner should accept it at high in that list.

## 4. Row 6 link-local-only IPv6 gap: CONFIRMED; not an accepted limit; high

**Code path:**
- `local` holds every interface address, link-local included (`guard/flows.go:135-144`).
- A client carried by Docker's userland proxy is named by the bridge gateway, a host address, so `fromHere` holds (`:148-151`). `npmClientFlows` then calls `npmPublicFlows` with `unicast=true` (`:452-456`).
- For `[::]`, `npmPublicFlows` always adds the IPv4 flow (`:417`). It adds the IPv6 flow only if some local IPv6 address is neither loopback nor link-local (`:420-425`). The public leg (`:288-290`) uses the same function, so a link-local-only host gets no IPv6 flow at all.
- `relayFlows` checks only the IPv4 output leg to the container (`:368-370`). A change that drops IPv6 TCP 443 matches no flow and passes without the override.

**Not an accepted limit.** The w12b design (`specs/2026-11-13-v1-w12b-open-rows-design.md:54`) carries the predicate over as a mechanism. It does not name it as a risk, and no ruling accepts it. The comment at `flows.go:418-419` ("a host without an IPv6 route skips those cells") is sound for the internet leg, but not for the hidden-client leg, whose unicast space includes `fe80::/10`.

**Line 215 of the threat model is not accurate.** It says "an NPM exposure recorded on `[::]:443` is checked on both families" and that "every unicast source of each binding's family" is checked; line 229 repeats the second claim. Both are false on a link-local-only host.

**Reachability.** The path is real for a same-link client that sends the exposure's hostname to `fe80::<host>%<iface>`:443, such as `curl --resolve` or an API client. Docker's userland proxy listens on `[::]:443` regardless of the host's IPv6 addresses (inference; the relay itself is verified per `threat-model.md:235`). A browser practically cannot do this, since browsers reject zone identifiers in URLs and DNS cannot carry a scope (inference).

**The 60-second window does not change the grade.** The rubric's high class is "passes without the override", with no duration term. The auto-revert applies to every guarded change, including the original row 6 scenario that was graded high; it only bounds how long the lockout lasts.

**Verifier's severity: high.** It is the rubric's high effect for a client that can actually reach the exposure; the narrowness lowers likelihood, not class. To treat it as a design limit instead, the owner must write it into the threat model, since line 215 now says the opposite.

## Fable's ungraded observations

**(a) Real.** For a request through an exposure, `linkguard.go:178-185` adds only the proxy-to-listener flow, and the comment at `:179` has been stale since `clientPeer` returns the client NPM named (`api/server.go:224-227`). A tailnet client of an NPM exposure gets no flow of its own in that guard. Fable's claim that the controller's Tailscale-address listener masks this is not verified, and no register row mentions it.

**(b) Real.** `checkRestoredLocation` (`proxyhost_edit.go:241-250`) passes the forward host without cutting it at its first `/`. `<NPM address>/x` defeats all three address parsers (`proxy/upstream.go:85-92`). Assuming the Go resolver answers "not found" for it (inference), the resulting Definite `invalid_input` is turned into "restore unchecked" at `:246-247`. NPM cuts the host at `/` (inference, matching `network_claims.go:854`), so the restored location forwards to NPM itself. Writing such a location in the first place needs NPM's administrator.
