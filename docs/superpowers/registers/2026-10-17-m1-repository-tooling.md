# M1 repository, tooling, CI and gates register

**Source:** M1 roadmap epic "Repository, tooling, CI and gates" (R2, R3, R4)
**Covers:** docs/superpowers/specs/2026-10-17-m1-repository-tooling-design.md

| # | Item | Assigned | Acceptance | State | Note |
|---|---|---|---|---|---|
| 1 | R2 one Go module builds static linux/amd64 and linux/arm64 binaries with the web UI embedded | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | make build verify-build passes; file reports two static ELF binaries; each contains <title>Darkpanel</title>; version --json prints ui true | done | - |
| 2 | R2 one documented build command | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | README and CONTRIBUTING document make build | done | - |
| 3 | R3 CI runs Go and TypeScript lint, type checks, unit tests and both builds on every push | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | ci.yml jobs adr, go, web, build call make targets; CI run on pushed main concludes success | done | CI run 39023127615 on main (eed7a01) concluded success; gate 6 green on main |
| 4 | R3 LICENSE (MIT), CONTRIBUTING.md and SECURITY.md | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | files exist with the content the spec section 3 names | done | - |
| 5 | R4 docs/superpowers/gates.md declares the project's gates | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | gates 1-6 in running-gates format; every gate command passes | done | - |
| 6 | Development host toolchain: Go 1.27.2 and golangci-lint v2.15.0 installed system-wide | docs/superpowers/plans/2026-10-17-m1-repository-tooling.md | go version and golangci-lint version print the pinned versions | done | - |
| 7 | Set GitHub default branch to main and push main with upstream tracking (default is probably m1-repository-tooling; local main tracks gone origin/master) | - | GitHub default branch is main; local main tracks origin/main | done | owner approved 2026-10-17; main pushed with -u, default branch set to main, remote m1-repository-tooling deleted (fully contained in main) |
| 8 | make check fails on a fresh clone and verify-build has no build prerequisite: add a web-deps target as prerequisite of lint-web, test-web and web, and make verify-build depend on build | - | - | deferred | Tasks 3 and final review; CONTRIBUTING documents the workaround (A1); fold into the walking-skeleton epic |
| 9 | Makefile VERSION and COMMIT are empty outside a git checkout: fall back to dev/unknown | - | - | deferred | Task 3 and final review; matters when tarball or release builds arrive (M8) |
| 10 | web/vite.config.ts uses __dirname in an ES module package; Vite warns on every build and the @ alias breaks if the native config loader becomes default (use import.meta.dirname) | - | - | deferred | Tasks 5, 6, 10; generator-verbatim copy |
| 11 | Pre-paint script in web/index.html falls back to system mode while themeConfig defaults to dark; script needs a hash or file once serve sets a strict CSP | - | - | deferred | Final review; belongs to the walking skeleton |
| 12 | web/tsconfig.json include omits vite.config.ts and vitest.config.ts so they are not type-checked | - | - | deferred | Final review; ESLint still lints them |
| 13 | Remove stale minimumReleaseAgeExclude eslint@10.13.0 from web/pnpm-workspace.yaml | - | - | deferred | Final review; harmless |
| 14 | ci.yml hardening: timeout-minutes, persist-credentials false, cancel-in-progress not on main, avoid duplicate push plus pull_request runs; CI shallow checkout has no tags so binaries carry a short SHA | - | - | deferred | Tasks 9 and final review; low stakes |
| 15 | Add .superpowers/ to .git/info/exclude so a git add -A cannot publish the ledger | - | - | deferred | Final review; local-only setting |
| 16 | gates.md: gate 1 duplicates test-adr (gate 3 also runs it); gate 6 prints null or empty while no run exists; use check-ignore --no-index in the .gitignore check | - | - | deferred | Tasks 4 and 8; plan-mandated, fail safe |
| 17 | Test polish: version_test hard-codes dev/unknown, main() error path untested, app.test.tsx toBeDefined after getByRole, vitest.config.ts separate from vite.config.ts, vitest react-swc notice | - | - | deferred | Tasks 2 and 6; plan-mandated |
| 18 | Docs polish: README and CONTRIBUTING prerequisites omit file, git and bash; SECURITY.md commits only to a 7-day acknowledgement; .gitignore bin/ is unanchored | - | - | deferred | Tasks 4 and 7; plan-mandated |
| 19 | Unrelated vite processes (PIDs 3687666 and 3687667 from /workspace/apps/template) were running on the host during the run; not started by this plan | - | - | deferred | Task 5 discovered; host observation for the owner |
