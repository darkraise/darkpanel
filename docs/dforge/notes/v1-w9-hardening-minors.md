# v1 wave 9 hardening: Minor findings deferred during execution

The per-task and per-group review seats of docs/dforge/plans/2026-11-05-v1-w9-hardening.md left these Minor findings unfixed (each task's ledger line says "Minor deferred"). The review reports are deleted with the plan workspace, so the first line of each finding is kept here. Register row 433 points to this note; the wave's open findings with their own rows are 416-432.

- Task 1: scripts/fuzz.sh:234 — `set -e` is switched off inside `check_inventory`, so a failing `find` or `awk` there is ignored.
- Task 1: scripts/fuzz.sh:220 — an inventory that is a file but unreadable makes the script exit 2 through awk, not through `usage`.
- Task 1: scripts/fuzz.sh:182 — the script adds usage errors the brief does not list: any positional argument (line 182), and an empty Package, Target or Tags cell (line 209).
- Task 2: web/src/test/a11y.ts:183 — `getRoles(document.body)` is called without `hidden: true`, while the `queryAllByRole` call below it uses `hidden: true`.
- Task 2: web/src/test/a11y.ts:183-186 — only the first role in the role map that lists the element is used.
- Task 2: .dforge/sdd/2026-11-05-v1-w9-hardening/task-2-report.md:22 — the RED evidence for page-title.test.tsx is not quoted.
- Task 3: .dforge/sdd/2026-11-05-v1-w9-hardening/task-3-report.md:27 — TDD and verification output is paraphrased, not quoted
- Task 3: internal/apidocs/apidocs.go:34452 — `Write` cannot write a key that has a directory part
- Task 3: internal/apidocs/apidocs_test.go:0 — render edge cases are covered only through the real registry
- Task 4: cmd/darkpanel/paths_harness_test.go:16 — the tests write package-level mutable state (`harnessStateDir`, `selectPaths`), which Global Constraints "Test isolation" forbids
- Task 4: cmd/darkpanel/paths_harness.go:29 — `--state-dir` is not checked for being an absolute path
- Task 4: cmd/darkpanel/paths_harness_test.go:0 — the brief's Step 1 says "The four tests" but provides three
- Tasks 5-6: (Task 5) .dforge/sdd/2026-11-05-v1-w9-hardening/task-5-report.md:14 — RED output for tslocal is elided and the seed-count RED was not observed
- Tasks 5-6: (Task 5) .dforge/sdd/2026-11-05-v1-w9-hardening/task-5-report.md:29 — verification lines are pass/fail tallies, not the real output the brief asks to record
- Tasks 5-6: (Task 6) .dforge/sdd/2026-11-05-v1-w9-hardening/task-6-report.md:25 — same tally-style verification for build, vet, full suite, race, lint and tidy
- Tasks 7-9: (Task 9) .dforge/sdd/2026-11-05-v1-w9-hardening/task-9-report.md:15 — fuzz-run evidence is elided, not verbatim
- Tasks 7-9: (Task 9) .dforge/sdd/2026-11-05-v1-w9-hardening/task-9-report.md:30 — full-suite, vet, race and lint results are counts, not output
- Tasks 7-9: (Task 7) .dforge/sdd/2026-11-05-v1-w9-hardening/task-7-report.md:37 — the Verified list is counts, not output
- Tasks 7-9: (Task 8) internal/modules/tailscale/deps.go:274 — nothing explains why Status words are split on a space only
- Task 10: (Task 10) .dforge/sdd/2026-11-05-v1-w9-hardening/task-10-report.md:7 — RED and GREEN output is paraphrased, not quoted verbatim
- Task 10: (Task 10) .dforge/sdd/2026-11-05-v1-w9-hardening/task-10-report.md:11 — the nine fuzz run lines are shortened and the closing cleanup is not recorded
- Task 11: test/harness/upgrade_sources.txt:4 — plan-mandated: the comment says "the commit wave 9 started from"
- Task 11: .github/workflows/harness.yml:55 — the shell loop and the Go parser read the sources file differently
- Task 11: Makefile:150 — the `harness-builds` loop does not stop when one `go build` fails (adjacent, existing code, not in this diff)
- Task 12: (Task 12) internal/docs/helpers_test.go:249 — the full-repository lint gate was not re-run after the nolint edit, and the RED and GREEN output is summarised rather than quoted
- Task 12: (Task 12) internal/docs/helpers_test.go:225 — dcBroken does not skip inline code spans, so a page that shows link syntax in backticks fails dcLinks
- Task 12: (Task 12) internal/docs/helpers_test.go:249 — the `//nolint:unused` directive on dcLinks goes stale once Tasks 16 to 19 call it, and nothing will flag that
- Task 13: web/src/pages/host.tsx:542 — the host lookup `hosts.data?.hosts.find((h) => h.id === hostId)` is computed twice, once for the title and again at host.tsx:552
- Task 14: web/src/components/change-dialog.tsx:222 — When the deadline appears after mount, the countdown is missing for up to one second, two announcements follow, and no test covers this path
- Task 14: web/src/pages/change-actions.tsx:201 — The notice takes focus only when its text changes
- Task 14: .dforge/sdd/2026-11-05-v1-w9-hardening/task-14-report.md:28 — The quoted test output is filtered, not verbatim
- Task 15: web/src/test/setup.ts:0 — Test output is not clean: jsdom prints "Not implemented: HTMLCanvasElement's getContext()" once for each file that runs axe
- Task 15: web/src/modules/firewall/firewall-page.tsx:517 — The two fallback landmarks are built two ways: `<div role="region">` here and `<section aria-label>` in rules-table.tsx:748
- Task 16: docs/user/administration.md:277 — over-long line breaks the 80-column wrap used elsewhere
- Task 17: .dforge/sdd/2026-11-05-v1-w9-hardening/task-17-report.md:36 — GREEN and full-suite evidence is paraphrased, not quoted verbatim
- Task 17: internal/docs/modules_a_test.go:121 — TestDmHostinfo checks only the top-level fields, and several mention checks match incidental words
- Task 17: docs/user/modules/firewall.md:304 — developer-voice instruction in a user page
- Task 18: docs/user/modules/panel.md:53 — web UI sections do not say the web UI cannot confirm the final firewall step
- Task 18: docs/user/modules/npm.md:25 — two prose lines are not wrapped like the rest of the page
- Task 18: .dforge/sdd/2026-11-05-v1-w9-hardening/task-18-report.md:15 — the RED evidence is elided and the GREEN evidence is a one-line summary
- Task 19: (Task 19) docs/dev/module-guide.md:67 — the ActionSpec `Class` row says only `core.DefineMutating` leaves the class to the caller
- Task 19: (Task 19) docs/dev/module-guide.md:151 — "The registry refuses a field on a kind that does not use it" is broader than the code
- Task 19: (Task 19) docs/dev/module-guide.md:14 — says the registry finds `core.Uninstaller` with a type assertion
- Task 20: web/src/modules/docker/containers-tab.tsx:123 — Both effects move focus whenever it is outside the group or form, not only when it fell to the body. The firewall effect does the same at web/src/modules/firewall/container-actions.tsx:90.
- Task 20: web/src/modules/docker/containers-tab.tsx:121 — If the read after a close fails, the record of the closed request is never cleared, because `dataUpdatedAt` does not change on an error. The firewall form has the same issue at web/src/modules/firewall/container-actions.tsx:88.
- Task 20: web/src/modules/tailnet/global.tsx:41 — The same six-line not-found block appears in the tailnet, derp and cloudflare global.tsx, and each copy hard-codes darkraise-ui's internal `dr-empty-state*` class names.
- Task 21: test/harness/footprint_test.go:466 — fpNoReconcile fails on any journal line containing "reconcile", in any case
- Task 21: test/harness/footprint_unit_test.go:119 — the "sample first" check indexes evs[i] and evs[i+1] without checking that IndexFunc found a match
- Task 21: test/harness/footprint_test.go:367 — stopExtraAgents discards the error of each `systemctl stop`
- Task 22: web/e2e/a11y.pw.ts:527 — plan-mandated: every run of the pass rewrites tracked files under docs/accessibility/, and it writes them before asserting
- Task 22: web/src/components/change-dialog.tsx:0 — at 320 px the expose dialog's title runs up to the close button (seen in docs/accessibility/expose-plan-dark-reflow-320.png)
- Task 22: web/e2e/fixtures.ts:0 — the reported `make test-web` output is not clean: it contains two `Not implemented: HTMLCanvasElement's getContext() method` jsdom lines
- Task 23: docs/accessibility.md:154 — the second Known issue states "Nothing is cut off" as fact
- Task 23: docs/accessibility.md:121 — the 3.3.1 evidence for Container actions is weaker than it looks and leaves out a stronger file
- Task 23: docs/accessibility.md:90-128 — 2.4.2 has no row in Firewall change or Container actions
- Task 24: internal/docs/links_test.go:327 — plan-mandated: `wcPages` appends `docs/accessibility.md` unconditionally, so the required-page check for that entry can never fire
- Task 24: docs/dforge/specs/2026-10-21-v1-completion-design.md:4919 — the Fuzzing bullet now states the push and nightly budgets twice
- Task 24: .dforge/sdd/2026-11-05-v1-w9-hardening/task-24-report.md:112 — Step 7's `go clean -testcache` was not run, and the GREEN run is summarized rather than quoted

## Final review

- internal/apidocs/apidocs_test.go:385 — the fix's new test sits between TestAdAPIDocsCurrent and its doc comment
- web/src/components/change-dialog.a11y.test.tsx:223 — the one-second countdown test has a one-second real-time margin and could flake under load (inference)
