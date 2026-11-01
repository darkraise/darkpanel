# v1 wave 4 follow-ups from the final review

Plan: `docs/dforge/plans/2026-10-31-v1-w4-tailscale-node-and-join.md`, merged into `main` at 02a9b4e.
The execution ledger is deleted when the worktree is cleaned up, so this note keeps what it left open.
Nothing here blocked the merge: gates 1-7 were green at 02a9b4e (CI run 41315407302; harness run 41315410574, whose one red job, TestM3Docker on debian-13, passed on rerun and touches no file of this wave).

Line numbers are those of `main` at 02a9b4e; the ledger's own numbers drifted, so they were re-read. Items the final fix wave fixed, and items the re-review upheld as rejected with evidence, are not listed.

## Residuals after the final fix wave

The final re-review (judge-opus) closed the fix round with 0 open and carried two Minor findings.

- [Minor] internal/controller/dispatch/availability.go:74 — after `tailscale.package.install` succeeds, every other tailscale action on that host is refused for up to 60 seconds with the misleading reason "tailscaled does not answer on its socket", and the UI greys those actions out. This follows spec 1.7 (the cache is retired only by hello or module enable), so changing it is a spec decision, for example retiring the host and module entry when a change of that module applies. The harness works around it by planning again while the refusal is `module.unavailable` (`m5pPlanPin`, test/harness/m5_pin_test.go) (final re-review)
- [Minor] internal/modules/tailscale/node_login.go:70 — `nodeLoginDoc`, which Observe (line 320) and Made (line 252) use, records a `NoState` backend as `loggedIn:false`, so an effect settled while tailscaled restarts records an unknown state as logged out. Returning an error from Observe in that state would make the engine retry; that is a choice about how the engine settles effects (final re-review, found by the fixer)

Left alone by the final fix wave because each needs a design, spec or plan decision (12):

- [Minor] internal/modules/tailscale/node_logout.go:70 — a failed status read after a successful Logout fails the change although the logout already took effect. Returning an empty `BackendState` changes the meaning of the output ("the backend state after the logout"), and Verify fails the change anyway if the read keeps failing (Task 22, final review)
- [Minor] internal/modules/tailscale/package_install.go:208 — repeating an undo after `setInstallRemoved` failed returns early because no version is installed and leaves the install row live. A fix needs a store query ("is this change's row live") and a rule for a missing row; `uninstall.go` already records the removal on its path (Task 20, final review)
- [Minor] internal/modules/tailscale/package_pin.go:240 — the rollback after a failed check runs on the forward context, so it fails at once when the 20-minute apply budget has expired; Recover finishes the rollback afterwards, so the cost is one failed attempt and a "needs repair" message. Needs a detached context and a bound for it (Task 24, final review)
- [Minor] internal/modules/tailscale/package_pin.go:311 — `rollback-verify`'s `txAwait` (line 455) never ends under `coretest.FakeClock` unless the test advances the clock, so a future test of a failing verify hangs until the go test timeout. Needs a real-time deadline or a clock helper (Task 29, final review)
- [Minor] internal/modules/tailnet/join.go — no end-to-end test of `tailnet.node.join` or of the installer's join from a bundle; TestM5Node runs without a tailnet. The `${steps.key.output.key}` template resolution was confirmed only by reading the engine (ruling cv-32a). Needs a harness tailnet, which spec 4 puts in the live step (Task 32, final review)
- [Minor] internal/modules/tailnet/join_sweep.go:96 — an enroll key of an enrolled host becomes `used` and is never deleted at Tailscale, so a copied bundle stays usable until its 3600 s expiry when the host enrolled by another route. Spec 4.2 step 3 says the controller deletes nothing for a used key, so deleting needs a spec change (Task 18, security lens)
- [Minor] internal/modules/tailnet/join_sweep.go:98 — the sweep can retire a live enroll key whose host row is not committed yet (`prepareJoin` settles the key to live before `hostCreate` inserts the host), so the operator gets a bundle with a dead key. A grace period conflicts with plan-mandated sweep cases (TestJsSettleEnrollKeys, TestJsSettleMovesEveryRow) and with the spec's "the host is deleted first" rule (Task 18, silent-failures lens)
- [Minor] internal/modules/tailscale/linkguard.go:186 — a `Client` with an invalid address adds no flow and no unknown flow, as in the firewall guard (flows.go:222). A caller on a Unix socket legitimately has no IP address, so failing on it needs a decision (the `Via` half was fixed in 7a36f54) (Task 17, silent-failures lens)
- [Minor] internal/modules/tailscale/latency.go:87 — a failed STUN probe's error is dropped and reported as "UDP may be blocked"; plan line 8151 fixes that reason text exactly, so keeping `lastErr` in the Reason is a plan change (Task 15, silent-failures lens)
- [Minor] internal/modules/tailscale/latency.go:49 — a nil DERP map is answered as a successful read with no regions; whether it should be `tailscale.unavailable` is a product decision (plan line 760 rules "no regions") (Task 15, silent-failures lens)
- [Minor] web/src/pages/create-host-dialog.tsx:47 — a grant without a bundle after `tailnetJoin` was asked shows the plain token (labelled as a token) instead of an alert, and the one-time token is shown only once. Whether to withhold it behind an alert, and the wording, is a UX decision (Task 11, silent-failures lens)
- [Minor] internal/modules/tailscale/package_pin_recover_test.go — Recover's unknown-phase branch is untested; it is unreachable through the store because `tailscale_pins.phase` has a CHECK constraint listing the ten phases (migrations/agent/0001_tailscale.sql:10). Needs a decision between a test through a fake store and dropping the branch (Task 29, test-quality lens)

