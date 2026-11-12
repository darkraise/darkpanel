# Rulings — register row 434 (w10a Minors, parked items and hazards)

Owner ruling 2026-11-12: every ruling below is approved as drafted (M3 audit triage of register row 434).

Source list: `docs/dforge/notes/v1-w10a-known-fixes-minors.md` (cited below as `notes:<line>`). Checked against `main` at f7a6432. "post-v1" follows precedent W10-R11.

## Minors deferred by the task reviews (84)

| Item | Now | Ruling | Reason |
|---|---|---|---|
| notes:7, 11, 13, 14, 17, 20, 21, 32, 39, 42, 43, 48, 56, 66, 68, 69, 73, 80 (Task 1 report:28; Task 3 report:14; Task 4 report:24 and :15; Task 5 report:76; Task 6 report:15; Task 7 report:100; Task 10 report:29; Task 12 report:29; Task 13 report:46; Task 14 report:105-108; Task 16 report:108; Task 21 report:41; Task 26 skipped full suite; Task 28 report:42 and skipped full suite; Task 29 skipped full suite; Task 32 report:14) | process only | closed: no product effect | Paraphrased or summarised evidence, a tool advisory line (`vite:react-swc`) or a full-suite run left to the controller; the review workspaces are deleted, and CI on `main` has passed since (ci.yml green on 6d7310a, 2026-11-12). |
| notes:51 Task 17: list-replies.test.ts GREEN output not pristine; notes:53 Task 18: probe-panel vitest prints jsdom `getContext` lines | process only (stderr noise; source of the Task 17 noise not identified) | closed: no product effect | Output noise in a passing run, not a failing or wrong test. |
| notes:46 Task 15: wire.go:0, the brief's expected-output line for Task 27 inherits a wrong `docs/api/npm.md` prediction | process only | closed: no product effect | The error was in a plan brief, not in shipped docs or code. |
| notes:8 Task 1: cmd/darkpanel/main.go:35 `errors.Is` matches a joined error | still present (main.go:40) | post-v1 | It only applies after a stop signal has arrived, so the worst case is exit 0 on a stop that also failed, and the unit stops anyway. |
| notes:9 Task 2: listeners_test.go timing test with about 1 s margin | still present (listeners_test.go:439-498) | post-v1 | Plan-mandated test-quality item. It is not on the known-flaky list. |
| notes:10 Task 2: `addrs, _ := s.runtimeListeners.Addrs()` | still present (listeners_test.go:397, :455) | post-v1 | Test tidiness. The `len(addrs) != 1` check still fails loudly. |
| notes:12 Task 3: registry_test.go `w10sf.` prefix hard-coded | still present (registry_test.go:390) | post-v1 | Test tidiness. |
| notes:15 Task 5: change-dialog.tsx `pe-7` shifts centred header text | still present (change-dialog.tsx:453; darkraise-ui `.dr-overlay-header` is `text-center sm:text-left`) | post-v1 | Cosmetic: below the `sm` width the title sits about 0.875 rem left of centre. This is the side effect of the row 431 fix. |
| notes:16 Task 5: header test checks a class name | still present (change-dialog.test.tsx:467-472) | post-v1 | Test pinned to implementation. |
| notes:18 Task 6: audit.tsx moves focus unconditionally when a load ends | still present (audit.tsx:29-35; focus moves after a clicked load even if the user has moved focus away) | post-v1 | Minor accessibility nit. Focus only moves after the user's own "Load older entries" click. |
| notes:19 Task 6: "No older entries." on a one-page log | still present (audit.tsx:115-119) | post-v1 | The sentence is true and harmless. |
| notes:22 Task 7: derp binary_update_test.go 2 ms pacing | still present (binary_update_test.go:236, :238) | post-v1 | Brief-mandated test timing. |
| notes:23 Task 7: binary_update.go `restore` doc comment repeats itself | still present (binary_update.go:303-306) | post-v1 | Wording. |
| notes:24 Task 8: `editProbeFailure` duplicates `serversReadFailure` | still present (region_edits.go:306, servers.go:288) | post-v1 | Plan-mandated duplicated helper (W10-R2). |
| notes:25 Task 8: `readServerStatuses` keeps starting reads after its context ends | still present (servers.go:256-269) | post-v1 | Each late read starts with a cancelled context, and the pass returns `ctx.Err()`. It costs time only while `lockHost` waits, which the notes:27 fix removes. |
| notes:26 Task 8: `TestW10RgPassReadsHostsTogetherAndBoundsEachRead` depends on wall-clock timing | still present (regions_w10_test.go:18-19, :220 now adds a real 20 ms lag) | post-v1 | Plan-mandated test design. The flake itself is notes:28. |
| notes:27 Task 8: `lockHost` ignores the context | still present (hosts.go:134-152, plain `sync.Mutex.Lock`) | fix before v1 | Choosing fix-before-v1 because I'm unsure. While one caller's Hello hangs, every bounded read of that host waits past its 15 s bound. `readServerStatuses` waits for all reads (`wg.Wait`), so one stalled agent can hang a whole DERP status pass, which breaks the bound servers.go:244-247 promises. |
| notes:28 Task 8: `TestW10RgPassReadsHostsTogetherAndBoundsEachRead` flaky under -race | fixed in f459a29 (w10b Task 2; regions_w10_test.go:18-19, :220) | closed: fixed | The answering agents now take 20 ms of real time, so the fake clock cannot expire their bound first. Register row 436 still holds open the 100-of-100 `-race` run. |
| notes:29 Task 9: cert_renew.go `Apply` derives file names after `Intend` | still present (cert_renew.go:167-176) | post-v1 | Ordering tidiness: `certRenewCheck` already calls `certRenewNames` (cert_renew.go:97) on the same row before `Intend`, so the later call cannot fail. |
| notes:30 Task 9: server_probe.go warns for every host whose `tailscale.status` read fails | still present (server_probe.go:150-152, :212-240; `probeHosts` lists every host) | fix before v1 | A fleet with hosts that do not run the tailscale module gets a false "could not be read" warning on every probe, which an operator would chase. |
| notes:31 Task 9: duplicated bounded-read helpers | still present (server_probe.go:31-63 vs servers.go:248-269) | post-v1 | Duplicated helper (W10-R2). Same item as parked Task 9 below. |
| notes:33 Task 10: tailnet client.go test-binary client has no `Timeout` | still present (client.go:36-41) | closed: no product effect | It is built only under `testing.Testing()`, and `absentTransport` fails every request at once, so a timeout could never fire. |
| notes:34 Task 11: dispatch/apply.go an in-flight Availability can re-cache a pre-install answer | still present (apply.go:84-86 retire vs availability.go:76-104 put) | post-v1 | Narrow race that clears itself within `AvailabilityMaxAge` (60 s). The worst case is a transient `module.unavailable` refusal. |
| notes:35 Task 11: no test for a view with `v.ID` set and a non-success state | still present | post-v1 | Test gap. `applySucceeded` (availability.go:62-69) is correct by reading. |
| notes:36 Task 12: npm unknown-phase branch passes `from` that the `failed_from` CHECK rejects | still present (instance_upgrade.go:504-505, :515) | post-v1 | Unreachable: the `phase` CHECK (migrations/agent/0001_npm.sql:47) and the `failed_from` CHECK (0002:4) allow only values the switch handles. |
| notes:37 Task 12: `notDefinite` drops the errcode of a definite cause | still present (instance_upgrade.go:206, :216, :236-241) | post-v1 | A coded refusal during an upgrade step shows as a bare internal error, with the cause in the agent log (lifecycle/apply.go:459-468). Most step failures are uncoded and already show this way. Borderline, see Notes. |
| notes:38 Task 12: wait loop spins after `cancel()` | still present (instance_upgrade_test.go:1095-1106) | post-v1 | Test busy-wait. |
| notes:40 Task 13: proxyhost_add.go Engine read failure lacks the `npm:` prefix and container name | still present (proxyhost_add.go:264-268) | post-v1 | Plan-mandated error wording. The parked Task 13 ruling still holds. |
| notes:41 Task 13: deps.go Engine read failure surfaces in two shapes | still present (deps.go:380-388 `engineCoded`) | post-v1 | Same classification either way. Wording only. |
| notes:44 Task 14: store-before-Engine test proves order, not one transaction | still present (instance_status.go:72 opens the transaction; test unchanged since d9c5b5b) | post-v1 | Test-quality gap. The code is correct by reading. |
| notes:45 Task 15: npm `-race` run near the 600 s default `-timeout` | still present (not measured) | post-v1 | Release gates do not hit it: `make test-go` uses `-timeout 20m` and `make test-race` covers only `internal/agent/daemon` (Makefile:84, :87). |
| notes:47 Task 16: `removeConnector` doc comment omits the bad tunnel id refusal | still present (connector_run.go:304-309) | post-v1 | Doc comment wording. |
| notes:49 Task 17: NPM list tabs and ExposeLoader replace data on a failed refetch | fixed in 0f7af5c (access-lists-tab.tsx:113-124; certificates-tab.tsx:92-97; proxy-hosts-tab.tsx:204-209; container-actions.tsx:166-172) | closed: fixed | w10b Task 3 keeps the loaded data and shows the failure above it, with tests. |
| notes:50 Task 17: no test for the `ports` half of the inspect guard | still present (container-actions.test.tsx:104-105 cover only `networks`) | post-v1 | Test gap. |
| notes:52 Task 18: connectors-page.test.tsx has no test for a failed background refetch | still present, and the code has the same defect (connectors-page.tsx:101-102 shows only the error when `connectors.isError`, even with data held) | fix before v1 | This is the defect the owner ruled fix-before-v1 for the NPM lists (row 435). Here a transient refetch failure hides the connector table. |
| notes:54 Task 21: lifecycle/guard.go recovery Verify left running can block every writer | still present (guard.go:53-77; firewall Verify takes the read lock, handler.go:161) | post-v1 | It needs a Verify that ignores its cancelled context. Firewall programs run under `exec.CommandContext` (platform/exec.go:124), so a timed-out Verify releases the lock. |
| notes:55 Task 21: `TestW10FlLockSites` pins call sites by function name | still present | post-v1 | Test pinned to implementation. |
| notes:57 Task 22: `TestExtensionStreamEndFrame` flaky under load | fixed in b6cde35 (w10b Task 2; extensions_test.go:500-574) | closed: fixed | The test now ends the stream only after the first frame. Row 436 still holds open the 100-run proof. |
| notes:58 Task 22: Origin with a trailing dot refused | still present (server.go:348-357) | post-v1 | Fails closed, and only a user who typed the host with a trailing dot meets it. |
| notes:59 Task 23: uninstall enablement follows the wants link | still present (server_uninstall.go:146-149) | post-v1 | It needs a dangling wants link while the unit is loaded, and the worst case is a leftover dangling link. |
| notes:60 Task 23: an uninstall effect journaled before the upgrade no longer matches Observe | still present (server_uninstall.go:112-120) | closed: no product effect | Only pre-1.0 builds wrote the old `{"active":..}` shape, and 0.x to 1.0 is a reinstall with no data carried over (docs/versioning.md:89-95). |
| notes:61 Task 23: uninstall Verify checks only `unitActive` | still present (server_uninstall.go:337-356) | post-v1 | Verify also refuses a still-recorded unit file. The worst case is an enabled link to a removed unit, which systemd ignores. |
| notes:62 Task 24: download errors start with "artifact:" | still present (artifact/download/fetch.go:41, :45) | post-v1 | Error-prefix convention only. |
| notes:63 Task 24: releasetool drops XDG_CACHE_HOME | still present (release/releasetool/derper.go:154-158) | post-v1 | Release build speed only. GOCACHE is passed through. |
| notes:64 Task 26: over-limit decoded answer not asserted to drain and close | still present (client_test.go:382-418 cover only undecoded answers) | post-v1 | Test gap. client.go:219-222 drains and closes on every path. |
| notes:65 Task 26: `json.Unmarshal` switch has no test pinning trailing-data refusal | still present | post-v1 | Test gap. |
| notes:67 Task 27: debug message says the list "leaves out" an unreadable host | still present (instance_list.go:64) | post-v1 | Debug-log wording. |
| notes:70 Task 28: `nm.run` marks a later-step refusal Definite | fixed in e6be20f (w10b Task 23; effects.go:108-126, `withoutDefiniteMark` at :81-94) | closed: fixed | Only the first kept step's refusal is Definite now, and the errcode is kept. |
| notes:71 Task 29: backup `Secure` opens the path before checking its type | still present (durable.go:76-103, O_NONBLOCK and O_NOFOLLOW) | post-v1 | Planting a device node under the staged name needs root (mknod). A FIFO is handled by O_NONBLOCK. |
| notes:72 Task 29: backup copy chmodded by path after `VACUUM INTO` | fixed in 964acd3 (w10b Task 22; stream.go:79-123) | closed: fixed | The copy is now in a 0700 directory and its mode is set through an O_NOFOLLOW descriptor after type and link-count checks. |
| notes:74 Task 30: quarantine refusal text duplicated | still present (quarantine_routes.go:255-256, :276-277) | post-v1 | Duplicated literal. |
| notes:75 Task 30: "is larger than %d bytes" format duplicated | still present (stream.go:193, :210) | post-v1 | Duplicated literal. |
| notes:76 Task 31: no test drives `restore` through `reportRestore` with a close error | still present (controller_backup.go:461; `ckHostFor` close always returns nil, controller_backup_test.go:258-263) | post-v1 | Test gap. The code joins the close error correctly. |
| notes:77 Task 31: no committed capacity assertion for the passphrase buffer | still present (controller_backup.go:190-207; no `cap(` assertion in internal/cli tests) | post-v1 | Test gap. The capacity is correct by reading. |
| notes:78 Task 32: banner shows only the generic warning when a refetch fails after a success | still present (banner.tsx:31 checks `fleet.isError` before the held data) | fix before v1 | A failed background refetch replaces "Admin interface exposed at https://..." with a generic "could not check", so the security warning loses the exposed host and its link. |
| notes:79 Task 32: no test for an unreadable id missing from a loaded host list | still present | post-v1 | Test gap on the `?? id` fallback. |
| notes:81 Task 33: "frozen hello and update path" cites internal/agent/daemon | fixed in a9a5fe0 (threat-model.md:111) | closed: fixed | Now cites dispatch, coreactions and agent/api. |
| notes:82 Task 33: two Command execution bullets name no path | fixed in a9a5fe0 (threat-model.md:134, :136) | closed: fixed | Both bullets now cite implementing paths. |
| notes:83 Task 33: 60-second self-revert cited to the firewall module | fixed in a9a5fe0 (threat-model.md:216) | closed: fixed | Now cites `internal/core/lifecycle`. |
| notes:84 Task 33: no test pins per-listener TLS session ticket separation by default | still present (only shared-key tests: service_test.go:483-, runtime_test.go:636-) | post-v1 | Test gap. The claim holds by reading: every server clones the config (set.go:138, runtime.go:315) and `ServeTLS` clones again before any handshake. |
| notes:85 Task 33: trailing citation also covers "agent dials out" | fixed in 0c06d9f (threat-model.md:110) | closed: fixed | The sentence is split, and the dial-out claim cites daemon.go. |
| notes:86 Task 33: derper 0600 key mode cited to Darkpanel | fixed in 0c06d9f (threat-model.md:307) | closed: fixed | Now says Darkpanel does not set the key file's mode. |
| notes:87 Task 34: "the one case that `--recover` cannot settle" | fixed in 88d0a97 (runbook.md:269-280) | closed: fixed | Lists the other refusals and scopes the section. |
| notes:88 Task 34: "Keep both services stopped" gives no command; "journal of the unit" | fixed in 88d0a97 (runbook.md:282, :292-297) | closed: fixed | Gives the stop command and says "systemd log". |
| notes:89 Task 34: restart contradiction and pending only when enrolled | fixed in 88d0a97 (runbook.md:353-354, :363-369) | closed: fixed | Says the restart is deliberate, and pending only if enrolled. |
| notes:90 Task 34: step 7 says tokens can be issued only during quarantine | fixed in e67a899 (runbook.md:233-240, :390-393) | closed: fixed | The false sentence is gone, and `host token` after release is documented. |

