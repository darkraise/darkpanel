# v1 wave 10a known fixes: findings left open during execution

The per-task and per-group review seats of docs/dforge/plans/2026-11-06-v1-w10a-known-fixes-threat-model-runbook.md left these Minor findings unfixed (each task's ledger line says "minor (deferred)"), and its implementers or reviewers parked the items in the second list with a ruling. The review reports are deleted with the plan workspace, so the first line of each finding is kept here. The final whole-branch review was clean with nothing parked. The register rows added at the finish of the plan point here; triage them with the other open rows of docs/dforge/registers/2026-10-21-v1-completion.md.

## Minors deferred by the task reviews

- Task 1: review workspace task-1-report.md:28 — the Verified section does not record the real output the brief asks for
- Task 1: cmd/darkpanel/main.go:35 — `errors.Is` also matches a joined error that wraps a real failure
- Task 2: internal/controller/server/listeners_test.go:339 — plan-mandated timing test with about 1 s of margin
- Task 2: internal/controller/server/listeners_test.go:316 — plan-mandated `addrs, _ := s.runtimeListeners.Addrs()` drops the error
- Task 3: review workspace task-3-report.md:14 — RED and GREEN evidence is paraphrased, not verbatim
- Task 3: internal/modules/registry_test.go:289 — the `w10sf.` prefix check is hard-coded outside the shared rule
- Task 4: review workspace task-4-report.md:24 — report claims `git diff --name-only` lists the spec file, but it ran after the commit
- Task 4: review workspace task-4-report.md:15 — the `rg 'cached answer|retired by|hello or module enable'` result is described, not quoted
- Task 5: web/src/components/change-dialog.tsx:645 — `pe-7` pads only the end of a header whose text is centered at narrow widths, so the title and description sit about 0.875 rem left of center.
- Task 5: web/src/components/change-dialog.test.tsx:503 — the header test checks for a Tailwind class name, not the layout.
- Task 5: review workspace task-5-report.md:76 — the GREEN Vitest output still prints the `vite:react-swc` recommendation line.
- Task 6: web/src/pages/audit.tsx:293 — the effect moves focus unconditionally when a load ends
- Task 6: web/src/pages/audit.tsx:336 — "No older entries." also renders for a log that fits in one page, where the user never loaded anything
- Task 6: review workspace task-6-report.md:15 — the RED and GREEN evidence is excerpted, not verbatim
- Task 7: review workspace task-7-report.md:100 — the second attempt's GREEN results are summarised, not quoted verbatim
- Task 7: internal/modules/derp/binary_update_test.go:235 — the 2 ms pacing makes the tests depend on timing and slow, and the brief requires it
- Task 7: internal/modules/derp/binary_update.go:304 — the new sentences in `restore`'s doc comment repeat the first sentence and read awkwardly
- Task 8: internal/modules/derp/region_edits.go:308 — plan-mandated: `editProbeFailure` repeats the condition in `serversReadFailure` (servers.go) word for word.
- Task 8: internal/modules/derp/servers.go:256 — `readServerStatuses` keeps starting reads after the pass's context is cancelled.
- Task 8: internal/modules/derp/regions_w10_test.go:221 — plan-mandated: `TestW10RgPassReadsHostsTogetherAndBoundsEachRead` depends on wall-clock timing.
- Task 8: internal/controller/dispatch/hosts.go lockHost takes a mutex that ignores the context, so a bounded read can wait past its bound while another caller holds that host's lock — outside Task 8's files, found by the ruling seat
- Task 8: internal/modules/derp/regions_w10_test.go — TestW10RgPassReadsHostsTogetherAndBoundsEachRead is flaky under -race (fails 6 of 100 on the base commit aaf76a4, found by Task 9's implementer); its fake-clock driver advances 1 s per loop without pausing, so the 15 s read bound can pass before the other hosts' reads start — for the final review to fix
- Task 9: internal/modules/derp/cert_renew.go:97 — `Apply` derives the file names after `env.Effects.Intend` has recorded the effect
- Task 9: internal/modules/derp/server_probe.go:714 — every host whose `tailscale.status` read fails gets a warning on every probe, including hosts that may not run the tailscale module at all
- Task 9: internal/modules/derp/server_probe.go — the duplicated bounded-read helpers should fold into one after Tasks 8 and 9 (post-v1)
- Task 10: review workspace task-10-report.md:29 — the RED quote is shortened, not verbatim
- Task 10: internal/modules/tailnet/client.go:61 — the test-binary client has no `Timeout`
- Task 11: internal/controller/dispatch/apply.go:57 — an Availability request in flight during the apply can re-cache the answer from before the install
- Task 11: internal/controller/dispatch/availability_test.go:212 — no test covers a returned view with `v.ID` set and a non-success state
- Task 12: internal/modules/npm/instance_upgrade.go:164 — the unknown-phase branch passes `from` to `setUpgradeFailed`, which the `failed_from` CHECK rejects
- Task 12: internal/modules/npm/instance_upgrade.go:67 — `notDefinite` keeps only the error's text, so any `errcode` or sentinel in a definite cause's chain is lost
- Task 12: internal/modules/npm/instance_upgrade_test.go:436 — after `cancel()`, the wait loop spins without sleeping until the upgrade goroutine ends
- Task 12: review workspace task-12-report.md:29 — RED rows elided, not verbatim
- Task 13: internal/modules/npm/proxyhost_add.go:267 — plan-mandated: an Engine read failure is returned without the `npm:` prefix or the container name
- Task 13: internal/modules/npm/deps.go:380 — an Engine read failure in add or update now surfaces in two shapes, with the same classification
- Task 13: review workspace task-13-report.md:46 — the RED output is edited, and the full-suite output is summarized rather than quoted
- Task 14: review workspace task-14-report.md:105-108 — the full-suite, race, lint and tidy results are summarized instead of quoted
- Task 14: internal/modules/npm/instance_status_test.go:392-412 — the store-before-Engine test proves the order of the reads, not that they share one transaction
- Task 15: internal/modules/npm/composite_deploy_expose_test.go:0 — the race run of the npm package is close to Go's default 600 s per-binary `-timeout`
- Task 15: internal/modules/npm/wire.go:0 — the brief's expected-output line for Task 27 inherits the same wrong `docs/api/npm.md` prediction
- Task 16: internal/modules/cloudflare/connector_run.go:298 — `removeConnector`'s doc comment does not mention the new refusal of a bad tunnel id
- Task 16: review workspace task-16-report.md:108 — Step 5 results are summarized as "N passed" rather than quoted
- Task 17: web/src/modules/npm/access-lists-tab.tsx:107 — The three list tabs and ExposeLoader still replace loaded data with the error alert when a background refetch fails
- Task 17: web/src/modules/npm/container-actions.test.tsx:259 — No test covers the `ports` half of the inspect guard
- Task 17: web/src/modules/npm/list-replies.test.ts:0 — The GREEN test output is not pristine
- Task 18: web/src/modules/cloudflare/connectors-page.test.tsx:51 — no test for a background refetch that fails while connector data is held
- Task 18: web/src/modules/derp/probe-panel.tsx:0 — reported vitest output is not clean: it still contains jsdom `Not implemented: HTMLCanvasElement's getContext()` lines
- Task 21: internal/core/lifecycle/guard.go:61 — the recovery Verify left running on its own goroutine can now block every writer
- Task 21: internal/modules/firewall/hostlock_test.go:451 — test-quality lens (tests pinned to implementation): `TestW10FlLockSites` pins lock call sites by enclosing function name, and still misses wrapper-level reads
- Task 21: review workspace task-21-report.md:41 — the GREEN output is annotated rather than quoted verbatim
- Task 22: internal/controller/api/extensions_test.go:524 — TestExtensionStreamEndFrame is flaky under load; this is not a defect against this task
- Task 22: internal/controller/api/server.go:354 — [lens:security] an Origin with a trailing dot (`https://panel.example.com.`) is refused; not a defect against this brief
- Task 23: internal/modules/derp/server_uninstall.go:148 — enablement uses readFile, which follows the link, so a dangling wants-link counts as not enabled
- Task 23: internal/modules/derp/server_uninstall.go:112 — an uninstall effect journaled before the upgrade (`{"active":..}` only) will no longer match what Observe returns
- Task 23: internal/modules/derp/server_uninstall.go:343 — Verify still checks only unitActive, so an uninstall that leaves the unit stopped but enabled still verifies
- Task 24: internal/modules/derp/artifact/download/fetch.go:40 — errors of package download still begin with "artifact:", against the Global Constraint that error strings start with the package name
- Task 24: release/releasetool/derper.go:154 — the pass-through list also drops XDG_CACHE_HOME, so a runner that relies on it to place GOCACHE gets a cold cache
- Task 26: internal/modules/npm/npmapi/client_test.go:345 — the over-limit decoded answer path is not asserted to drain at most `errorBodyLimit` and close the body
- Task 26: internal/modules/npm/npmapi/client.go:217 — the switch to `json.Unmarshal` (trailing data now refused, empty body error text changed) has no test pinning the new behaviour
- Task 26: internal/modules/npm/npmapi/client_test.go:0 — the report skips the brief's Step 4 `go test -count=1 ./...` full-suite run, deferring it to the controller
- Task 27: internal/modules/npm/instance_list.go:132 — debug message still says the list "leaves out" an unreadable host, which is now named in Unreadable
- Task 28: review workspace task-28-report.md:42 — the RED output is shortened ("first entry and ..."), but the global constraints ask for it verbatim
- Task 28: review workspace task-28-report.md:38 — the full `go test -count=1 ./...` that the task's Step 4 and the global constraints require was not run
- Task 28: internal/modules/npm/effects.go:83 — nm.run also returns Definite when a later step's call is refused (a 409 on RemoveVolume after the container is gone); stripping the mark with notDefinite would drop the errcode
- Task 29: internal/controller/backup/durable.go:82 — Secure opens the path as root before checking its type, so a character device planted under the staged name gets opened (opening some devices has side effects), where the old Lstat check never opened it
- Task 29: internal/controller/backup/stream.go:86 — adjacent, outside this task: the backup copy is chmodded by path after `VACUUM INTO`, which follows a symbolic link and leaves the file at umask mode until the chmod
- Task 29: review workspace task-29-report.md:19 — the full `go test -count=1 ./...` that Step 4 and the Global Constraints require was not run; the report says the dispatch handed it to the controller, so it still has to run
- Task 30: internal/controller/backup/quarantine_routes.go:91 — the refusal text is duplicated as a literal beside the early check; a shared constant would stop the two texts drifting apart
- Task 30: internal/controller/backup/stream.go:311 — the "is larger than %d bytes" error format is duplicated from stream.go:293; a shared format constant would keep the two in step
- Task 31: internal/cli/controller_backup_fix_test.go:288 — no committed test drives `restore` through `reportRestore` with a cleanup or `closeHost` error, so reverting line 155 of controller_backup.go to the old early return would pass every test
- Task 31: internal/cli/controller_backup.go:113 — the guarantee that `append` never reallocates the passphrase buffer is shown only by a scratch test that was deleted; commit a capacity assertion so a later change to the escaping cannot silently break it
- Task 32: web/src/modules/npm/banner.tsx:32 — a failed refetch after an earlier success replaces the exposure alerts with the generic warning, because `fleet.isError` is checked before the retained data
- Task 32: web/src/modules/npm/banner.test.tsx:112 — no test names an unreadable id that a successful host list does not contain (the `?? id` branch with a loaded list)
- Task 32: review workspace task-32-report.md:14 — RED and GREEN evidence is summarised rather than verbatim, and the reported output carries jsdom getContext noise
- Task 33: docs/security/threat-model.md:110 — "frozen hello and update path" cites `internal/agent/daemon`, which holds only the OnHello handler and update recovery at start
- Task 33: docs/security/threat-model.md:134 — two Command execution bullets name no implementing path
- Task 33: docs/security/threat-model.md:208 — the 60-second self-revert is cited to `internal/modules/firewall`, but it lives in `internal/core/lifecycle/apply.go`
- Task 33: internal/controller/listen — no test pins the default separation of TLS session tickets per listener, only the shared-keys case
- Task 33: docs/security/threat-model.md:109 — the single trailing citation `internal/controller/listen/agent.go` also reads as supporting the clause "the agent in `internal/agent/daemon` dials out"; that clause is implemented in internal/agent/daemon/daemon.go (`dialLoop`), not in listen/agent.go
- Task 33: docs/security/threat-model.md:289 — "Derper writes its node key file with mode 0600 (`internal/modules/derp/server`)": server.go only defines `KeyPath` and passes `-c KeyPath` to derper; the 0600 mode is set by upstream derper, so the cited path does not implement it
- Task 34: docs/runbook.md:286 — "the one case that `controller restore --recover` cannot settle" is wrong: recovery also keeps the journal when the installed database fails verification (restore.go:338), when the journal is not a trusted root-owned file (recover.go:76) and when it does not belong to this controller (recover.go:106); name those refusals or drop "the one"
- Task 34: docs/runbook.md:301 — "Keep both services stopped" gives no command while the controller unit restarts itself (darkpanel-controller.service:7-16, state `activating`, which restore_guard.go:111-115 refuses); point at the stop command of Restore from backup step 3, and write "systemd log" instead of "the journal of the unit" at line 291, which reads like the restore journal
- Task 34: docs/runbook.md:363 — the page requires an agent restart while the command prints "it needs no restart" (agent.go:314-316); say the restart is deliberate, and at line 351 say the host is set to pending only when it was enrolled (quarantine_routes.go:295-299)
- Task 34: docs/runbook.md:238 — step 7 says a token for the agents "can be issued only while the quarantine is active", which the new paragraph at line 372 contradicts and which is false because `darkpanel host token` issues one after the release; say that the quarantine enroll-token works only during the quarantine

## Items parked with a ruling

Each line keeps the ruling that closed it for the run.

- Task 1 (parked, ruled): exit status of a cancelled startup (reviewer's cannot-verify on 0 versus 70) — Ruling: exit 0 is what register row 392 and plan Task 1 require; exit 70 would be restarted by RestartForceExitStatus=70, and exit 0 already ends the unit inactive so OnFailure does not start — if the owner wanted 70, a signalled stop would be restarted by systemd and count toward StartLimitBurst
- Task 1 (parked, ruled): full-suite and lint output not shown raw — Ruling: the task check is deferred to the scheduled full suite and the implementer's Step 5 is recorded as passed; the wave-1 check covers the integrated tree — a misreported failure would surface one task later
- Task 5 (parked, ruled): Playwright pass needed E2E_CHROMIUM because the pinned headless shell 1248 is not installed on this host — Ruling: running the pass with E2E_CHROMIUM set to the cached chromium-1243 (Chrome for Testing 153.0.8010.12, the version the doc's last-run line already names) is acceptable evidence; web/playwright.config.ts and docs/accessibility.md document the override and the brief tells the implementer to use it — at worst a screenshot pixel difference from the pinned build that a later run on a host with it would show
- Task 6 (parked, ruled): the RED run at the base commit could not be re-run by the reviewer (command not named in the plan) — Ruling: the RED claim holds; the report quotes the brief's four-file run failing 3 of 33 tests and the diff explains each failure (htmlFor/id pairs and the 'No older entries.' paragraph exist only as additions), and the fallbackFocus mutation check is credible — if wrong, the base failure is unproven for one task while the GREEN runs and the shipped fix are unaffected
- Task 7 (parked, ruled): reviewer could not confirm Recover is unchanged and still flags a failed update — Ruling: Recover is untouched (the only hunk in binary_update_recover.go covers lines 38-73, the Inverse doc comment and the added "failed" case; Recover sits at lines 76-108) and its failed branch still returns RecoveryNeedsRepair — if wrong, a failed update would stop being flagged for repair after a restart, which the code shows it is not
- Task 8 (parked, ruled): reviewer could not confirm what the real agent transport returns when a read hits its per-read bound — Ruling: the transport (Hosts.Agent, api.Client.Read/do, transport.Session.open) never wraps an expired or cancelled context in a coded error, so the read failure stays uncoded and serversReadFailure stores probeAgentNoAnswer as the brief requires — if wrong, an unresponsive host's stored error would carry a transport detail instead of probeAgentNoAnswer
- Task 9 (parked, ruled): Important plan-mandated: serverProbeEach and serverProbeBounded duplicate the bounded parallel-read block of readServerStatuses (servers.go) — Ruling: the plan requires both helpers by name, sharing one would edit servers.go which Task 8 owns and which is outside Task 9's Files, and W10-R2 defers duplicated helpers to post-v1 (the preflight already parked it) — a final-review Minor asking to fold the helpers, absorbed by post-v1 tidying
- Task 9 (parked, ruled): reviewer could not confirm an unknown mode never reaches UnitFile — Ruling: Task runs precheck at server_install.go:175, which calls CheckConfig (line 56) refusing any mode other than letsencrypt or ip, before line 205, the only non-test UnitFile caller; a nil unit file is never written — none
- Task 10 (parked, ruled): reviewer could not confirm that sharing exportTN between Module() and the exported readers breaks no test elsewhere — Ruling: exportTN is unexported and no test assigns or replaces it, module{t: ...} tests use their own tn values, other packages only call Module() and the shared cache carries no state between tests (a token is stored only after a successful fetch and absentTransport refuses every request); the full-suite run and the wave-3 closing check cover it — if wrong, a test elsewhere would fail at the wave check
- Task 13 (parked, ruled): plan-mandated Engine read failure surfaces without the npm: prefix or container name (deferred minor above) — Ruling: the brief prescribes returning the Engine's own error and the review found it only a Minor — the bare message shows only in a narrow race because reservedVia inspects the container first
- Task 19 (parked, ruled): the VM scenarios harnessSettings, exposeRemove and upgrade were not run (they need VMs) — Ruling: acceptable now; the plan's Global Constraints say no task runs VMs and gate 7 (harness.yml dispatched on the branch) is where they are confirmed: the harnessSettings step's `SELECT changes();` through m3Run prints exactly 1, and the upgrade scenario sees a changed container id — if wrong, a VM scenario fails or flakes only at gate 7 and a fix round follows there, with no effect on the shipped product code

## Hazards discovered during execution and not fixed

- Task 10: the cloud repository-file failure path still rolls back on the forward context (outside that row, per the brief).
- Task 17: the NPM list tabs and `ExposeLoader` do not keep data when a refetch fails; the brief did not cover them.
- Task 28: `nm.run` in internal/modules/npm/effects.go has the same later-step Definite mark that the fix removed for the before-state read.
- Task 29: internal/controller/backup/stream.go still calls `os.Chmod` by path.
- Task 33: the path-existence test of docs/security/threat-model.md cannot show that a cited path implements the claim made about it.
- Task 34: `controller restore` refuses a host with no /etc/darkpanel/controller.key, and a successful restore deletes earlier `.pre-` files; docs/runbook.md was written against that behaviour.
- Known flaky tests: `TestW10RgPassReadsHostsTogetherAndBoundsEachRead` in internal/modules/derp (6 of 100 under `-race`, its fake-clock driver advances without pausing) and `TestExtensionStreamEndFrame` in internal/controller/api (fails under load, passed 20 of 20 alone).