## Security finding parked during execution

- [Minor] internal/modules/tailscale/latency.go:152 — the DERP map's `HostName` and `DERPPort` go unchecked into the probe URL (`DERPPort` above 65535 is also not rejected, line 156). Whoever controls the tailnet's DERP map could make the host send body-less HTTPS GETs to internal services and see whether they answer. Ruled PARK because the brief mandates the construction and the probe is one GET with verified TLS, no redirects, a discarded response and a 5 s timeout. The fix the ruling describes: skip nodes whose `HostName` holds a slash, question mark, hash, at sign, backslash, whitespace, a control character, or a colon that is not part of an IPv6 literal, build the URL with `url.URL`, and bound the port (Task 15; upheld by the final re-review)

## Parked items

The harness run 41315410574 at 02a9b4e passed vm-m5, vm-m5p and the tailscaled-live step on all four OS, which settles the parked cannot-verify items that waited on it: cv-33a, cv-33b, cv-34a, cv-34b, cv-34c, cv-35a, cv-35b and the Task 1 item that root holds PermitWrite. They are not listed.

- [Minor] docs/dforge/specs/2026-10-21-v1-completion-design.md:1990 — the "Login by key" source facts (Start does not log in a node without a node key; login-interactive follows) can only be settled on a real tailscaled with a control server, and the harness has no tailnet, so the run did not exercise them (inference). If the recorded form is wrong, `tailscale.node.login` fails on its first real login (Task 1)
- [Minor] internal/controller/listen/tailscale_cert.go:191 — the certificate name is not checked against the leaf or the MagicDNS suffix; it comes only from the controller host's own agent, which already controls the certificate, and tailnets may use custom domains. An agent-side bug could put an unrelated name into the panel CA leaf's SANs; tailscale_cert_test.go:423 has no case for a leaf that does not cover the name (Task 6, security ruling)
- [Minor] internal/modules/tailscale/wire_test.go:24 — `TestTHWireTypesExist` checks the length of its own list; its only real check is that five types compile (preflight-76, found again by the test-quality lens) (Task 8)
- [Minor] internal/modules/tailnet/join_enroll.go:84 — the enroll key row stores the provider's key id, not the sealed key, while spec 4.2 step 2 says "the controller stores the key sealed for cleanup only". The sweep deletes by description and no task reads a stored key, so a `key_ct` column would be an unused secret; add it only if a later wave needs the key (preflight-78) (Task 10)
- [Minor] web/src/modules/tailscale/tailscale-change.tsx:26 — `ChangeDialog` does not call `onOpenChange(false)` after a successful apply, so confirm and revert controls stay visible for `prefs.set`; the brief does not require closing (Task 5)
- [Minor] web/src/pages/create-host-dialog.tsx — the running Add host dialog and the plan refusing a bad tag with field `/tags` were checked only through tests that drive the real extension registry; no task checks the built UI, so a defect there would show at the first live tailnet demonstration (Task 11)
- [Minor] internal/modules/tailscale/status.go:20 — `statusRead` assumes `Status` and `Prefs` of the local API client are assumed never to return nil with a nil error (client.go returns an address of a local value); a different local API implementation returning nil, nil would panic `statusRead` (Task 14)
- [Minor] internal/modules/tailscale/repo.go:195 — the two-phase Intend resources `tailscale.file:?` and `tailscale.package:?` follow effects.go:145-150; safe only while no later task Intends a creation (Task 26's Join runs `ensureRepo` with `env.Effects` nil) (Task 20)
- [Minor] internal/modules/tailscale/uninstall.go:56 — an explicit `package=remove` on a host whose tailscale agent migrations never ran aborts `darkpanel uninstall` at `liveInstall`; migrations run for every registered module, so this needs a broken migration, and the failure is loud (rerun with keep) (Task 25)
- [Minor] internal/modules/tailscale/bootstrap.go:142 — a node already Running with an address counts as joined and the auth key is never used, whichever tailnet or tags it holds; a rerun with the same bundle then fails after the one-use key was spent, and a node on another tailnet enrolls over the wrong address and fails with a connection error. Spec 4.2 step 4 makes a rerun with a fresh bundle the retry (ruling cv-30a); comparing the node's tailnet or tags with the bundle, or saying the key was not applied, would need a decision (Task 30, silent-failures lens)
- [Minor] test/harness/m5_helpers_test.go:140 — `m5Settle` copies `m4Settle`'s body (about 12 lines) because `m4_helpers_test.go` was outside Task 33's files; the two can drift (pc-33a) (Task 33)
- [Minor] .github/workflows/harness.yml:69 — the existing `vm` job has `timeout-minutes: 280` (its comment: 120 for M1 and M2, 80 for M3 firewall, 80 for M3 Docker), above the 120-minute job limit the spec states; no task of this plan could change it, and the owner was to be told at finishing (Task 35)

## Deferred findings by task

### Task 1
- [Minor] docs/dforge/specs/2026-10-21-v1-completion-design.md:2003 — "Login by key" leaves open whether `Start` with an `AuthKey` on a node that already has a node key makes the server use the key ("not documented, direct.go not read")
- [Minor] docs/dforge/specs/2026-10-21-v1-completion-design.md:3983 — the changed 4.2 step 4 line is not wrapped like its neighbours
- [Minor] internal/modules/tailscale/prefs_set.go:63 — tailscaled refuses (400) an edit that uses an exit node while advertising both /0 routes (spec 1.8); `prefs.set` has no plan-time check for it, so the operator sees tailscaled's own refusal text at apply

### Task 2
- [Minor] internal/transport/token.go:160 — the unsupported-version detail echoes the pasted digits unbounded (`bundleVersionPattern` at line 105 matches any run of digits)
- [Minor] internal/transport/token_test.go:170 — no row covers a zoned IPv4-mapped address, which `isTailnetHostPort` refuses before `Unmap` (token.go:235)

### Task 3
- [Minor] internal/modules/tailscale/stun/stun.go:57 — the panic-safety invariant of the attribute loop (the header length check guarantees four bytes for each attribute header) is stated nowhere in the code
- [Minor] internal/modules/tailscale/stun/stun_test.go:140 — a few `Parse` behaviours have no test

### Task 4
- [Minor] internal/modules/tailscale/link/link.go:206 — rules 6 and 7 call `Prefix.Contains` without `Unmap` (also line 239), unlike `IsTailscaleAddr`; safe while every flow address is unmapped, as `linkguard.go` guarantees
- [Minor] internal/modules/tailscale/link/link_test.go:271 — no row for `relogin` with `cur.RunSSH`, rule 6 with several removed prefixes (sentence order), or a rule 7 flow with a zero `Local`

### Task 5
- [Minor] web/src/modules/tailscale/fixtures.ts:16 — the exported fixtures are shared mutable objects

### Task 6
- [Minor] internal/controller/listen/tailscale_cert.go:134 — `names` ignores the `loading` flag, so a listen check and a background refresh can read twice at once (same result)
- [Minor] internal/controller/listen/tailscale_cert.go:195 — a `tls.X509KeyPair` error can name PEM block types such as `PRIVATE KEY` in the debug log when an answer misplaces the key
- [Minor] internal/controller/listen/tailscale_cert.go:48 — the shared cache keeps the first caller's `SourceDeps`, including `Log`, for the life of the process
- [Minor] docs/dforge/plans/2026-10-31-v1-w4-tailscale-node-and-join.md — the plan text of `TestTcDuePairIsReadAgainOnlyAfterAMinute` (amendment A38) has a bare `c.names(...)` call that fails errcheck; the code uses `_, _ =`. Fix the plan text if the test is copied again

### Task 7
- [Minor] docs/dforge/plans/2026-10-31-v1-w4-tailscale-node-and-join.md:180 — the Contracts "Core host join" struct writes the `Tags` tag without `pattern=`, while the brief (line 3110) and `internal/core/fleet.go:111` include it

### Task 8
- [Minor] internal/modules/tailscale/deps_test.go:84 — plan-mandated: `TestTHHTTPSProbe` prints a TLS handshake error line in the untrusted-client case
- [Minor] internal/modules/tailscale/deps.go:156 — the os-release path and the dpkg status path (line 268) are inline literals

### Task 9
- [Minor] internal/controller/coreactions/hostenroll.go:241 — a `ParseToken` failure after the write committed returns an error although the host and token rows are already written

### Task 10
- [Minor] internal/modules/tailnet/join_enroll.go:84 — an `insertKey` failure is returned without `core.Definite` (unreachable with a fresh ULID; nothing downstream reads `IsDefinite` there)
- [Minor] internal/modules/tailnet/join_enroll.go:87 — the comment above `record` is misleading about what is recorded when the context ended

### Task 11
- [Minor] web/src/pages/create-host-dialog.test.tsx:180 — a non-string `bundle` is not tested
- [Minor] web/src/modules/tailnet/host-create.test.tsx — the `aria-describedby` link to the invalid-tag help (host-create.tsx:47) is not asserted

### Task 12
- [Minor] web/src/modules/tailscale/status-panel.tsx:89 — health warnings use the message text as the React key, so a repeated warning gives duplicate keys
- [Minor] web/src/modules/tailscale/status-panel.test.tsx — the Backend state badge variants (`stateVariant`, status-panel.tsx:11) and the refetch-failure path are not tested
- [Minor] web/src/modules/tailscale/peers-table.tsx:8 — connection text leaves a trailing space ("direct ", "derp ") when the endpoint or region is empty
- [Minor] web/src/modules/tailscale/latency-panel.tsx:36 — the destructive-alert block and the guard-and-read helper are copied from status-panel.tsx

### Task 13
- [Minor] web/src/modules/tailscale/prefs-form.test.tsx — the mandated tests leave several implemented rules unasserted (route set comparison, `::/0`, exit-node trimming, blank hostname, repeated tags)
- [Minor] web/src/modules/tailscale/prefs-form.tsx:150 — closing the dialog resets the draft before the status refetch lands, so the form briefly shows the old values
- [Minor] web/src/modules/tailscale/prefs-form.tsx:142 — the change dialog is mounted only while a change is pending, not always with `input={null}` as the brief describes (equivalent)

### Task 14
- [Minor] internal/modules/tailscale/status.go:141 — default-route prefixes are parsed again with `MustParsePrefix` for every route
- [Minor] internal/modules/tailscale/status_test.go:20 — no test exercises the "skip a nil peer value" rule, and the logged-out test never sets `CurrentTailnet`, so clearing the tailnet is not exercised

### Task 15
- [Minor] internal/modules/tailscale/latency.go:40 — a read has no overall time bound beyond the caller's context, so dead endpoints can take tens of seconds
- [Minor] internal/modules/tailscale/latency_test.go — nothing tests the limit of 8 concurrent measurements

### Task 16
- [Minor] internal/modules/tailscale/cert_get.go:67 — `certErr` is `statusLocalErr` (status.go:71) with the detail as a parameter

### Task 17
- [Minor] internal/modules/tailscale/linkguard_test.go:429 — the fail-closed paths are tested only for a nil `env.Units`; a failing socket table, interface list or unit state is not, and nothing tests that the default-route probe runs once per local address
- [Minor] internal/modules/tailscale/linkguard.go:381 — `linkInbound` matches an unspecified listener of either address family

### Task 18
- [Minor] internal/modules/tailnet/keysweep.go:101 — the claim that a failing settle step still lets the provider pass run has no test
- [Minor] internal/modules/tailnet/join_sweep.go:77 — the `parseStoreTime` error is returned without the "read enroll keys" context the neighbouring errors carry

### Task 19
- [Minor] web/src/modules/tailscale/tailscale-page.test.tsx:98 — the Package and Preferences tab tests (line 110) read the new panel's props straight after the click, with no wait
- [Minor] docs/dforge/plans/2026-10-31-v1-w4-tailscale-node-and-join.md — the plan's verbatim Latency tab test asserts `queryByTestId("status-panel")` is null with no `waitFor`; this is a timing race in the darkraise-ui TabsContent exit (ruling pc-19a fixed the code, the plan text was not amended)

### Task 20
- [Minor] internal/modules/tailscale/package_install.go:212 — `/usr/bin/apt-get` is a literal in three places (also repo.go:218 and repo.go:221)
- [Minor] internal/modules/tailscale/repo.go:195 — no test pins the Intend-time resource and Recovery shape that the frozen journal requires
- [Minor] docs/dforge/plans/2026-10-31-v1-w4-tailscale-node-and-join.md — the brief's Observe step and `TestPiPackageInstallObserve` mandate `Matches` 1 for absent objects; ruling pc-20a fixed the code and test to `Matches` 0 with `Exclusive`, the plan text was not amended (a copy of the plan would reintroduce it)

### Task 21
- [Minor] internal/modules/tailscale/prefs_set.go:138 — if `Effects.Skipped` fails after a definite refusal, Apply returns the journal error and drops the definite refusal
- [Minor] internal/modules/tailscale/prefs_set_test.go — no test covers Revert's failure paths (the restore `EditPrefs` failing, the post-restore mismatch) or the other A39 paths: Apply with a Status error, Revert resolving the snapshot, Plan and Verify wrapping the Status error
- [Minor] internal/modules/tailscale/prefs_set.go:345 — Plan returns the Status error from `prefsSetResolve` (and `currentLinkPrefs`, linkguard.go:54) neither coded nor marked definite, so a Status failure at plan time surfaces as an uncoded internal error

### Task 22
- [Minor] internal/modules/tailscale/node_login.go:239 — a failed `Effects.Skipped` call hides tailscaled's definite refusal
- [Minor] internal/modules/tailscale/node_login.go:131 — `nodeLoginRefusal` rebuilds the cause from the redacted text, so `tslocal.ErrUnavailable` and `*tslocal.APIError` leave the error chain for callers that test it with `errors.Is` or `errors.As`

### Task 23
- [Minor] internal/modules/tailscale/package_update.go:62 — the StateHash covers only the installed version, not the hold state, and Task does not check the hold again, so a hold added after planning makes apt-get fail and the error returns unchanged

### Task 24
- [Minor] internal/modules/tailscale/pin_session.go:22 — `agentSession` has no test-binary guard, unlike `productionTS`'s tailscaled socket (deps.go:110)

### Task 25
- [Minor] internal/modules/tailscale/uninstall.go:69 — a removal that dpkg interrupted partway is recorded as complete on the next run (the rerun sees no installed version and records the removal while files remain; the state needs `dpkg --configure -a`, which the rules allow only in the pin rollback)
- [Minor] internal/modules/tailscale/uninstall_test.go — no test covers a timed-out remove followed by the re-hold under the fresh bound

### Task 26
- [Minor] internal/modules/tailscale/bootstrap.go:99 — `redact` with an empty key would corrupt the error text (`ReplaceAll` with an empty old string); `Join` refuses an empty key first, but `join` called directly does not
- [Minor] internal/modules/tailscale/bootstrap.go:118 — install failure messages repeat the package prefix and the word "install" ("tailscale: join: install: tailscale: record install install: ...")
- [Minor] internal/modules/tailscale/bootstrap_test.go — no test cancels the context in either poll

### Task 27
- [Minor] internal/modules/tailscale/prefs_repair.go:194 — the empty-`RepairOf` branch calls `prefsRepairTarget` only to get its refusal back

### Task 28
- [Minor] internal/modules/tailscale/package_unpin.go:123 — plan-mandated: Before is always `{"held":true}`, even when Task reads the hold as false
- [Minor] internal/modules/tailscale/package_unpin.go:72 — `packageUnpinBy` with an empty change id matches every active pin (`unpinned_by = ''`, SQL at line 21); every caller passes one
- [Minor] internal/modules/tailscale/package_unpin.go:18 — `packageUnpinResource` duplicates `pinTxResource` (package_pin.go:26)

### Task 29
- [Minor] internal/modules/tailscale/package_pin_recover_test.go:172 — no test covers Observe when `installedVersion` fails; only the `showhold` failure is tested

### Task 30
- [Minor] internal/install/install.go:507 — a `bootstrap()` error returned from `enroll` reads "install: enrollment: install: bootstrap bundle: ...", with the prefix twice (the step name is added at line 211)
- [Minor] internal/install/install.go:151 — `bootstrap` runs before the existing `Units` flag check (line 160), while the brief says after its existing checks

### Task 32
- [Minor] internal/modules/tailnet/join.go — the decode-error branch of `Output` has no test
- [Minor] internal/modules/tailnet/join.go:60 — the comment sits above the whole `return append(...)` but explains only the login step, and its wording is unclear

### Task 33
- [Minor] test/harness/m5_helpers_test.go:255 — `m5xLoggedOut` with an empty `version` reports `want one beginning ""` when the version is empty, which is misleading

### Task 34
- [Minor] test/harness/m5_pin_test.go:454 — `m3Run` of the iptables delete in step 7 can end the scenario before the link is lifted (the cleanup in `m5pArm` then removes the rule)
- [Minor] test/harness/m5_pin_test.go:397 — the error from reading `tailscale_pin_steps` is discarded (`stepsOut, _ :=`)
- [Minor] test/harness/m5_pin_test.go:290 — the result of the arm cleanup script is dropped

### Task 35
- [Minor] test/harness/m5_live_test.go:70 — the cleanup that removes the copied binary is registered after `CopyFile` succeeds (a partial copy would stay on the VM; inference)
- [Minor] test/harness/m5_live_unit_test.go:171 — most mutation cases change only the first occurrence of a shared string (`strings.Replace(..., 1)`), which is in `vm-m5`, so the same mutation in `vm-m5p` goes unchecked