## Items parked with a ruling (11)

| Item | Now | Ruling | Reason |
|---|---|---|---|
| notes:96 Task 1: exit 0 for a cancelled startup | holds (main.go:39-44; units SuccessExitStatus=70, RestartForceExitStatus=70, internal/install/units/darkpanel-controller.service) | closed: no product effect | The ruling holds: exit 0 leaves the unit inactive without restart or OnFailure. |
| notes:97 Task 1: full-suite and lint output not shown raw | process only | closed: no product effect | CI on `main` is green since (6d7310a, 2026-11-12). |
| notes:98 Task 5: Playwright run with E2E_CHROMIUM | holds (web/playwright.config.ts:18; docs/accessibility.md:10) | closed: no product effect | The override is documented, and the evidence ruling stands. |
| notes:99 Task 6: RED at base not re-run | process only | closed: no product effect | The shipped fix and its GREEN tests are unaffected. |
| notes:100 Task 7: Recover unchanged and flags a failed update | holds (binary_update_recover.go: the `failed` case returns RecoveryNeedsRepair) | closed: no product effect | Verified in code. |
| notes:101 Task 8: transport never codes an expired-context read | holds (session.go:105 wraps `ctx.Err()` uncoded; no errcode in transport for context errors) | closed: no product effect | The stored text stays `probeAgentNoAnswer`. |
| notes:102 Task 9: duplicated bounded-read helpers (Important, plan-mandated) | holds (server_probe.go:31-63 vs servers.go:248-269) | post-v1 | W10-R2 duplicated-helper deferral, same as notes:31. |
| notes:103 Task 9: an unknown mode never reaches UnitFile | holds (server_install.go:56 CheckConfig in precheck; :207 is the only non-test UnitFile caller) | closed: no product effect | Verified in code. |
| notes:104 Task 10: shared exportTN breaks no test | holds (export.go:14; export_test.go only reads it) | closed: no product effect | CI green since. |
| notes:105 Task 13: Engine read failure without `npm:` prefix | holds (proxyhost_add.go:264-268) | post-v1 | Same as notes:40. |
| notes:106 Task 19: VM scenarios harnessSettings, exposeRemove, upgrade not run in-task | holds; harness.yml runs TestM6NPM (vm-m6n job, harness.yml:456-507) and has passed on `main` repeatedly, latest 6d7310a on 2026-11-12 | closed: no product effect | Gate 7 covers it. I did not open the job logs to see each step. |

## Hazards discovered during execution (7)

| Item | Now | Ruling | Reason |
|---|---|---|---|
| notes:110 Task 10: cloud repository-file rollback on the forward context | fixed in fa7f8f7 (w10b Task 6; tailscale/package_pin.go:165, :178, :245-257 `rollbackContext`) | closed: fixed | Early-failure rollbacks now run on a detached, bounded context. |
| notes:111 Task 17: NPM list tabs and ExposeLoader drop data on a failed refetch | fixed in 0f7af5c (w10b Task 3) | closed: fixed | Same as notes:49. |
| notes:112 Task 28: `nm.run` later-step Definite mark | fixed in e6be20f (w10b Task 23) | closed: fixed | Same as notes:70. |
| notes:113 Task 29: backup stream.go `os.Chmod` by path | fixed in 964acd3 (w10b Task 22; stream.go:120 `f.Chmod`) | closed: fixed | Same as notes:72. |
| notes:114 Task 33: threat-model path test proves existence only | holds (internal/docs/threat_model_test.go:195-215 uses `os.Stat`) | post-v1 | The owner's 2026-11-06 ruling on row 435 still holds. Citations were corrected by hand (a9a5fe0, 0c06d9f). |
| notes:115 Task 34: restore refuses a host without controller.key; a successful restore deletes earlier `.pre-` files | still present (restore_guard.go:146-148; restore.go:358-368 `dropOldAside`) | post-v1 | Deliberate behaviour that the runbook states with its workaround (runbook.md:302-313, :322-324). |
| notes:116 Known flaky tests | fixed in f459a29 and b6cde35 | closed: fixed | Same as notes:28 and notes:57. Row 436 stays open only for the recorded 100-of-100 `-race` runs. |

## Fix before v1

- notes:27 `lockHost` ignores the context. In `internal/controller/dispatch/hosts.go:134-152`, replace the per-host `*sync.Mutex` with a one-slot `chan struct{}` and acquire it with `select` on `ctx.Done()`, returning the context error. Test in the dispatch package: hold host h1's lock through a Hello that blocks, call `Agent(ctx, "h1")` with a 50 ms deadline, and expect `context.DeadlineExceeded` well before the Hello ends. Also add a DERP test showing that `readServerStatuses` returns within its bound while another caller holds the host.
- notes:30 false tailscale warning. In `internal/modules/derp/server_probe.go` (`probeSelves`, :212-240, and the warning loop at :150-152), do not report a host whose `tailscale.status` read is refused because the tailscale module is unavailable or disabled there (`errcode.ModuleUnavailable`, or a check of `core_module_state`). First confirm which code an agent returns for a resource of a disabled or unavailable module. Keep the warning for timeouts and other failures. Test in the derp package: one host refusing with `module.unavailable` gets no warning, and one that times out still does.
- notes:52 connectors page drops data on a failed refetch. In `web/src/modules/cloudflare/connectors-page.tsx:101-109`, render the table whenever `connectors.data` is defined and show `ErrorAlert` above it when `connectors.isError`. Show the error alone only when there is no data, following 0f7af5c's NPM pattern. Test in `connectors-page.test.tsx`: load the connectors, make the next read fail, invalidate, and expect both the rows and the alert.
- notes:78 banner hides exposure alerts on a failed refetch. In `web/src/modules/npm/banner.tsx:31-33`, branch on `fleet.data` first: with held data, render the exposure alerts and the unreadable-host alert, plus the not-checked warning when `fleet.isError`. Use the generic warning alone only when there is no data. Test in `banner.test.tsx`: a successful read that shows an exposed host, then a failed refetch, still shows "Admin interface exposed" for that host.

## Notes

- notes:52 understates the problem. The review graded it a missing test, but `connectors-page.tsx` itself has the defect: on refetch failure `connectors.isError` wins over the held data. It is the same class as the NPM list hazard the owner ruled fix-before-v1 in row 435.
- notes:37 (`notDefinite`) is borderline. If the owner wants coded upgrade failures to reach the API, swap `notDefinite` in `instanceUpgradeHandler.fail` (instance_upgrade.go:206, :216) for `withoutDefiniteMark` (effects.go:81) and wrap with the step and restore lead, so the code and the "restored" text both survive. The text is lost from the API today either way, because `changeError` drops the message of an internal error.
- notes:27 and notes:25 are linked. Once `lockHost` honours the context, the late reads of `readServerStatuses` end at once, and notes:25 becomes pure tidiness.
- The Task 33 threat-model and Task 34 runbook findings (notes:81-90 minus 84) were fixed inside w10a itself (a9a5fe0, 0c06d9f, 88d0a97, e67a899 all predate the plan's completion commit dc91aa2), yet the notes still list them as deferred.
- Rows 435 (done) and 436 (open) already cover the hazards. Only the 100-run `-race` record for the two formerly flaky tests is still outstanding, under row 436, not row 434.
- Counts: the notes file has exactly 84 Minor lines (7-90), 11 parked lines (96-106) and 7 hazard lines (110-116), matching row 434.
