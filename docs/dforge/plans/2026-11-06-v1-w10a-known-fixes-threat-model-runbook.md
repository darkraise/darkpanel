# v1 wave 10a: known fixes, threat model and runbook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first half of wave 10 of the v1 completion spec. That means fixing every open follow-up row whose fix is already determined: two major bugs, the Important and security findings of waves 0c–9, and the minors that make observable behaviour wrong. It also means writing `docs/security/threat-model.md` (R73's threat model) and `docs/runbook.md` (R81's runbook), and copying this plan's rulings into the spec. The two-reviewer security review then runs against the merged result, and plan `w10b` follows it.

**Architecture:** Every fix is a feature slice with its own regression test, grouped by module and file so that no two tasks of a wave share a file. Fifteen ordinary fixes run in five parallel waves of three, and two more (Tasks 11 and 13) run on their own. Wave 1 also carries the spec amendments, a trivially independent fourth task. Thirteen fixes that touch security, a migration or concurrency run alone. The banner runs alone after them, and the threat model and the runbook come last, because they describe what the fixes changed.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), SQLite through the existing `internal/core/store` migrations, React 19 + TypeScript with Vitest 5.0.5, vitest-axe 1.0.0-pre.5, @testing-library/user-event 14.6.8 and @playwright/test 1.64.0 (all already in `web/pnpm-lock.yaml`), the KVM harness in `test/harness` (GitHub Actions `harness.yml`), Markdown documentation checked by `internal/docs`.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-4 | 5-7 | 8-10 | 11 | 12 | 13 | 14-16 | 17-19 | 20 | 21 | 22 | 23 | 24 | 25 | 26 | 27 | 28 | 29 | 30 | 31 | 32 | 33-34

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w10a-tmp/integ && export TMPDIR=/root/.cache/w10a-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 120 bash scripts/fuzz.test.sh && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w10a-tmp/wave && export TMPDIR=/root/.cache/w10a-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./internal/... ./cmd/... ./release/... && timeout 1200 make lint-web test-web`

**Plan review:** 2026-11-06 — dforge:judge-opus — executability 16 / coherence 17 / coverage 12 / assumptions 14 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`. It covers wave 10's row of Parallel decomposition ("fixes anywhere"), the threat model bullet of section 9 (R73) and the runbook bullet of section 10 (R81), the register rows the tasks' **Items:** lines name, and the rulings W10-R1 to W10-R14 under Assumptions. Out of scope, for plan `w10b`: the two-reviewer security review and its register, the fixes of its findings, the security-design rows W10-R10 sends to the panel, `CHANGELOG.md`, `docs/versioning.md`, `make release-check` and release-candidate readiness. Out of scope for the owner: the live demonstrations (row 55), the release prerequisites (row 35) and the 14-day production run (row 44).
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd, runs `systemctl`, `docker`, `ufw`, `iptables`, `nft`, `useradd`, `userdel` or `nmap`, or binds an address other than `127.0.0.1` (port 0, or the port Playwright's `webServer` picks in Task 5). It binds Unix sockets only under `t.TempDir()`. It never reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/run/darkpanel*` or `/proc` of another process. Host effects are tested through the existing fakes (`core.Runner`, `core.Units`, the Docker and NPM fakes, the file providers). Real installs, services and agents run only on harness VMs.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w10a-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first. When the task ends, remove it and run `go clean -testcache`. The reasons: `/tmp` is an 8 GiB tmpfs, and Unix socket paths over 108 bytes fail. When `df -h /` shows 90 % or more before a task's first build, trim the Go build cache (`go clean -cache` is acceptable).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml` or `web/pnpm-workspace.yaml`. After every task, `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every Go task all of these pass, with their real output recorded:
  - `go build ./...`
  - `go vet ./...` (no output)
  - `go test -count=1 ./...` (every line `ok` or `?`)
  - `go test -race -count=1` on the task's packages
  - `golangci-lint run --allow-serial-runners ./...` (`0 issues.`)
  - `go mod tidy -diff` (no output)

  A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. A task that edits `cmd/darkpanel/` also runs `go test -tags harness -count=1 ./cmd/darkpanel/` and `go vet -tags harness ./cmd/...`. A task that edits `web/` runs `make lint-web test-web`.
- Generated files: only Tasks 15 and 27 change a wire type, and each then runs `make schemas` and `make api-docs` and commits the regenerated `schemas/npm.*` and `docs/api/**`. Every other task leaves `schemas/**` and `docs/api/**` unchanged, so `TestAdAPIDocsCurrent` stays green.
- TDD: write the test, run it and see the expected RED (a build failure naming the first undefined identifier, or a failed assertion), then implement and run GREEN. Quote the RED and GREEN output in the report verbatim, not as a paraphrase. A task that skips a line of the full-suite constraint above says so and why in its report.
- Frozen files (spec 1.12). No task edits them except where its **Files:** block names the file:
  - `internal/core/**`
  - `internal/platform/*.go` (the package's own non-test files)
  - `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`
  - `internal/modules/modules.go`, `internal/cli/modules.go`, `cmd/darkpanel/paths.go`
  - `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`
  - `web/src/app.tsx`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`
  - `test/harness/pins.go`, `schemas/**`

  New `*_test.go` files in a frozen package are allowed. Production code outside these paths changes only where a task's **Files:** block names it. A task that finds it needs an unlisted file stops and reports it; it does not edit the file.
- File ownership: only Task 4 edits the spec; only Task 5 edits `docs/accessibility.md` and `docs/accessibility/`; only Tasks 15 and 27 edit `internal/modules/npm/wire.go` (in different waves); only Task 32 edits `web/src/modules/npm/types.ts`. No task edits `.github/workflows/**` or `Makefile`. A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave).
- Test isolation:
  - Files and databases go under `t.TempDir()`.
  - The only TCP listeners are `httptest` on `127.0.0.1` port 0.
  - Tests write no package-level mutable state, except an existing test seam (a package variable such as `serveRun`, `newSystemd` or `daemonRun`) that a helper replaces and restores through `t.Cleanup`; such tests do not call `t.Parallel`.
  - Vitest tests stub `fetch` through `stubFetch` and never reach a network.
  - Task 5's Playwright run starts its own Vite server on `127.0.0.1` with `strictPort: false` and answers every `/api/**` request from in-test fixtures (`page.route`).
  - Harness tests derive VM names, work directories and address ranges from the test name (`NewLab`). Harness unit tests carry `Unit` in their name, so `-run 'Unit'` selects them.

  Tasks of one wave therefore share no port, path or global state.
- Go identifiers: every new Go test function and every new unexported helper in a test file carries its task's prefix from Contracts "Test prefixes", so tasks that share a package declare no identifier another task declares. Web test cases join the component's existing test file, or a new one named after the component.
- Code style:
  - Match the surrounding code. Put a doc comment on every exported identifier.
  - Comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review. `//nolint` names the rule and a reason.
  - gofmt clean; in `web/`, eslint `--max-warnings 0` and `tsc --noEmit` clean.
  - Error strings start with the package name. A changed refusal keeps its existing error code unless the task names another.
- Documentation style (Tasks 3, 5, 33, 34): English, plain Markdown (ATX headings, one `#` per file, fenced code blocks tagged `sh`, `json` or `text`, no HTML), sentence-case headings, second person, present tense, and commands exactly as the CLI prints them (`sudo darkpanel …`). A page describes what exists on `main`; it never promises future work and never names a plan, wave, task or register row. Every relative link resolves to a file in the repository. The existing `internal/docs` style and link tests cover every page these tasks write.
- Secrets: no log line, error, test failure message, document example or screenshot carries a real token, password, TOTP secret, passphrase, tunnel token or private key. Examples use obvious placeholders (`<token-file>`, `example.com`, `203.0.113.10`).
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies to the branch once it is pushed, as well as on `main`, because Task 19 edits `test/harness/`. Dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag, and no task runs `release.yml`.

## Contracts

### Test prefixes

| Task | Package(s) | Go test prefix |
|---|---|---|
| 1 | `cmd/darkpanel` | `TestW10Cs` |
| 2 | `internal/controller/cloud`, `internal/controller/server` | `TestW10Pl` |
| 3 | `internal/modules/firewall/loaded`, `internal/modules`, `internal/docs` | `TestW10Sf` |
| 7 | `internal/modules/derp` | `TestW10Bu` |
| 8 | `internal/modules/derp` | `TestW10Rg` |
| 9 | `internal/modules/derp`, `internal/modules/derp/server` | `TestW10Sp` |
| 10 | `internal/modules/tailscale`, `internal/modules/tailnet`, `internal/modules/tailnet/policy` | `TestW10Tp` |
| 11 | `internal/controller/dispatch` | `TestW10Av` |
| 12 | `internal/modules/npm` | `TestW10Nu` |
| 13 | `internal/modules/npm` | `TestW10Nv` |
| 14 | `internal/modules/npm` | `TestW10Ns` |
| 15 | `internal/modules/npm` | `TestW10Nb` |
| 16 | `internal/modules/cloudflare` | `TestW10Cf` |
| 19 | `test/harness` | `TestW10Hx…Unit…` (the name contains `Unit`) |
| 20 | `internal/core/store`, `internal/modules/cloudflare`, `internal/modules/tailnet` | `TestW10Au` |
| 21 | `internal/modules/firewall` | `TestW10Fl` |
| 22 | `internal/controller/listen` | `TestW10Og` |
| 23 | `internal/modules/derp` | `TestW10Du` |
| 24 | `internal/modules/derp/artifact`, `internal/modules/derp/artifact/download`, `release/releasetool` | `TestW10Da` |
| 25 | `internal/modules/tailscale` | `TestW10Lp` |
| 26 | `internal/modules/npm`, `internal/modules/npm/npmapi` | `TestW10Nc` |
| 27 | `internal/modules/npm` | `TestW10Nl` |
| 28 | `internal/modules/npm` | `TestW10Nr` |
| 29 | `internal/controller/backup` | `TestW10Br` |
| 30 | `internal/controller/backup` | `TestW10Bc` |
| 31 | `internal/cli` | `TestW10Br` |
| 33 | `internal/docs` | `TestW10Tm` |
| 34 | `internal/docs` | `TestW10Rb` |

Unexported helpers in test files use the lower-case form (`w10cs…`). A test that extends an existing table-driven test adds rows to it and keeps its name.

### Audit phases of system services

`internal/core/store/migrations/controller/0015_audit_service_phases.sql` widens the `core_audit.phase` CHECK to the set of `0011_platform_surfaces.sql` plus `'check'` and `'delete'`. These are the phases that `system:cloudflare.hostnames` (`internal/modules/cloudflare/hostnames_service.go`) and `system:tailnet.keysweep` (`internal/modules/tailnet/keysweep.go`) write. The migration rebuilds the table the way `0011` does and keeps every existing row, index and column unchanged. No writer's phase string changes.

### NPM unreadable hosts (Tasks 27 and 32)

- Go (Task 27): `InstancesOutput` in `internal/modules/npm/wire.go` gains `Unreadable []string \`json:"unreadable"\``. It holds the host ids whose instance status read failed during `npm.instance.list`, sorted, as `[]` when there are none and never `null`. A host listed there is absent from `Instances`.
- Web (Task 32): `web/src/modules/npm/types.ts`'s `InstancesOutput` gains `unreadable: string[]`.
  - When `unreadable` is non-empty, the every-page banner (`web/src/modules/npm/banner.tsx`) renders a warning alert: `Could not check whether the NPM admin UI is exposed on: <names>.`, where `<names>` are the hosts' display names joined by `, `.
  - When the list read itself fails, the banner renders `Could not check whether the NPM admin UI is exposed.`
  - The existing admin-exposure warning is unchanged.

### Threat model outline (Task 33; plan `w10b`'s review brief reads it)

`docs/security/threat-model.md` has exactly these headings, in this order:

```text
# Threat model
## Scope
## Assets
## Actors
## Trust boundaries
### Browser and controller
### Proxy and controller
### Controller and agent
### Agent and host
### Controller and cloud APIs
## Areas
### Authentication
### Agent protocol
### Command execution
### Compose execution
### Secret handling and delivery
### Firewall
### Cloud credentials
### Exposure
### DERP host
## Known residual risks
```

- Each `### <area>` under `## Areas` holds exactly the three subheadings `#### Threats`, `#### Mitigations` and `#### Residual risk`, in that order, each with at least one bullet.
- Every bullet under `#### Mitigations` names, in backticks, a repository path that exists (a package directory or file).
- `## Known residual risks` lists every security question still open after this plan as a bullet; it never names a register row.

### Runbook outline

`docs/runbook.md` has exactly these headings, in this order:

```text
# Runbook
## Lockout
### Firewall break-glass
### Tailscale preferences revert
### Panel listener revoke
## Failed update
### Rollback
### Recover
## Lost controller
### Restore from backup
### Unsettled restore journal
### Re-enroll agents
```

- Every line inside an `sh` fence that starts with `sudo darkpanel ` or `darkpanel ` names an existing command path of the CLI tree and only flags that command declares. `TestW10Rb…` checks this through the `internal/docs` CLI helpers.
- `### Re-enroll agents` uses `darkpanel agent enroll` followed by an agent restart, never `system install --role agent`.
- `docs/user/README.md` links to the runbook.

## Assumptions (evidence)

- Every wave of the spec except 10 is planned and merged: `docs/dforge/plans/completed.md` lists w0a through w9; the last is `2026-11-05-v1-w9-hardening.md`, merged at 6c424f0 (read 2026-11-06).
- CI on `main` is green: `gh run list --workflow ci.yml --branch main` on 2026-11-06 returned run 41633622040 (2026-11-03, `success`). Rows 94 and 250 (self-hosted runner) therefore go to `verify`, not into a task.
- No tagged `golang.org/x/crypto` release carries the GO-2026-5932 fix: `go list -m -versions golang.org/x/crypto` on 2026-11-06 ends at `v0.58.0`, the version `go.mod` pins. Row 429 stays open for plan `w10b` to re-check.
- The audit CHECK refuses `check` and `delete`: `0011_platform_surfaces.sql:26-27` lists the phases, `hostnames_service.go:202` writes `Phase: "check"`, `keysweep.go:260` writes `Phase: "delete"` (read 2026-11-06).
- A cancelled startup reaches `OnFailure`: both units set `SuccessExitStatus=70` and `OnFailure=darkpanel-rollback.service` (`internal/install/units/darkpanel-agent.service:11,20`, `darkpanel-controller.service:9,19`); `cmd/darkpanel/main.go:238` exits with `run`'s status.
- The firewall module holds no lock of its own: `rg 'sync\.(RW)?Mutex|\.Lock\(\)' internal/modules/firewall/*.go` finds nothing outside tests (2026-11-06).
- `InstancesOutput` is `{Instances []HostInstance}` today (`internal/modules/npm/wire.go:230-232`), so the unreadable-host marker is a wire change.
- Every file the tasks' **Files:** blocks name as Modify exists on `main` at cdb37e7 (checked with a loop over the paths on 2026-11-06).
- External executors: the owner answered "none" to the agy lane offer on 2026-11-06, so the lane-eligible tasks (1-11, 13-19, 32-34) carry no Executor line, keep their parallel waves, and the plan is Claude-only.
- Plan review round 1 scored coverage 12 (borderline) for four unrouted register rows (341, 386, 389, 400) and two procedures the spec sends to the runbook; both were fixed (rulings W10-R11, W10-R13 and W10-R14, and the runbook's "Unsettled restore journal" section), and with no Critical finding and no score of 8 or below no second round ran.
- The dforge Codex review seat is off by setting (`codex-gate`: `usable=false reason=setting`, 2026-11-06). Whether `codex exec -m gpt-6-astra` runs for the owner's panel is unverified; plan `w10b` checks it before the review.

**Plan rulings** (Task 4 copies them into the spec):

- **W10-R1 two plans.** Wave 10 runs as two plans, because the security review's findings cannot be planned before the review runs.
  - `w10a` (this plan) fixes what is known and writes the threat model and the runbook.
  - The owner's two-reviewer panel (`dforge:judge-fable` and Codex `gpt-6-astra`, one shared brief built on `docs/security/threat-model.md`) then reviews `main`. Its findings go into `docs/dforge/registers/<date>-v1-security-review.md`.
  - `w10b` fixes them, re-reviews the critical and high ones, and writes `CHANGELOG.md` and `docs/versioning.md`. It also turns `make release-check` green.
- **W10-R2 triage.** An open follow-up row is fixed in `w10a` when it is Important or major, touches security with a fix its text settles, is a spec gap the spec already answers, or makes observable behaviour wrong. Pure tidiness (doc comments, duplicated helpers, redundant or weak-but-correct tests, process records) is deferred to post-v1.
- **W10-R3 TLS session resumption** (rows 209, 212, 225, 243). Session resumption stays per listener. No shared ticket keys are set: resumption across listeners buys nothing a browser needs.
- **W10-R4 contributed routes** (rows 218, 247). Route contributions are first-party, and the socket peer check still applies. A literal contributed route that shadows a built-in wildcard route is accepted, not refused.
- **W10-R5 netlink** (row 287). Spec 1.8, as Task 4 amends it, states that there is no third-party netlink library. `netstat.Interfaces` keeps `net.Interfaces`, which uses rtnetlink inside the standard library.
- **W10-R6 module availability** (row 298). A successful apply of a change of module M on host H retires the availability cache entry for (H, M). The next request re-probes it, so an install that brings the module up is seen at once.
- **W10-R7 tailnet validate** (row 425). Spec 4.2 follows the code: any message or data in the policy validate endpoint's answer is `tailnet.policy_invalid`.
- **W10-R8 wave 4 follow-ups** (row 301).
  - Fixed: the latency probe URL built from the DERP map (Task 25), and `package_pin.go`'s rollback on the forward context (Task 10).
  - The spec states the `vm` job's budget as the sum of its milestone budgets: 120 minutes for M1 and M2, 80 for the M3 firewall and 80 for M3 Docker, so 280 minutes in total.
  - The other items of `docs/dforge/notes/v1-w4-final-review-followups.md` are deferred to post-v1.
- **W10-R9 firewall host-state lock** (row 323). The firewall module in the agent holds one `sync.RWMutex` per module instance.
  - Every path that changes UFW's files, the loaded rules or the Docker block holds the write lock, from its first write until both address families have reloaded. These paths are apply, revert, the revert watchdog, repair, reconcile, snapshot restore and Docker cleanup.
  - Status reads, plan preparation and the Docker and exposure resource reads hold the read lock for their whole read.
  - Only the exported entry points take the lock. Internal helpers that a locked path calls take none, so no path takes the lock twice.
  - A separate break-glass process is not covered: it runs when the agent cannot be reached.
- **W10-R10 security-design rows go to the panel** (rows 245, 306, 326, 329, 330, 384). Each needs a security design choice: requiring Origin for cookie sessions, DERP probe target ranges, resolving NPM forward hosts, NPM's in-container admin port, NPM's port 3000, and Docker label digests. They stay open, go into the review brief, and are fixed in `w10b`.
- **W10-R12 the DERP grant warning** (row 303) moves to `w10b`. Carrying the visibility-grant warning into the output of `derp.region.remove` and `derp.server.remove` needs a `warnings` member on `RegionChanged` and `ServerRemoved`. It also needs a way for a composite to see a read step's output, because `internal/core/lifecycle/composite_run.go` skips `ClassRead` steps when it builds the composer's input, and `internal/core` is frozen. Both are interface changes this plan's file ownership does not admit.
- **W10-R11 deferred to post-v1:** rows 299, 305, 322, 341, 348, 349, 350, 352, 357, 358, 360, 361, 362, 363, 364, 367, 368, 369, 372, 374, 375, 376, 387, 388, 394, 396, 398, 422, 426, 427, 428, 430, 432. Each is tidiness, a process record, an accepted plan mandate, or a decision that changes a wire contract with no defect behind it.
- **W10-R13 verified, not fixed** (rows 386, 389, 400). Row 386 is a look at the Cloudflare connectors page and its Expose via tunnel dialog in a running browser, made with the owner's live demonstrations. Rows 389 and 400 are M3 Docker harness flakes and build-cache failures whose cause is unconfirmed; W10-R9's lock removes the torn read that row 400 names as a possible cause. This plan's gate 7 harness runs and full-suite runs show whether they recur, and a recurrence is diagnosed in `w10b`.
- **W10-R14 NPM password without the controller** (spec 1.x, the "No `--local`" bullet of reveal actions). The runbook covers Darkpanel's own procedures. A root operator without the controller restores the controller and reveals the password again, or resets it with NPM's own procedure, which NPM documents; the spec stops pointing at the runbook for it.

**Task scope.** Row numbers are those of `docs/dforge/registers/2026-10-21-v1-completion.md`, whose Item cell gives each finding with its file and line on `main`. A row split across tasks names the part each task takes; a part not named here is not in the plan.

| Task | Rows | Part of a split row |
|---|---|---|
| 1 | 392 | — |
| 2 | 185, 186, 227, 246 | — |
| 3 | 416, 421, 423 | 423: soften `docs/dev/module-guide.md` to what `internal/core/registry.go` enforces |
| 4 | 287, 301, 425 | 301: the `vm` job budget statement of W10-R8 |
| 5 | 420, 431 | — |
| 6 | 417, 419, 424 | — |
| 7 | 309, 310, 311 | 310: `binary_update.go:208-214` |
| 8 | 310, 312, 313, 314, 315 | 310: `servers.go:173-184`; 312: `region_edits.go:280-286`; 314: the `region_edits.go` items |
| 9 | 310, 312, 314 | 310: `server_probe.go`, `server_status.go`; 312: `server_status.go`, `server_probe.go`, `fallback_check.go`; 314: `server/server.go` Flags and `cert_renew.go` items |
| 10 | 301, 319, 418 | 301: `package_pin.go:240` rollback on the forward context |
| 11 | 298 | W10-R6 |
| 12 | 327, 351, 353, 354, 355 | — |
| 13 | 336, 338, 339, 340, 342, 343, 344 | 336: a second `npm.admin.expose` on a host that already has an admin exposure is refused with `npm.reserved`, detail naming the existing hostname, in `proxyhostAddHandler.check` |
| 14 | 345, 346 | — |
| 15 | 337, 359 | — |
| 16 | 382, 383 | — |
| 17 | 370, 371, 373 | — |
| 18 | 320, 385 | — |
| 19 | 321, 365, 366 | — |
| 20 | 390 | — |
| 21 | 323 | W10-R9 |
| 22 | 169 | normalise the public host (lower case, no trailing dot) where the runtime listener stores and opens it |
| 23 | 304, 307, 314 | 314: the `server_uninstall.go` items |
| 24 | 308, 316, 317 | — |
| 25 | 301, 318 | 301: the parked `latency.go:152` security item, with the fix the note describes |
| 26 | 331, 356 | 356: `controller_checks.go` and the Docker version string |
| 27 | 333, 334, 356 | 333: the Go side; 356: `instance_list.go:593-601` |
| 28 | 332, 347 | — |
| 29 | 393, 397 | 397: `Restore` returns the result of a finished restore whose cleanup failed |
| 30 | 395 | — |
| 31 | 397 | 397: the CLI parts |
| 32 | 333 | 333: the web side |
| 33 | — | R73's threat model; the review half stays with `w10b` |
| 34 | 399 | R81's runbook, including the manual procedure for an unsettled restore journal; the 14-day run stays with the owner |

## Task index

1. Cancelled startup exits cleanly
2. Cloud probe and listener shutdown fixes
3. Small correctness fixes
4. Spec amendments for the wave 10a rulings
5. Change dialog confirms composites awaiting confirmation
6. Accessibility fixes outside the core flows
7. DERP binary update rollback
8. DERP regions, composites and server passes
9. DERP status reads and probes
10. Tailscale pin rollback and tailnet fixes
11. Module availability after a successful change
12. NPM upgrade rollback
13. NPM proxy, claims and access-list validation
14. NPM status reads and admin exposures
15. NPM schema bounds
16. Cloudflare connector and hostname fixes
17. NPM web list validation and panel errors
18. DERP and Cloudflare web fixes
19. Harness test fixes
20. Audit rows of system services
21. Firewall reads under a host-state lock
22. Origin check normalises the public host
23. DERP service user ownership
24. DERP artifact and derper build checks
25. Tailscale latency probe URL checks
26. NPM client limits and public-check denials
27. NPM instance list reports unreadable hosts
28. NPM removal deletes upgrade archives
29. Restore sets staged files through one descriptor
30. Backup copy, key read and enroll-token fixes
31. Backup CLI passphrase, answer type and restore summary
32. NPM banner warns on unreadable hosts
33. Threat model
34. Runbook

---

### Task 1: Cancelled startup exits cleanly

**Files:**
- Modify: `cmd/darkpanel/main.go` (`newServeCmd`'s `RunE`; a new unexported helper `startupCancelled`)
- Modify: `cmd/darkpanel/agent.go` (`newAgentRunCmd`'s `RunE`)
- Test: `cmd/darkpanel/signal_test.go` (shared helpers), `cmd/darkpanel/serve_test.go`, `cmd/darkpanel/agent_test.go`
- Read only: `internal/install/units/darkpanel-agent.service`, `internal/install/units/darkpanel-controller.service`

**Interfaces:**
- Consumes: the seams the existing tests already replace: `serveRun`, `geteuid`, `daemonRun`, `newSystemd`, `selectPaths`, `installLayout`, and the test helpers `fakeServe`, `fakeAgent` and `runArgs`.
- Produces: no name another task consumes. `startupCancelled(ctx context.Context, err error) error` is unexported to package `main`.

**Items:** 392

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

Background. Both unit files set `SuccessExitStatus=70` and `OnFailure=darkpanel-rollback.service`. `darkpanel serve` and `darkpanel agent run` end a SIGTERM or SIGINT that arrives while they are still starting with an error that wraps `context.Canceled` (the signal context cancels `openAgent`, `daemon.Run` and `server.Run`'s startup). `main` turns any error into exit status 1, so systemd sees a failed unit and starts the rollback service. A stop signal during startup is a stop, not a failure, and must exit 0.

- [ ] **Step 1: Write the failing test**

Add these helpers to `cmd/darkpanel/signal_test.go` after the `hungServeEnv` constant. The file already imports `context`, `errors`, `fmt`, `os`, `syscall`, `testing` and `time`.

```go
// w10csInterrupt sends this test process SIGTERM and waits until ctx, the
// signal context of the command under test, has ended. Call it only from a
// seam that runs after the command installed its signal handler: with no
// handler the signal would end the test binary.
func w10csInterrupt(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Error("the signal context did not end within 10 s of SIGTERM")
	}
}

// w10csCancelled is the error a startup step returns when ctx ended under it.
func w10csCancelled(ctx context.Context) error { return fmt.Errorf("startup: %w", ctx.Err()) }

// w10csStrayCancel is a cancellation error from a context that no signal ended.
func w10csStrayCancel(context.Context) error { return fmt.Errorf("startup: %w", context.Canceled) }

// w10csFailure is a failure that is not a cancellation.
func w10csFailure(context.Context) error { return errors.New("the database is locked") }
```

Add this test to `cmd/darkpanel/serve_test.go` (its imports already cover everything it uses):

```go
// TestW10CsServeCancelledStartup shows that a signal which ends serve while
// it starts is a clean stop with exit status 0, and that no other error is
// hidden by it.
func TestW10CsServeCancelledStartup(t *testing.T) {
	tests := []struct {
		name   string
		signal bool
		result func(context.Context) error
		code   int
		out    string
	}{
		{"the signal cancels the startup", true, w10csCancelled, cli.ExitOK, ""},
		{"a failure that is not the cancellation", true, w10csFailure, cli.ExitError, "darkpanel: the database is locked"},
		{"a cancellation no signal asked for", false, w10csStrayCancel, cli.ExitError, "context canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_RUNTIME_DIR", "")
			fakeServe(t, 0, nil)
			serveRun = func(ctx context.Context, _ server.Options) error {
				if tt.signal {
					w10csInterrupt(ctx, t)
				}
				return tt.result(ctx)
			}
			out, code := runArgs("serve", "--dev")
			if code != tt.code || !strings.Contains(out, tt.out) {
				t.Errorf("darkpanel serve --dev: exit %d, output %q; want exit %d and %q", code, out, tt.code, tt.out)
			}
		})
	}
}
```

Add this test to `cmd/darkpanel/agent_test.go` (its imports already cover everything it uses). The `inOpen` rows put the signal inside `openAgent`, through the `newSystemd` seam that only the installed layout calls, which is why the test runs as root against the rooted temporary layout of `fakeAgent`.

```go
// TestW10CsAgentRunCancelledStartup shows that a signal which ends agent run
// while openAgent or the daemon is starting is a clean stop with exit status
// 0, that the opened state is still closed, and that no other error is hidden
// by it.
func TestW10CsAgentRunCancelledStartup(t *testing.T) {
	tests := []struct {
		name   string
		inOpen bool // the signal arrives while openAgent connects to systemd
		signal bool
		result func(context.Context) error
		code   int
		out    string
	}{
		{"the signal cancels openAgent", true, true, w10csCancelled, cli.ExitOK, ""},
		{"the signal cancels the daemon", false, true, w10csCancelled, cli.ExitOK, ""},
		{"openAgent fails for another reason", true, true, w10csFailure, cli.ExitError, "darkpanel: the database is locked"},
		{"the daemon fails for another reason", false, true, w10csFailure, cli.ExitError, "darkpanel: the database is locked"},
		{"a cancellation no signal asked for", false, false, w10csStrayCancel, cli.ExitError, "context canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 0)
			interrupt := func(ctx context.Context) {
				if tt.signal {
					w10csInterrupt(ctx, t)
				}
			}
			if tt.inOpen {
				newSystemd = func(ctx context.Context) (systemdConn, error) {
					interrupt(ctx)
					return nil, tt.result(ctx)
				}
			} else {
				daemonRun = func(ctx context.Context, cfg daemon.Config) error {
					s.runs = append(s.runs, cfg)
					interrupt(ctx)
					return tt.result(ctx)
				}
			}
			out, code := runArgs("agent", "run")
			if code != tt.code || !strings.Contains(out, tt.out) {
				t.Errorf("darkpanel agent run: exit %d, output %q; want exit %d and %q", code, out, tt.code, tt.out)
			}
			wantRuns, wantClosed := 1, 1
			if tt.inOpen {
				wantRuns, wantClosed = 0, 0
			}
			if len(s.runs) != wantRuns || s.systemd.closed != wantClosed {
				t.Errorf("the daemon ran %d times and the systemd seam closed %d times; want %d and %d", len(s.runs), s.systemd.closed, wantRuns, wantClosed)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w10a-tmp/1 && TMPDIR=/root/.cache/w10a-tmp/1 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestW10Cs' -v ./cmd/darkpanel/`
Expected: FAIL. `TestW10CsServeCancelledStartup/the_signal_cancels_the_startup` and the two `TestW10CsAgentRunCancelledStartup/the_signal_cancels_...` rows report `exit 1 ... want exit 0`. The other rows already pass because they pin that real failures and stray cancellations keep exit 1. Quote the failing lines in the report.

- [ ] **Step 3: Implement**

1. In `cmd/darkpanel/main.go` add an unexported function `startupCancelled(ctx context.Context, err error) error`. It returns nil when `err` is non-nil, `ctx.Err()` is non-nil and `errors.Is(err, context.Canceled)` holds; in every other case it returns `err` unchanged. Give it a doc comment that states the why: the units treat only exit status 70 as success and start the rollback service through `OnFailure=` on any other exit, so a stop signal that arrives while a command is still starting is a stop, not a failure. The check needs both conditions so that a cancellation error from some other context, or a real failure after the signal, still exits 1.
2. In `newServeCmd`'s `RunE`, return `startupCancelled(ctx, serveRun(ctx, server.Options{...}))` instead of returning `serveRun`'s result directly. `ctx` is the signal context the function already creates; the options stay as they are.
3. In `cmd/darkpanel/agent.go`, in `newAgentRunCmd`'s `RunE`:
   - when `openAgent(ctx, p, log)` fails, return `startupCancelled(ctx, err)`;
   - replace `errors.Join(daemonRun(ctx, cfg), host.Close())` with `errors.Join(startupCancelled(ctx, daemonRun(ctx, cfg)), host.Close())`, so the daemon's error alone is judged and an error from `host.Close()` is still reported.
4. Do not change `agent task`, `agent enroll`, `agent check`, the unit files or `cli.ExitCode`. The unit files are read only: confirm with `rg -n 'SuccessExitStatus|OnFailure' internal/install/units/darkpanel-agent.service internal/install/units/darkpanel-controller.service` that both still list `SuccessExitStatus=70` and `OnFailure=darkpanel-rollback.service`, and note in the report that a unit whose main process exits 0 ends inactive, not failed, so `OnFailure=` does not start.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w10a-tmp/1 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestW10Cs|TestSecondInterruptKillsServe|TestExitCodes|TestAgentRunDev|TestAgentRunProduction' -v ./cmd/darkpanel/`
Expected: PASS for every named test.

- [ ] **Step 5: Verify the task**

```sh
mkdir -p /root/.cache/w10a-tmp/1 && export TMPDIR=/root/.cache/w10a-tmp/1 GOTOOLCHAIN=local
df -h /
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 300 go vet -tags harness ./cmd/...
timeout 1500 go test -count=1 ./...
timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/
timeout 600 go test -race -count=1 ./cmd/darkpanel/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/1 && go clean -testcache
```

Expected: every command succeeds, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and every `go test` line is `ok` or `?`. Record the real output in the report.

- [ ] **Step 6: Commit**

```bash
git add cmd/darkpanel/main.go cmd/darkpanel/agent.go cmd/darkpanel/signal_test.go cmd/darkpanel/serve_test.go cmd/darkpanel/agent_test.go
git commit -m "fix(cmd): exit 0 when a signal ends startup"
```

### Task 2: Cloud probe and listener shutdown fixes

**Files:**
- Modify: `internal/controller/cloud/probe.go` (`ProbeReads`, the `Probe` doc comment)
- Test: `internal/controller/cloud/probe_test.go`, `internal/controller/cloud/credentials_test.go` (the sealing code in `credentials.go` is read only)
- Modify: `internal/controller/server/listeners.go` (the `"runtime listeners"` shutdown step in `startListeners`; a new unexported helper `halfOfRemaining`)
- Modify: `internal/controller/server/server.go` (the doc comment of `ShutdownTimeout`)
- Test: `internal/controller/server/listeners_test.go`

**Interfaces:**
- Consumes: the existing server test helpers `testOptions`, `baseURL` and the type `asDeps`.
- Produces: `cloud.ProbeReads` reports a `Probe` whose `Read` is nil as `Capability{State: CapabilityMissing, Detail: "internal"}` instead of panicking; its other results are unchanged, so the callers in `internal/modules/tailnet` and `internal/modules/cloudflare` need no change. No name another task consumes.

**Items:** 185, 186, 227, 246

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

In `internal/controller/cloud/probe_test.go` add `"errors"` to the imports and this test:

```go
// TestW10PlProbeReadsUnexpectedFailures shows that a probe whose read fails
// with an error that is not an errcode error, and a probe with no read at all,
// are reported as missing with the internal code and never panic or leak the
// error text.
func TestW10PlProbeReadsUnexpectedFailures(t *testing.T) {
	probes := []Probe{
		{Name: "devices:read", Read: func(context.Context) error { return errors.New("dial tcp 203.0.113.10:443: connection refused") }},
		{Name: "policy:read"},
		{Name: "keys:read", Read: func(context.Context) error { return nil }},
	}
	var got []Capability
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ProbeReads panicked: %v", r)
			}
		}()
		got = ProbeReads(t.Context(), probes, []string{"devices:write"})
	}()
	want := []Capability{
		{Name: "devices:read", State: CapabilityMissing, Detail: "internal"},
		{Name: "policy:read", State: CapabilityMissing, Detail: "internal"},
		{Name: "keys:read", State: CapabilityOK},
		{Name: "devices:write", State: CapabilityUnverified},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %+v, want %+v", got, want)
	}
}
```

In `internal/controller/cloud/credentials_test.go` add `"strings"` to the imports and this test. It pins behaviour that is already correct (the register row asks for the missing test), so it passes on the current tree.

```go
// TestW10PlOpenRefusesDamagedCiphertext shows that a ciphertext with a
// flipped bit or cut short does not open at the place it was sealed for, and
// that the refusal names the place but never the secret.
func TestW10PlOpenRefusesDamagedCiphertext(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		b := bytes.Clone(ct)
		b[i] ^= 0x01
		return b
	}
	tests := []struct {
		name string
		ct   []byte
	}{
		{"a flipped bit in the sealed data", flip(len(ct) - 1)},
		{"a flipped bit in the nonce", flip(1 + 8 + 3)},
		{"the last byte cut off", ct[:len(ct)-1]},
		{"the authentication tag cut off", ct[:len(ct)-16]},
		{"only the header", ct[:9]},
		{"nothing", nil},
	}
	const place = "cloud: open tailnet_accounts.secret_ct.acc1: "
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := OpenCredential(s, "tailnet_accounts", "secret_ct", "acc1", tt.ct)
			if err == nil {
				t.Fatalf("opened %q from a damaged ciphertext", pt)
			}
			if len(pt) != 0 {
				t.Errorf("returned %q with an error", pt)
			}
			if !strings.HasPrefix(err.Error(), place) || strings.Contains(err.Error(), "client-secret") {
				t.Errorf("error %q, want it to start with %q and not carry the secret", err, place)
			}
		})
	}
}
```

In `internal/controller/server/listeners_test.go` add this test (all its imports are already there). A raw TCP connection that sends nothing keeps the runtime listener's `http.Server.Shutdown` waiting until its context ends, which stands for a slow drain. The test shows that the network listener still stops well before the shared deadline.

```go
// TestW10PlShutdownKeepsTimeForTheNetworkListeners shows that a runtime
// listener which does not drain uses only part of Shutdown's time, so the
// network listeners, which stop after it, still stop before the deadline.
func TestW10PlShutdownKeepsTimeForTheNetworkListeners(t *testing.T) {
	opts := testOptions(t)
	c := &asDeps{}
	opts.Contributors = []ext.Controller{c}
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	spec := core.ListenerSpec{
		Addr: netip.MustParseAddrPort("127.0.0.1:9443"), AllowedPeer: netip.MustParseAddr("127.0.0.1"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.com", ChangeID: "01W10PLSHUTDOWNCHANGE0000",
	}
	if _, err := c.deps.Listeners.Open(context.Background(), spec); err != nil {
		t.Fatalf("Open: %v", err)
	}
	addrs, _ := s.runtimeListeners.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("runtime listeners bound at %v, want one", addrs)
	}
	var dialer net.Dialer
	held, err := dialer.DialContext(context.Background(), "tcp", addrs[0].String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	// The listener must have accepted the connection before Shutdown starts.
	time.Sleep(200 * time.Millisecond)

	network := strings.TrimPrefix(baseURL(t, s), "http://")
	const total = 4 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()
	began := time.Now()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(ctx) }()

	probe := net.Dialer{Timeout: 100 * time.Millisecond}
	stopped := false
	for time.Since(began) < 3*time.Second {
		conn, err := probe.DialContext(context.Background(), "tcp", network)
		if err != nil {
			stopped = true
			break
		}
		_ = conn.Close()
		time.Sleep(25 * time.Millisecond)
	}
	if !stopped {
		t.Errorf("the network listener still accepted connections %v into a Shutdown with a %v budget: the runtime listeners used it all", time.Since(began).Round(time.Millisecond), total)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stop runtime listeners") {
			t.Errorf("Shutdown = %v, want the error of the runtime listeners step: the held connection must delay it", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/2 && TMPDIR=/root/.cache/w10a-tmp/2 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW10Pl' -v ./internal/controller/cloud/ ./internal/controller/server/`
Expected:
- `TestW10PlProbeReadsUnexpectedFailures` FAILS with `ProbeReads panicked: runtime error: invalid memory address or nil pointer dereference`.
- `TestW10PlShutdownKeepsTimeForTheNetworkListeners` FAILS with `the network listener still accepted connections 3s ... into a Shutdown with a 4s budget`.
- `TestW10PlOpenRefusesDamagedCiphertext` PASSES on the current tree, because the sealing code is already correct; the row asked for the missing test. Say so in the report.

- [ ] **Step 3: Implement**

1. `ProbeReads` in `internal/controller/cloud/probe.go`: a probe whose `Read` is nil cannot prove its capability. Report it as `CapabilityMissing` with `Detail` `string(errcode.Internal)`, the same detail a read that fails with a non-errcode error already gets, and do not call it. Order and every other result stay as they are. Say in the `Probe` doc comment that a nil `Read` reports the capability as missing.
2. `internal/controller/server/listeners.go`: add an unexported function `halfOfRemaining(ctx context.Context) (context.Context, context.CancelFunc)`. When `ctx` has a deadline it returns a context derived from `ctx` whose deadline is now plus half of the time left until that deadline; when `ctx` has none it returns `context.WithCancel(ctx)`. Give it a doc comment that states the why: the runtime listeners stop before the network listeners and share one deadline with them, so a runtime listener that does not drain must not use all of it.
3. In `startListeners` replace `s.onShutdown("runtime listeners", s.runtimeListeners.Shutdown)` with a step of the same name that derives its context with `halfOfRemaining`, defers the cancel and calls `s.runtimeListeners.Shutdown` with it. Keep the step name `runtime listeners`, because the error text `server: stop runtime listeners: ...` is what the test reads. The order of the steps, the other steps' contexts, the contexts `Run` and `Start` create and the value of `ShutdownTimeout` do not change.
4. `internal/controller/server/server.go`: extend the doc comment of `ShutdownTimeout` with one sentence saying that the runtime listeners may use half of the time left when their step starts, so the network listeners that stop after them always keep the rest.

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/root/.cache/w10a-tmp/2 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW10Pl|TestProbeReads|TestCredential|TestShutdownClosesTheListeners|TestRunStopsWhenTheContextEnds|TestTxTopologyWaitsForRuntimeListeners' -v ./internal/controller/cloud/ ./internal/controller/server/`
Expected: PASS for every named test.

- [ ] **Step 5: Verify the task**

```sh
mkdir -p /root/.cache/w10a-tmp/2 && export TMPDIR=/root/.cache/w10a-tmp/2 GOTOOLCHAIN=local
df -h /
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/controller/cloud/ ./internal/controller/server/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/2 && go clean -testcache
```

Expected: every command succeeds, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and every `go test` line is `ok` or `?`. Record the real output in the report.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/cloud/probe.go internal/controller/cloud/probe_test.go
git commit -m "fix(cloud): treat a nil probe Read as missing"
git add internal/controller/cloud/credentials_test.go
git commit -m "test(cloud): cover damaged ciphertext"
git add internal/controller/server/listeners.go internal/controller/server/server.go internal/controller/server/listeners_test.go
git commit -m "fix(server): bound the runtime listeners' shutdown"
```

### Task 3: Small correctness fixes

**Files:**
- Modify: `internal/modules/firewall/loaded/read.go` (`toolVariant`)
- Test: `internal/modules/firewall/loaded/read_test.go`, `internal/modules/firewall/loaded/fuzz_test.go` (`internal/modules/firewall/preflight.go` is read only)
- Test: `internal/modules/registry_test.go` (`internal/core/registry.go` is read only)
- Modify: `docs/dev/module-guide.md` (the `**Migrations.**` paragraph)
- Test: `internal/docs/guide_test.go`

**Interfaces:**
- Consumes: the test helpers `ldRunner`, `ldResult` (in `read_test.go`), `dcRead` and `dgGuide` (in `internal/docs`), and `core.NewRegistry`, `core.Manifest`, `core.ResourceDef` from `internal/core`.
- Produces: no name another task consumes.

**Items:** 416, 421, 423

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

Three independent minor fixes that share no code.

- [ ] **Step 1: Write the failing tests**

1. `internal/modules/firewall/loaded/read_test.go`: add this test (all imports are already there). `checkTools` in `preflight.go` trims the whole `iptables --version` output before it takes the first line; `toolVariant` must read the same line.

```go
// TestW10SfToolVariantSkipsLeadingBlankLines shows that toolVariant reads the
// first line of the trimmed output, as the install preflight does.
func TestW10SfToolVariantSkipsLeadingBlankLines(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		variant string
		err     string
	}{
		{"no blank line", "iptables v1.8.10 (nf_tables)\n", variantNFT, ""},
		{"a leading blank line", "\niptables v1.8.10 (nf_tables)\n", variantNFT, ""},
		{"blank lines, spaces and a tab", "\n \n\tiptables v1.8.9 (legacy)\n", variantLegacy, ""},
		{"a leading blank line before an unknown variant", "\niptables v1.6.1\n", "", `loaded: cannot tell the iptables variant from "iptables v1.6.1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := &ldRunner{answers: map[string]ldResult{"/usr/sbin/iptables --version": {out: tt.out}}}
			variant, ok, err := toolVariant(context.Background(), x, "iptables")
			if tt.err != "" {
				if err == nil || err.Error() != tt.err || ok {
					t.Fatalf("toolVariant = %q, %v, %v; want the error %q", variant, ok, err, tt.err)
				}
				return
			}
			if err != nil || !ok || variant != tt.variant {
				t.Fatalf("toolVariant = %q, %v, %v; want %q", variant, ok, err, tt.variant)
			}
		})
	}
}
```

2. `internal/modules/firewall/loaded/fuzz_test.go`: change the oracle of `FuzzToolVariant` to the trimmed first line. In the doc comment, replace "reads the output as its first line ending in the variant's name" with "reads the output as its first line, after white space is trimmed from the whole output, ending in the variant's name". In the body, replace the line `line, _, _ := strings.Cut(out, "\n")` with:

```go
			line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
```

The seed `"\niptables v1.8.10 (nf_tables)\n"` is already in the corpus, so this edit alone makes the fuzz test fail on the current tree.

3. `internal/modules/registry_test.go`: add `"context"` to the imports and add this test. It compares the test's `resourceRule` with the rule `core.NewRegistry` enforces, so the two cannot drift again.

```go
// w10sfModule is a module whose manifest the test chooses.
type w10sfModule core.Manifest

func (m w10sfModule) Manifest() core.Manifest { return core.Manifest(m) }

// TestW10SfResourceRuleMatchesCore shows that the resource rule written out
// in this file accepts exactly the names core.NewRegistry accepts, so the
// test guards the rule core enforces and no looser one.
func TestW10SfResourceRuleMatchesCore(t *testing.T) {
	read := func(context.Context, core.Env) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
	tests := []struct {
		name     string
		accepted bool
	}{
		{"w10sf.status", true},
		{"w10sf.ports2", true},
		{"w10sf.docker-ports", false},
		{"w10sf.Status", false},
		{"w10sf.2status", false},
		{"w10sf", false},
		{"w10sf.a.b", false},
	}
	for _, tt := range tests {
		_, err := core.NewRegistry(w10sfModule{ID: "w10sf", Version: 1, Resources: []core.ResourceDef{{Name: tt.name, Read: read}}})
		if (err == nil) != tt.accepted {
			t.Errorf("core.NewRegistry with resource %q: error %v, want accepted = %v", tt.name, err, tt.accepted)
		}
		if got := resourceRule.MatchString(tt.name) && strings.HasPrefix(tt.name, "w10sf."); got != tt.accepted {
			t.Errorf("resourceRule on %q = %v, want %v, as core decides", tt.name, got, tt.accepted)
		}
	}
}
```

4. `internal/docs/guide_test.go`: add this test (its imports already cover it). It reads the `**Migrations.**` paragraph of the guide, which is one line.

```go
// TestW10SfGuideStatesWhatTheRegistryChecksOfMigrations shows that the guide
// says which part of the migration layout is a convention and which part the
// registry checks, so it does not promise more than internal/core enforces.
func TestW10SfGuideStatesWhatTheRegistryChecksOfMigrations(t *testing.T) {
	var paragraph string
	for _, line := range strings.Split(dcRead(t, dgGuide), "\n") {
		if strings.HasPrefix(line, "**Migrations.**") {
			paragraph = line
		}
	}
	if paragraph == "" {
		t.Fatalf("%s has no **Migrations.** paragraph", dgGuide)
	}
	for _, want := range []string{
		"By convention",
		"The registry checks only that the keys of `Migrations` are `core.SideController` and `core.SideAgent`",
		"the directory layout is not checked",
		"`NNNN_<name>.sql`",
	} {
		if !strings.Contains(paragraph, want) {
			t.Errorf("the Migrations paragraph of %s does not contain %q", dgGuide, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/3 && TMPDIR=/root/.cache/w10a-tmp/3 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW10Sf|FuzzToolVariant' -v ./internal/modules/firewall/loaded/ ./internal/modules/ ./internal/docs/`
Expected: FAIL.
- `TestW10SfToolVariantSkipsLeadingBlankLines` fails on the "a leading blank line" and "blank lines, spaces and a tab" rows with `loaded: cannot tell the iptables variant from ""`.
- `FuzzToolVariant/seed#2` fails with `ok is false for the first line ""`.
- `TestW10SfResourceRuleMatchesCore` fails with `resourceRule on "w10sf.docker-ports" = true, want false, as core decides`.
- `TestW10SfGuideStatesWhatTheRegistryChecksOfMigrations` fails with `does not contain "By convention"` and the two other missing phrases.

- [ ] **Step 3: Implement**

1. `toolVariant` in `internal/modules/firewall/loaded/read.go`: take the first line of `strings.TrimSpace(string(out))` instead of the first line of `string(out)`, exactly as `checkTools` does. The variant suffix checks, the error text and the `fs.ErrNotExist` handling do not change.
2. `internal/modules/registry_test.go`: change `resourceRule` to the pattern `internal/core/registry.go` calls `resourceNamePattern`: `^[a-z][a-z0-9]*\.[a-z][a-z0-9]*$`. Keep the variable's name, the other rules and the message of `TestRegistrationRules`. Do not touch `internal/core/registry.go`.
3. `docs/dev/module-guide.md`, the `**Migrations.**` paragraph (still one line). Rewrite it so that it says:
   - "By convention" a module with tables keeps its SQL migrations in `internal/modules/<id>/migrations/`, one directory for each side that has tables, `agent` and `controller`;
   - a sentence that contains exactly: "The registry checks only that the keys of `Migrations` are `core.SideController` and `core.SideAgent`; the directory layout is not checked.";
   - the migration runner refuses a file whose name does not match `NNNN_<name>.sql` (four digits, then lower-case letters, digits and underscores) when it applies the module's migrations, and numbers are unique and start at 1;
   - the three existing instructions stay: embed each directory with `//go:embed` and return it from `Manifest()` as an `fs.FS` for its side in `Migrations`, with the `internal/modules/derp/store.go` and `internal/modules/derp/derp.go` pointers; a migration that fails, or whose file changed after it was applied, marks the module unavailable; add a new file for each schema change and never edit one that has shipped.

   Follow `docs/dev` style: English, present tense, second person, no mention of any plan, wave, task or register row. Leave the line in "Rules the registry enforces" that reads "`Migrations` has only the keys `core.SideController` and `core.SideAgent`." as it is.

- [ ] **Step 4: Run tests to verify they pass**

Run: `TMPDIR=/root/.cache/w10a-tmp/3 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW10Sf|FuzzToolVariant|TestRegistrationRules|TestRead|TestDg' -v ./internal/modules/firewall/loaded/ ./internal/modules/ ./internal/docs/`
Expected: PASS for every named test.

- [ ] **Step 5: Verify the task**

```sh
mkdir -p /root/.cache/w10a-tmp/3 && export TMPDIR=/root/.cache/w10a-tmp/3 GOTOOLCHAIN=local
df -h /
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/firewall/loaded/ ./internal/modules/ ./internal/docs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/3 && go clean -testcache
```

Expected: every command succeeds, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and every `go test` line is `ok` or `?`. `TestAdAPIDocsCurrent` stays green because no wire type changed. Record the real output in the report.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/firewall/loaded/read.go internal/modules/firewall/loaded/read_test.go internal/modules/firewall/loaded/fuzz_test.go
git commit -m "fix(firewall): trim iptables version output"
git add internal/modules/registry_test.go
git commit -m "test(modules): check the core resource name rule"
git add docs/dev/module-guide.md internal/docs/guide_test.go
git commit -m "docs(dev): say what the registry checks"
```

### Task 4: Spec amendments for the wave 10a rulings

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (the only file this task edits): section 1.7 (the availability cache sentence), section 1.8 (the "Socket table and routes" bullet), section 4.2 (the "Policy (R59)" bullet), the Parallel decomposition "Test isolation" bullet, and a new bullet at the end of section 10
- Read: `docs/dforge/notes/v1-w4-final-review-followups.md`

**Interfaces:**
- Consumes: the rulings W10-R1 to W10-R14 as written in the block below.
- Produces: the "Plan rulings (wave 10a)" list at the end of spec section 10, which plan `w10b` and the security review brief read, and the four in-place amendments. No Go identifier.

**Items:** 287, 301, 425

**Depends on:** none

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

This task edits one Markdown file and runs no Go command. Do not touch any other file, and do not run the Go gates of this plan.

- [ ] **Step 1: Check the targets, then edit**

First confirm each sentence below is still on `main` with `rg -n '<anchor>' docs/dforge/specs/2026-10-21-v1-completion-design.md`; apply every edit by matching the quoted lines, never a line number. Keep the spec's hard wrap (about 78 columns, two-space indent inside bullets).

1. **Ruling W10-R6, section 1.7.** In the first bullet of "1.7 Per-action availability, scope and internal actions", find

```text
  an unavailable action with `module.unavailable` and the reason; the UI
  greys it out from the cached answer. Module preflight stays module-wide.
```

and replace it with

```text
  an unavailable action with `module.unavailable` and the reason; the UI
  greys it out from the cached answer. A successful apply of a change of
  module M on host H retires the cache entry for (H, M), so the next
  request re-probes it and an install that brings the module up is seen at
  once. Module preflight stays module-wide.
```

2. **Ruling W10-R5, section 1.8.** In the "Socket table and routes" bullet, find

```text
  joined to owning processes through `/proc/<pid>/fd`; interface addresses
  and route lookups (`RTM_GETROUTE`, including the reverse lookup for an
  incoming flow) through hand-written netlink on `golang.org/x/sys/unix`.
```

and replace it with

```text
  joined to owning processes through `/proc/<pid>/fd`; interface addresses
  through `net.Interfaces`, which uses rtnetlink inside the standard
  library; and route lookups (`RTM_GETROUTE`, including the reverse lookup
  for an incoming flow) through hand-written netlink on
  `golang.org/x/sys/unix`. There is no third-party netlink library.
```

3. **Ruling W10-R7, section 4.2.** In the "Policy (R59)" bullet, find

```text
  .../acl/validate` (a 200 with an empty body or `{}` is valid; a 200 with
  `{"message":"test(s) failed","data"}` or a 400 with `{message}` becomes
  `tailnet.policy_invalid`; whether a 200 with `{"message":"warning(s)
  found","data"}` also becomes `tailnet.policy_invalid` is an open point the
  integrator confirms before Tasks 17 and 39), and renders a line diff. Apply posts with
```

and replace it with

```text
  .../acl/validate` (a 200 with an empty body or `{}` is valid; any other
  200 that carries a message or data, including `{"message":"test(s)
  failed","data"}` and `{"message":"warning(s) found","data"}`, and a 400
  with `{message}` become `tailnet.policy_invalid`), and renders a line
  diff. Apply posts with
```

4. **Ruling W10-R8, the `vm` budget.** In the Parallel decomposition section, find the end of the "Test isolation" bullet

```text
  helper) and respect the VM concurrency limit; a job whose total passes its
  120-minute limit is split per milestone.
```

and replace it with

```text
  helper) and respect the VM concurrency limit; a job whose total passes 120
  minutes runs its milestones as separate steps, each with its own budget,
  and the job's limit is the sum of those budgets: for the harness `vm` job
  120 minutes for M1 and M2, 80 for the M3 firewall run and 80 for the M3
  Docker run, so 280 minutes in total.
```

5. **The list.** At the end of section 10, after the "Release candidate" bullet and before the heading `### 11. Error codes, actors and dependencies`, add a blank line and this bullet exactly (it follows the layout of the "Plan rulings (wave 9)" bullet of section 9):

````text
- **Plan rulings (wave 10a).** Wave 10a (plan
  `v1-w10a-known-fixes-threat-model-runbook`) settles these gaps and
  underdetermined points of the text above:
  - **W10-R1 two plans.** Wave 10 runs as two plans, because the security
    review's findings cannot be planned before the review runs.
    - `w10a` fixes what is known and writes the threat model and the
      runbook.
    - The owner's two-reviewer panel (`dforge:judge-fable` and Codex
      `gpt-6-astra`, one shared brief built on
      `docs/security/threat-model.md`) then reviews `main`. Its findings go
      into `docs/dforge/registers/<date>-v1-security-review.md`.
    - `w10b` fixes them, re-reviews the critical and high ones, and writes
      `CHANGELOG.md` and `docs/versioning.md`. It also turns
      `make release-check` green.
  - **W10-R2 triage.** An open follow-up row is fixed in `w10a` when it is
    Important or major, touches security with a fix its text settles, is a
    spec gap the spec already answers, or makes observable behaviour wrong.
    Pure tidiness (doc comments, duplicated helpers, redundant or
    weak-but-correct tests, process records) is deferred to post-v1.
  - **W10-R3 TLS session resumption** (rows 209, 212, 225, 243). Session
    resumption stays per listener. No shared ticket keys are set:
    resumption across listeners buys nothing a browser needs.
  - **W10-R4 contributed routes** (rows 218, 247). Route contributions are
    first-party, and the socket peer check still applies. A literal
    contributed route that shadows a built-in wildcard route is accepted,
    not refused.
  - **W10-R5 netlink** (row 287). Spec 1.8 states that there is
    no third-party netlink library. `netstat.Interfaces` keeps
    `net.Interfaces`, which uses rtnetlink inside the standard library.
  - **W10-R6 module availability** (row 298). A successful apply of a
    change of module M on host H retires the availability cache entry for
    (H, M). The next request re-probes it, so an install that brings the
    module up is seen at once.
  - **W10-R7 tailnet validate** (row 425). Spec 4.2 follows the code: any
    message or data in the policy validate endpoint's answer is
    `tailnet.policy_invalid`.
  - **W10-R8 wave 4 follow-ups** (row 301).
    - Fixed: the latency probe URL built from the DERP map, and
      `package_pin.go`'s rollback on the forward context.
    - The spec states the `vm` job's budget as the sum of its milestone
      budgets: 120 minutes for M1 and M2, 80 for the M3 firewall and 80 for
      M3 Docker, so 280 minutes in total.
    - The other items of `docs/dforge/notes/v1-w4-final-review-followups.md`
      are deferred to post-v1.
  - **W10-R9 firewall host-state lock** (row 323). The firewall module in
    the agent holds one `sync.RWMutex` per module instance.
    - Every path that changes UFW's files, the loaded rules or the Docker
      block holds the write lock, from its first write until both address
      families have reloaded. These paths are apply, revert, the revert
      watchdog, repair, reconcile, snapshot restore and Docker cleanup.
    - Status reads, plan preparation and the Docker and exposure resource
      reads hold the read lock for their whole read.
    - Only the exported entry points take the lock. Internal helpers that a
      locked path calls take none, so no path takes the lock twice.
    - A separate break-glass process is not covered: it runs when the agent
      cannot be reached.
  - **W10-R10 security-design rows go to the panel** (rows 245, 306, 326,
    329, 330, 384). Each needs a security design choice: requiring Origin
    for cookie sessions, DERP probe target ranges, resolving NPM forward
    hosts, NPM's in-container admin port, NPM's port 3000, and Docker label
    digests. They stay open, go into the review brief, and are fixed in
    `w10b`.
  - **W10-R11 deferred to post-v1:** rows 299, 305, 322, 341, 348, 349,
    350, 352, 357, 358, 360, 361, 362, 363, 364, 367, 368, 369, 372, 374,
    375, 376, 387, 388, 394, 396, 398, 422, 426, 427, 428, 430, 432. Each is
    tidiness, a process record, an accepted plan mandate, or a decision that
    changes a wire contract with no defect behind it.
  - **W10-R12 the DERP grant warning** (row 303) moves to `w10b`: carrying
    the visibility-grant warning into the output of `derp.region.remove` and
    `derp.server.remove` needs a `warnings` member on `RegionChanged` and
    `ServerRemoved`, and a way for a composite to see a read step's output
    (`internal/core/lifecycle/composite_run.go` skips `ClassRead` steps when
    it builds the composer's input, and `internal/core` is frozen).
  - **W10-R13 verified, not fixed** (rows 386, 389, 400). Row 386 is a look
    at the Cloudflare connectors page and its Expose via tunnel dialog in a
    running browser, made with the owner's live demonstrations. Rows 389 and
    400 are M3 Docker harness flakes and build-cache failures whose cause is
    unconfirmed; W10-R9's lock removes the torn read that row 400 names as a
    possible cause. The harness and full-suite runs of wave 10a show whether
    they recur, and a recurrence is diagnosed in `w10b`.
  - **W10-R14 NPM password without the controller.** The runbook covers
    Darkpanel's own procedures. A root operator without the controller
    restores the controller and reveals the password again, or resets it
    with NPM's own procedure, which NPM documents.
````

6. **Ruling W10-R14, section 1.** In the "No `--local`." bullet of the reveal actions, find

```text
  A root operator on the host without the controller recovers NPM's
  password with NPM's own reset procedure (the runbook, 9), not through
  Darkpanel.
```

and replace it with

```text
  A root operator on the host without the controller restores the
  controller (the runbook's "Lost controller", 10) and reveals the password
  again, or resets it with NPM's own procedure, not through Darkpanel.
```

- [ ] **Step 2: Verify the edits**

Run:

```sh
S=docs/dforge/specs/2026-10-21-v1-completion-design.md
for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14; do printf 'W10-R%s: ' "$n"; grep -c "^ *- \*\*W10-R$n " $S; done
grep -n 'Plan rulings (wave 10a)' $S
grep -n 'open point the' $S
grep -n 'is split per milestone' $S
grep -n 'no third-party netlink library\|There is no third-party netlink library' $S
grep -n 'retires the cache entry for (H, M)' $S
grep -n '280 minutes in total' $S
grep -n '(the runbook, 9)' $S
grep -n 'and reveals the password' $S
git diff --check
git diff --name-only
```

Expected: each `W10-R<n>` line prints `1`; `Plan rulings (wave 10a)` prints one line, located after the "Release candidate" bullet and before `### 11.`; `open point the` and `is split per milestone` print nothing; the netlink phrase prints two lines (the amended 1.8 bullet and W10-R5); `retires the cache entry for (H, M)` prints one line; `280 minutes in total` prints two lines (the Test isolation bullet and W10-R8); `(the runbook, 9)` prints nothing; `and reveals the password` prints one line; `git diff --check` prints nothing; `git diff --name-only` lists only `docs/dforge/specs/2026-10-21-v1-completion-design.md`.

Then re-read the five edited sentences in place with `sed -n` over each anchor (1.7's cache sentence, 1.8's "Socket table and routes" bullet, 4.2's "Policy (R59)" bullet, the Test isolation bullet, the "No `--local`." bullet) and the new list from its heading to `### 11.`. Confirm each reads as grammatical English in the spec's style, that no other sentence of the spec still contradicts it (`rg -n 'cached answer|retired by|hello or module enable' $S` shows nothing that says the cache is retired only by `hello` or a module enable), and quote the re-read sentences in the report.

- [ ] **Step 3: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): record the wave 10a rulings"
```

### Task 5: Change dialog confirms composites awaiting confirmation

**Files:**
- Modify: `web/src/components/change-dialog.tsx` (the `Applied` component, a new helper `lastStepDeadline`, and the `DialogHeader` of `ChangeDialog`)
- Modify: `web/src/components/change-dialog.test.tsx` (append the tests below)
- Modify: `web/src/components/change-dialog.a11y.test.tsx` (append the test below)
- Modify: `web/e2e/a11y.pw.ts` (a header clearance check in the reflow and zoom checks)
- Modify: `docs/accessibility.md` (Scope paragraph on composites, Known issues, last-run line)
- Modify: `docs/accessibility/*.png` (the screenshots the Playwright pass rewrites)
- Not modified: `web/playwright.config.ts` (read it; the pass runs through it as it is)

**Interfaces:**
- Consumes: `confirmChange`, `revertChange`, `listSteps`, `stepsQueryKey`, `changeQueryKey` from `@/api/client` and `Step` from `@/api/types`, all existing. The controller's composite change view has `class: "composite"` and `state: "awaiting_confirmation"`, and its last step (highest `ordinal`) has the revertible child's deadline in `childDeadline`.
- Produces: no name another task uses. `ChangeDialogProps` and `ChangeTarget` do not change.

**Items:** 420, 431

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

The two rows. Row 420: the change dialog offers Confirm and Revert only for state `pending`, so a composite in `awaiting_confirmation` (`derp.server.deploy`, `derp.server.remove`, `panel.exposure.add`, `panel.exposure.remove`, `npm.instance.deploy-public` with the integration on) has no Confirm in the web, although spec 1.2 and 2.4 give it a countdown with Confirm and Revert. A composite change's own `revertDeadline` is `null`; the deadline of the revertible last step is in that step's `childDeadline`, which `GET /api/v1/changes/<id>/steps` returns. Row 431: the dialog's close button sits over the top corner of the content and the header reserves no space for it, so at 320 px a long title runs under the button. The Playwright pass of `web/e2e/a11y.pw.ts` visits only `/login`, `/` (the hosts page), `/hosts/<id>/firewall` and the `/hosts/<id>/docker` pages, none of which renders a view Task 6 changes in the same wave, so its screenshots stay true after both merge.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/components/change-dialog.test.tsx`. The file already imports `act`, `screen`, `waitFor`, `within`, `userEvent`, `vi` and the types `Change`, `Guard`, `Plan`, `Step`, and defines `cdxPlan`, `cdxChange`, `cdxRender`, `PLAN`, `APPLY`:

```tsx
function cdxStep(over: Partial<Step> = {}): Step {
  return {
    id: "ports", ordinal: 1, host: "h1", action: "cdx.ports.open/v1", state: "applied", attempt: 1, childChangeId: "k1",
    earlierChildChangeIds: null, childDeadline: null, error: null, ...over,
  }
}

const cdxAwaiting = () => cdxChange({ class: "composite", state: "awaiting_confirmation" })

test("a composite awaiting confirmation counts down to its last step's deadline and can be confirmed", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date("2026-10-23T08:00:00Z"))
  const steps = [
    cdxStep({ id: "network", ordinal: 1, action: "cdx.net.create/v1", childDeadline: "2026-10-23T08:00:10Z" }),
    cdxStep({ id: "ports", ordinal: 2, childDeadline: "2026-10-23T08:01:00Z" }),
  ]
  const completed = cdxChange({ class: "composite", state: "completed" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: cdxAwaiting() },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/confirm")) ? completed : cdxAwaiting() }),
    "GET /api/v1/changes/c1/steps": { body: { steps } },
    "POST /api/v1/changes/c1/confirm": { body: completed },
  })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("timer")).textContent).toBe("Reverts automatically in 60 s unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(within(dialog).getByRole("timer").textContent).toBe("Reverts automatically in 55 s unless confirmed.")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
  expect((await within(dialog).findByText("completed")).textContent).toBe("completed")
  expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/changes/c1/confirm")).toBe(true)
  expect(within(dialog).queryByRole("timer")).toBeNull()
  expect(within(dialog).queryByRole("button", { name: "Confirm" })).toBeNull()
})

test("a composite awaiting confirmation can be reverted", async () => {
  const undoing = cdxChange({ class: "composite", state: "undoing" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: cdxAwaiting() },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/revert")) ? undoing : cdxAwaiting() }),
    "GET /api/v1/changes/c1/steps": { body: { steps: [cdxStep({ childDeadline: "2099-01-01T00:00:00Z" })] } },
    "POST /api/v1/changes/c1/revert": { body: undoing },
  })
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await user.click(await within(dialog).findByRole("button", { name: "Revert" }))
  expect((await within(dialog).findByText("undoing")).textContent).toBe("undoing")
  expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/changes/c1/revert")).toBe(true)
  expect(within(dialog).queryByRole("button", { name: "Revert" })).toBeNull()
  expect(within(dialog).queryByRole("button", { name: "Confirm" })).toBeNull()
})

test("a composite awaiting confirmation whose last step has no deadline offers Confirm and Revert without a countdown", async () => {
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: cdxAwaiting() },
    "GET /api/v1/changes/c1": { body: cdxAwaiting() },
    "GET /api/v1/changes/c1/steps": { body: { steps: [cdxStep()] } },
  })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("button", { name: "Confirm" })
  expect(within(dialog).getByRole("button", { name: "Revert" })).toBeTruthy()
  await within(dialog).findByRole("list", { name: "Steps" })
  expect(within(dialog).queryByRole("timer")).toBeNull()
})

test("a composite that is still applying offers neither Confirm nor Revert", async () => {
  const applying = cdxChange({ class: "composite", state: "applying" })
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: applying },
    "GET /api/v1/changes/c1": { body: applying },
    "GET /api/v1/changes/c1/steps": { body: { steps: [cdxStep({ state: "applying", childDeadline: "2099-01-01T00:00:00Z" })] } },
  })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("list", { name: "Steps" })
  expect(within(dialog).queryByRole("button", { name: "Confirm" })).toBeNull()
  expect(within(dialog).queryByRole("button", { name: "Revert" })).toBeNull()
  expect(within(dialog).queryByRole("timer")).toBeNull()
})

test("the header reserves room for the close button at its end", async () => {
  cdxRender({ [PLAN]: { body: cdxPlan() } })
  const dialog = await screen.findByRole("dialog")
  const header = within(dialog).getByRole("heading", { name: "Allow HTTPS" }).parentElement
  expect(header?.classList.contains("pe-7")).toBe(true)
})
```

Append to `web/src/components/change-dialog.a11y.test.tsx`. The file already imports `Step`, `within`, `expectNoAxeViolations` and defines `cdaOpen`, `cdaPlan`, `cdaChange`, `PLAN`, `APPLY`:

```tsx
test("a composite awaiting confirmation with its steps, countdown, Confirm and Revert has no violations", async () => {
  const steps: Step[] = [
    { id: "ports", ordinal: 1, host: "h1", action: "cda.ports.open/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: "2099-01-01T00:00:00Z", error: null },
  ]
  const awaiting = cdaChange({ class: "composite", state: "awaiting_confirmation" })
  const { dialog, user } = await cdaOpen({
    [PLAN]: { body: cdaPlan() },
    [APPLY]: { status: 202, body: awaiting },
    "GET /api/v1/changes/c1": { body: awaiting },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
  })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("timer")
  expect(within(dialog).getByRole("button", { name: "Confirm" })).toBeTruthy()
  expect(within(dialog).getByRole("button", { name: "Revert" })).toBeTruthy()
  await within(dialog).findByRole("list", { name: "Steps" })
  await expectNoAxeViolations(dialog)
})
```

In `web/e2e/a11y.pw.ts`, add this function after `clippedInDialog`:

```ts
// The dialog's close button is positioned over the top corner of its content, so a header that reserves no
// space for it lets a long title run under it. This lists every child of the dialog's header whose box reaches
// the button's box.
async function headerUnderCloseButton(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const dialog = document.querySelector('[role="dialog"]')
    if (dialog === null) return []
    const titleId = dialog.getAttribute("aria-labelledby")
    const header = titleId === null ? null : (document.getElementById(titleId)?.parentElement ?? null)
    const close = Array.from(dialog.querySelectorAll("button")).find((el) => getComputedStyle(el).position === "absolute")
    if (header === null || close === undefined) return ["the dialog has no header or no corner close button"]
    const button = close.getBoundingClientRect()
    const reached: string[] = []
    for (const child of Array.from(header.children)) {
      const box = child.getBoundingClientRect()
      const sharesRows = box.top < button.bottom && box.bottom > button.top
      if (sharesRows && box.right > button.left + 0.5) {
        reached.push(`${child.tagName.toLowerCase()} ends at ${Math.round(box.right)}, the close button starts at ${Math.round(button.left)}`)
      }
    }
    return reached
  })
}
```

and, in the `for (const { check, viewport, scale } of CHECKS)` loop, in the `else` branch that holds the `scrollWidth` and `clippedInDialog` assertions, add after the `clippedInDialog` assertion:

```ts
            expect(await headerUnderCloseButton(page), "dialog header content under the close button").toEqual([])
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/5 && export TMPDIR=/root/.cache/w10a-tmp/5 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 pnpm -C web exec vitest run src/components/change-dialog.test.tsx src/components/change-dialog.a11y.test.tsx
```

Expected FAIL: "a composite awaiting confirmation counts down…" and "…can be reverted" cannot find the `timer` or the `Confirm` and `Revert` buttons; "…without a countdown" cannot find `Confirm`; "the header reserves room…" gets `false`; the a11y test cannot find `timer`. "a composite that is still applying offers neither…" passes today: it guards against showing the buttons for every composite state. Quote the output.

Run the browser pass before the fix. Playwright starts its own Vite server on 127.0.0.1 (`strictPort: false`) and a headless Chromium, and answers every `/api/**` request from the fixtures:

```sh
export TMPDIR=/root/.cache/w10a-tmp/5 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 900 pnpm -C web exec playwright test -c playwright.config.ts
```

If Playwright says its pinned Chromium is missing, set `E2E_CHROMIUM` to the newest build in the cache and run again: `E2E_CHROMIUM=$(ls -d /root/.cache/ms-playwright/chromium-*/chrome-linux64/chrome | sort -V | tail -1)`. Expected FAIL: the 16 tests `reflow-320 <view> <theme>` and `zoom-200 <view> <theme>` of the four views that end in a dialog (`firewall-change-plan`, `firewall-change-applied`, `container-actions-dialog`, `expose-plan`, each light and dark) fail on "dialog header content under the close button"; the other 72 pass. A failing test writes no screenshot, so `git status --short docs/accessibility` shows the screenshots of the passing tests only. Quote the first failure.

- [ ] **Step 3: Implement**

In `web/src/components/change-dialog.tsx`:

1. Add the helper `lastStepDeadline(steps: Step[] | undefined): string | null`, with a doc comment. It returns the `childDeadline` of the step with the highest `ordinal` when that step's `state` is `"applied"` or `"confirming"` and its `childDeadline` is not `null`, and `null` otherwise (no steps, a last step in another state, no deadline). Import the type `Step` from `@/api/types`.
2. In `Applied`, after `const current = change.data`, add a second query for the steps: `useQuery` with `queryKey: stepsQueryKey(applied.id)`, `queryFn: () => listSteps(applied.id)` and `enabled: current.class === "composite"`. The `Steps` component runs the same query key, so the two share one request. Event `composite.step` already invalidates that key.
3. Replace the single `counting` flag by these values:
   - `awaitingComposite` is `current.class === "composite" && current.state === "awaiting_confirmation"`.
   - The deadline is `current.revertDeadline` when `current.state === "pending"`, `lastStepDeadline(steps.data)` when `awaitingComposite`, and `null` otherwise. The time the deadline was read at is `change.dataUpdatedAt` for the first and `steps.dataUpdatedAt` for the second. Pass both to `useSecondsLeft` as today.
   - `counting` is now "the deadline is not `null`". The announcement logic (`spoken`, `shouldAnnounce`, `progress`) keeps its code and uses the new `counting`, so a composite's countdown is announced at 30 s and 10 s as a revertible change's is.
4. The block with the countdown, Confirm and Revert shows when `(current.state === "pending" && current.revertDeadline !== null) || awaitingComposite`. The `Countdown` inside it shows only when the seconds left are not `null`, so a composite whose last step has no deadline gets the two buttons and no countdown. The buttons, their labels, `busy` handling and `decide` stay as they are.
5. In `decide`, after `client.setQueryData(...)`, when the change is a composite, also `void client.invalidateQueries({ queryKey: stepsQueryKey(current.id) })` so the step list shows the confirmed or undone step.
6. In `ChangeDialog`, give the `DialogHeader` the class `pe-7`: the close button of `darkraise-ui` is `h-7 w-7` (1.75 rem), so `pe-7` pads the header's end by the button's width.

Do not change `change.tsx`, `change-actions.tsx` or any other page: they use their own buttons.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/5 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 pnpm -C web exec vitest run src/components/change-dialog.test.tsx src/components/change-dialog.a11y.test.tsx
```

Expected: PASS, every test of both files. Then run the browser pass again:

```sh
timeout 900 pnpm -C web exec playwright test -c playwright.config.ts
```

Expected: all 88 tests pass, and the pass rewrites the screenshots under `docs/accessibility/`. Run `git status --short docs/accessibility` and keep every rewritten file: the pass is the refresh of the reflow evidence (W9-R6). Open `docs/accessibility/expose-plan-light-reflow-320.png`, `docs/accessibility/expose-plan-dark-reflow-320.png` and `docs/accessibility/firewall-change-plan-light-reflow-320.png` with the Read tool and check that the title ends clear of the close button. Then confirm Playwright left nothing running: `pgrep -af 'vite --host 127.0.0.1'` prints nothing.

- [ ] **Step 5: Update `docs/accessibility.md`**

Three edits, in the page's documentation style (second person where it addresses the reader, present tense, no mention of a plan, wave, task or register row):

1. In Scope, replace the paragraph that says a composite change that waits for confirmation gets no Confirm or Revert with: the confirm and revert states covered are those of a revertible change, and a composite change that waits for confirmation, such as a public deploy of Nginx Proxy Manager with the firewall integration on, shows the same countdown with Confirm and Revert, counted to the deadline of its last step. State that the Vitest tests of the change dialog (`web/src/components/change-dialog.test.tsx` and `web/src/components/change-dialog.a11y.test.tsx`) check that state and that the browser pass does not render it.
2. In Known issues, delete the bullet about the expose dialog's title ending a few pixels from its close button. Keep the first bullet (the scrolling "Planned changes" block) unchanged. The section keeps its heading and a non-empty body.
3. In the paragraph under the commands, replace the date and the Chromium version of the last run with today's date (`date -u +%F`) and the version the run used (`"$E2E_CHROMIUM" --version`, or the pinned build's `chrome --version`), and keep "passed all 88 checks" only if the final Playwright summary says 88 passed.

- [ ] **Step 6: Verify and commit**

```sh
export TMPDIR=/root/.cache/w10a-tmp/5 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 1200 make lint-web test-web
timeout 300 go mod tidy -diff
git status --short
```

Expected: `make lint-web test-web` ends with every Vitest file passing (including `src/test/accessibility-doc.test.ts`), eslint with no warning and `tsc` with no error; `go mod tidy -diff` prints nothing; `git status --short` lists only the files of the **Files:** block. Remove the scratch directory and the test cache: `rm -rf /root/.cache/w10a-tmp/5 && go clean -testcache`.

```sh
git add web/src/components/change-dialog.tsx web/src/components/change-dialog.test.tsx web/src/components/change-dialog.a11y.test.tsx web/e2e/a11y.pw.ts docs/accessibility.md docs/accessibility
git commit -m "fix(web): confirm composites awaiting confirmation"
```

### Task 6: Accessibility fixes outside the core flows

**Files:**
- Modify: `web/src/pages/create-token-dialog.tsx` (the `ClipboardLabel` and `ClipboardInput` of the created token)
- Modify: `web/src/modules/tailnet/keys-page.tsx` (the `ClipboardLabel` and `ClipboardInput` of the created key)
- Modify: `web/src/pages/audit.tsx` (`AuditBody`)
- Test: `web/src/pages/create-token-dialog.test.tsx`
- Test: `web/src/modules/tailnet/keys-page.test.tsx`
- Test: `web/src/pages/audit.a11y.test.tsx`
- Test: `web/src/modules/firewall/docker-panel.a11y.test.tsx` (the focus tests of `DockerSources` run through the panel that owns its `fallbackFocus`)
- Not modified: `web/src/modules/firewall/docker-sources.tsx` (read it; it is correct and only gains tests), `web/src/pages/create-host-dialog.tsx` (the pattern to copy: its `ClipboardLabel` has `htmlFor="host-grant-secret"` and its `ClipboardInput` has `id="host-grant-secret"`)

**Interfaces:**
- Consumes: `ClipboardLabel` and `ClipboardInput` of `darkraise-ui/components/clipboard` (the label renders a plain `<label>` that takes `htmlFor`, the input renders an `<input readOnly type="text">` that takes `id`), `DockerPanel` and `fwDocker`, the `FirewallChange` fallback focus described in its doc comment.
- Produces: nothing another task uses.

**Items:** 417, 419, 424

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

Three rows. Row 417: the read-only clipboard inputs that show the created API token and the created auth key have no accessible label (the Add host dialog's was fixed by tying the label to the input with an id). Row 419: when the last page of the audit log loads, the "Load older entries" button unmounts and keyboard focus drops to the document body. Row 424: no test covers the focus that `DockerSources` hands to its `fallbackFocus` after the Remove exemption and Add proposed exemption buttons are gone, or after Set private sources is disabled; a regression that drops focus to the body there would pass.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/pages/create-token-dialog.test.tsx` (it already defines `PLAN`, `APPLY`, `plan`, `created`, `change` and `fillAndCreate`, and imports `screen`, `within`, `userEvent`, `stubFetch`, `renderWithProviders`):

```tsx
test("the token the dialog shows is a read-only field named for the token", async () => {
  stubFetch({ [PLAN]: { body: plan }, [APPLY]: { body: change("completed", created) } })
  renderWithProviders(<CreateTokenDialog />)
  const dialog = await fillAndCreate(userEvent.setup())
  const field = await within(dialog).findByRole("textbox", { name: "Token ci" })
  expect((field as HTMLInputElement).value).toBe("dpt_example_secret")
  expect((field as HTMLInputElement).readOnly).toBe(true)
  expect(await axe(dialog)).toHaveNoViolations()
})
```

and add `import { axe } from "vitest-axe"` to that file's imports.

Append to `web/src/modules/tailnet/keys-page.test.tsx` (it already defines `kyRender`, `KY_APPLY`, `kyChange`, `KY_SECRET` and imports `axe`, `screen`, `waitFor`, `within`, `userEvent`):

```tsx
test("the key the dialog shows is a read-only field named for the key", async () => {
  kyRender({ [KY_APPLY]: kyChange() })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.click(screen.getByRole("button", { name: "Create key" }))
  const plan = await screen.findByRole("dialog", { name: "Create an auth key" })
  const apply = within(plan).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  const done = await screen.findByRole("dialog", { name: "Auth key created" })
  const field = await within(done).findByRole("textbox", { name: "Auth key darkpanel-c3" })
  expect((field as HTMLInputElement).value).toBe(KY_SECRET)
  expect((field as HTMLInputElement).readOnly).toBe(true)
  expect(await axe(done)).toHaveNoViolations()
})
```

In `web/src/pages/audit.a11y.test.tsx`, change the first import line to `import { screen, waitFor } from "@testing-library/react"` and the `@/test/a11y` import to `import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"`, then append (the file already defines `AUDIT`, `entry`, `pages`):

```tsx
test("loading the last page leaves the keyboard focus in the log, on the end of the list", async () => {
  stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  const user = userEvent.setup()
  await screen.findByText("core.token.create/v1")
  screen.getByRole("button", { name: "Load older entries" }).focus()
  await user.keyboard("{Enter}")
  await screen.findByText("core.auth.login/v1")
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load older entries" })).toBeNull())
  expect(document.activeElement).not.toBe(document.body)
  expect(document.activeElement?.textContent).toBe("No older entries.")
})

// Whether the browser also keeps focus on a button while it is disabled for loading is up to the browser; this
// pins that the button has it again when the load ends.
test("the load button has the focus again after a page that is not the last", async () => {
  stubFetch({
    [AUDIT]: (call) => {
      const before = (call.body as { input: { before?: string } }).input.before
      if (before === undefined) return { body: { output: { entries: [entry("a3", "core.token.create")], nextBefore: "a3" } } }
      if (before === "a3") return { body: { output: { entries: [entry("a2", "core.auth.login")], nextBefore: "a2" } } }
      return { body: { output: { entries: [entry("a1", "core.auth.logout")], nextBefore: null } } }
    },
  })
  renderWithProviders(<AuditPage />)
  const user = userEvent.setup()
  await screen.findByText("core.token.create/v1")
  screen.getByRole("button", { name: "Load older entries" }).focus()
  await user.keyboard("{Enter}")
  await screen.findByText("core.auth.login/v1")
  await waitFor(() => expect(focusedName()).toBe("Load older entries"))
})
```

In `web/src/modules/firewall/docker-panel.a11y.test.tsx`, change the imports to add `import type { DockerStatus, Exemption } from "./types"`, then append:

```tsx
const dpaNat: Exemption = { origDestination: "203.0.113.5", origPort: 8443, destination: "192.168.50.10", proto: "tcp", port: 443 }
const dpaProposed: Exemption = { origDestination: "203.0.113.7", origPort: 2222, destination: "192.168.50.20", proto: "tcp", port: 22 }

// dpaRender serves the Docker resource as first until an apply, and as second afterwards, which is what the panel
// reads again when the change dialog applies and closes.
function dpaRender(first: DockerStatus, second: DockerStatus) {
  let current = first
  const applied: Change = {
    id: "c1", hostId: "h1", action: "firewall.docker.set-sources", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/docker": () => ({ body: current }),
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.docker.set-sources", version: 1, inputHash: "h", diff: [{ op: "~", text: "sources" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    "POST /api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/apply": () => {
      current = second
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  renderWithProviders(<DockerPanel hostId="h1" />)
}

// dpaApplyAndClose applies the open change dialog titled title, waits until settled holds (the panel has read the
// host again and the control that opened the dialog has changed or gone), and closes the dialog from its footer.
async function dpaApplyAndClose(user: ReturnType<typeof userEvent.setup>, title: string, settled: () => void) {
  const dialog = await screen.findByRole("dialog", { name: title })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  within(dialog).getByRole("button", { name: "Apply" }).focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(settled)
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
}

test("closing the change dialog of a removed exemption leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [dpaNat] }, foreignForwards: [] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const remove = await screen.findByRole("button", { name: "Remove exemption 1" })
  remove.focus()
  await user.keyboard("{Enter}")
  await dpaApplyAndClose(user, "Remove a forward exemption", () => expect(screen.queryByRole("button", { name: "Remove exemption 1" })).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})

test("closing the change dialog of an added proposal leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [dpaProposed] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [dpaProposed] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const add = await screen.findByRole("button", { name: "Add proposed exemption 1" })
  add.focus()
  await user.keyboard("{Enter}")
  await dpaApplyAndClose(user, "Add a forward exemption", () => expect(screen.queryByRole("button", { name: "Add proposed exemption 1" })).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})

test("closing the change dialog of new private sources leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8", "192.168.0.0/16"], forwardExemptions: [] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const box = await screen.findByLabelText("Private source ranges")
  await user.type(box, "{Enter}192.168.0.0/16")
  await user.click(screen.getByRole("button", { name: "Set private sources" }))
  await dpaApplyAndClose(user, "Set the private sources", () =>
    expect(screen.getByRole("button", { name: "Set private sources" }).hasAttribute("disabled")).toBe(true),
  )
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/6 && export TMPDIR=/root/.cache/w10a-tmp/6 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 900 pnpm -C web exec vitest run src/pages/create-token-dialog.test.tsx src/modules/tailnet/keys-page.test.tsx src/pages/audit.a11y.test.tsx src/modules/firewall/docker-panel.a11y.test.tsx
```

Expected FAIL: the two clipboard tests cannot find a textbox named "Token ci" or "Auth key darkpanel-c3" (the input has no label); "loading the last page leaves the keyboard focus…" finds `document.activeElement` equal to `document.body`. These pass today and stay as guards: the "load button has the focus again" test (jsdom keeps focus on a button that becomes disabled; a browser may not) and the three `DockerSources` focus tests, whose `fallbackFocus` code already exists. Quote the output.

Prove the three focus tests catch the regression row 424 names: in `web/src/modules/firewall/docker-sources.tsx` delete the line `fallbackFocus={fallbackFocus}` of the `FirewallChange` element, run only those three tests, and see each fail with the active element being `document.body`. Then restore the file with `git checkout -- web/src/modules/firewall/docker-sources.tsx` and run them green. Quote both outputs.

- [ ] **Step 3: Implement**

1. `web/src/pages/create-token-dialog.tsx`: tie the created token's label to its input as `create-host-dialog.tsx` does. Give the `ClipboardLabel` `htmlFor="token-secret"` and the `ClipboardInput` `id="token-secret"`. The label text, the value and the Copy button stay.
2. `web/src/modules/tailnet/keys-page.tsx`, `CreatedDialog`: the same, with the id `authkey-secret`.
3. `web/src/pages/audit.tsx`, `AuditBody`: keep keyboard focus in the list when the load button goes away.
   - Add `useRef`, `useState` and `useEffect` imports. Declare, above the early returns of `AuditBody` (hooks run in one order), a ref for the button (`ref` prop on `Button`), a ref for a new end-of-list paragraph, and a state `refocus` of type `"more" | "end" | null`, initially `null`.
   - The button's `onClick` becomes an async function: `const result = await audit.fetchNextPage()`, then `setRefocus(result.hasNextPage ? "more" : "end")`. A failed older page resolves with `hasNextPage` still true, so focus returns to the button.
   - An effect keyed on `refocus`, `audit.isFetchingNextPage` and `audit.hasNextPage` does nothing while `refocus` is `null` or a page is loading; otherwise it focuses the ref `refocus` names when that element is mounted, then sets `refocus` back to `null`. When the element is not mounted yet the effect runs again after the next render.
   - Where the list rendered `audit.hasNextPage && <Button …>`, render the button when `audit.hasNextPage` and otherwise a `<p>` with the end ref, `tabIndex={-1}`, the classes `text-sm text-muted-foreground` and the text `No older entries.`
   - Do not change the empty-log branch, the error alert, the table or the page title.
4. `docker-sources.tsx`, `docker-panel.tsx` and `create-host-dialog.tsx` are not edited.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/6 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 900 pnpm -C web exec vitest run src/pages/create-token-dialog.test.tsx src/modules/tailnet/keys-page.test.tsx src/pages/audit.a11y.test.tsx src/pages/audit.test.tsx src/modules/firewall/docker-panel.a11y.test.tsx src/modules/firewall/docker-sources.test.tsx
```

Expected: PASS, every test of the six files, including the existing audit test that expects the load button gone and three rows after the last page.

- [ ] **Step 5: Verify and commit**

```sh
export TMPDIR=/root/.cache/w10a-tmp/6 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 1200 make lint-web test-web
timeout 300 go mod tidy -diff
git status --short
```

Expected: every Vitest file passes (including `src/test/accessibility-doc.test.ts`, which this task leaves alone), eslint prints no warning, `tsc` no error, `go mod tidy -diff` nothing; `git status --short` lists only the files of the **Files:** block. Then `rm -rf /root/.cache/w10a-tmp/6 && go clean -testcache`.

```sh
git add web/src/pages/create-token-dialog.tsx web/src/pages/create-token-dialog.test.tsx web/src/modules/tailnet/keys-page.tsx web/src/modules/tailnet/keys-page.test.tsx web/src/pages/audit.tsx web/src/pages/audit.a11y.test.tsx web/src/modules/firewall/docker-panel.a11y.test.tsx
git commit -m "fix(web): label secrets and keep audit focus"
```

### Task 7: DERP binary update rollback

**Files:**
- Modify: `internal/modules/derp/binary_update.go` (the `const` block, `fail`, `await`, `rollback`, the doc comment of `restore`; new helpers `binaryBound` and `binaryRecord`)
- Modify: `internal/modules/derp/binary_update_recover.go` (`Inverse`)
- Create: `internal/modules/derp/binary_update_w10_test.go`

**Interfaces:**
- Consumes: the existing test fixtures `buNewSetup`, `buDrive`, `buCoded`, `buFrom`, `buOld` (`binary_update_test.go`) and `rcSetup`, `rcLayout`, `rcAdd`, `rcDef`, `rcDrive`, `rcRestored`, `rcPhase`, `rcTo`, `rcNew`, `rcSum`, `rcResource` (`binary_update_recover_test.go`); `core.Env.Clock` (`AfterFunc`) and `core.Units`.
- Produces: unexported identifiers of package `derp` that no other task uses: the constants `binaryRollbackWindow`, `binaryRecordWindow`, `binaryProbeWait` and the helpers `binaryBound`, `binaryRecord`. Test prefix `TestW10Bu` and helper prefix `w10bu` (Contracts, Test prefixes). Task 8 also edits package `derp` but no file of this task.

**Items:** 309, 310, 311

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

Rows and parts. Row 309: the rollback of a failed binary update runs on `context.WithoutCancel(ctx)` with no deadline (`binaryUpdateHandler.fail`), so a hung `Restart`, `Rename` or database call blocks the task forever while it holds the `derp` lock. Row 310, the part `binary_update.go:208-214`: `await` bounds each health probe by the time left in the 60 s window, so a probe context always carries a deadline and `stun.Probe`'s own 3 s limit (which applies only when the context has none) is off; a lost STUN datagram then wastes the whole window instead of one poll. The other parts of row 310 belong to other tasks. Row 311: `Inverse` refuses an update in phase `failed`, so the undo cannot be run again after an operator repaired the cause of a failed rollback; and `restore` also runs for phase `done` (an undo of a finished update), which its comment does not say.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/derp/binary_update_w10_test.go`:

```go
package derp

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// w10buHungUnits lets the first allow restarts through and hangs every later one until its context ends.
type w10buHungUnits struct {
	core.Units
	allow int
	ended chan error
}

func (u *w10buHungUnits) Restart(ctx context.Context, unit string) error {
	if u.allow > 0 {
		u.allow--
		return u.Units.Restart(ctx, unit)
	}
	<-ctx.Done()
	u.ended <- ctx.Err()
	return ctx.Err()
}

// TestW10BuRollbackIsBoundedWhenARestartHangs: the update's health check fails, the rollback's restart never
// answers, and the rollback ends when its window on the host's clock ends, with the update recorded as failed.
func TestW10BuRollbackIsBoundedWhenARestartHangs(t *testing.T) {
	s := buNewSetup(t)
	delete(s.h.tls, "127.0.0.1")
	hung := &w10buHungUnits{Units: s.units, allow: 1, ended: make(chan error, 1)}
	s.env.Units = hung
	start := s.h.clock.Now()
	_, err := s.run(t)
	coded := buCoded(t, err, errcode.DERPVerifyFailed)
	for _, want := range []string{"the rollback to " + buFrom + " failed", "derper needs repair"} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q lacks %q", coded.Detail, want)
		}
	}
	select {
	case got := <-hung.ended:
		if !errors.Is(got, context.Canceled) {
			t.Errorf("the hung restart ended with %v, want the rollback's cancellation", got)
		}
	default:
		t.Fatal("the rollback's restart never ended")
	}
	floor := healthWindow + binaryRollbackWindow
	if waited := s.h.clock.Now().Sub(start); waited < floor || waited > floor+3*binaryUpdatePoll {
		t.Errorf("the task ended after %v, want the %v health window plus the %v rollback window", waited, healthWindow, binaryRollbackWindow)
	}
	if u := s.update(t); u.Phase != "failed" || !strings.Contains(u.Result, "context canceled") {
		t.Errorf("update row %+v, want phase failed with the cancelled restart as the result", u)
	}
	if string(s.h.files[server.BinaryPath]) != string(buOld) {
		t.Errorf("binary %q, want the old one, restored before the restart", s.h.files[server.BinaryPath])
	}
}

// TestW10BuHealthProbesAreBoundedLikeSTUN: every look at the server in the health check carries a deadline of at
// most binaryProbeWait, the limit stun.Probe applies when its context has none.
func TestW10BuHealthProbesAreBoundedLikeSTUN(t *testing.T) {
	s := buNewSetup(t)
	var bounds []time.Duration
	stun := s.d.d.stun
	s.d.d.stun = func(ctx context.Context, a netip.AddrPort) (time.Duration, error) {
		if d, ok := ctx.Deadline(); ok {
			bounds = append(bounds, time.Until(d))
		} else {
			bounds = append(bounds, 0)
		}
		return stun(ctx, a)
	}
	if _, err := s.run(t); err != nil {
		t.Fatal(err)
	}
	if len(bounds) == 0 {
		t.Fatal("the health check never asked STUN")
	}
	for _, b := range bounds {
		if b <= 0 || b > binaryProbeWait {
			t.Errorf("a STUN probe was bounded to %v, want more than 0 and at most %v", b, binaryProbeWait)
		}
	}
}

// TestW10BuInverseRunsAgainAfterAFailedRollback: an update whose rollback failed (phase failed) can be undone once
// the operator has put right what stopped it.
func TestW10BuInverseRunsAgainAfterAFailedRollback(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	def := rcDef(t, d)
	rcLayout(h, "swapped")
	rcAdd(t, h, "c1", "failed", "derper.prev does not hold the previous binary")
	if err := setLocalBinary(ctx, h.db, rcTo, rcSum(rcNew), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var res core.InverseResult
	var err error
	rcDrive(t, h, func() {
		res, err = def.Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: rcResource, State: core.EffectUndoing})
	})
	if err != nil || res != (core.InverseResult{}) {
		t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
	}
	rcRestored(t, h)
	if got := rcPhase(t, h, "c1"); got != "rolled_back" {
		t.Errorf("update phase %q, want rolled_back", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/7 && export TMPDIR=/root/.cache/w10a-tmp/7 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go test -count=1 -run 'TestW10Bu' ./internal/modules/derp/
```

Expected FAIL: the build stops at the first undefined identifier, `undefined: binaryRollbackWindow` (then `binaryProbeWait`). Add the three constants of Step 3 with their values and nothing else, run again, and expect: `TestW10BuRollbackIsBoundedWhenARestartHangs` fails with "the call did not finish" (nothing ever ends the hung restart); `TestW10BuHealthProbesAreBoundedLikeSTUN` fails with a STUN probe bounded to about 1m0s; `TestW10BuInverseRunsAgainAfterAFailedRollback` fails with an error saying the update "is in phase \"failed\", which has nothing to undo". Quote both runs.

- [ ] **Step 3: Implement**

In `internal/modules/derp/binary_update.go`:

1. Add three constants to the existing `const` block, each with a doc comment of one sentence: `binaryRollbackWindow` is 3 minutes, how long the rollback of a failed update may take (one restart, the wait for the unit up to `healthWindow`, file moves and database writes); `binaryRecordWindow` is 30 seconds, how long one write of the rollback's bookkeeping may take; `binaryProbeWait` is 3 seconds, the bound of one look at derper in the health check, the limit `stun.Probe` applies to a context that has no deadline.
2. Add `binaryBound(ctx context.Context, env core.Env, window time.Duration) (context.Context, func())`. It returns a child of ctx that is cancelled when `env.Clock.AfterFunc(window, ...)` fires, and a stop function that stops the timer and cancels the child. It measures the window on `env.Clock`, not with `context.WithTimeout`, as `await` measures its wait, so a test's fake clock drives it. Doc comment: the window is on the host's clock so that tests can advance it.
3. Add `binaryRecord(ctx context.Context, env core.Env, write func(ctx context.Context) error) error`. It runs write on `binaryBound(context.WithoutCancel(ctx), env, binaryRecordWindow)` and stops the bound afterwards. Bookkeeping must still be recorded when ctx has been cancelled or its window has ended, so it detaches from ctx's cancellation and has its own window.
4. `fail`: keep `ctx = context.WithoutCancel(ctx)`, then replace ctx by `binaryBound(ctx, env, binaryRollbackWindow)` and `defer` its stop function. The loading of the update row, the rollback and every call under it now end with the window. The error texts and `DERPVerifyFailed` stay as they are.
5. `rollback`: run each of the three `setUpdatePhase` calls (`rolling_back`, `failed`, `rolled_back`) and the block that lists and skips the intended effect through `binaryRecord`, and leave `h.restore(ctx, env, u)` on ctx. A restore that the window ended then still records phase `failed` with its error as the result, and the effect stays intended for the operator. The returned errors, the 200-rune cut of the result and the order of the steps stay.
6. `await`: the bound of one probe is the smaller of the existing bound (`max(deadline.Sub(env.Clock.Now()), binaryUpdatePoll)`) and `binaryProbeWait`. Update the comment above `await` to say each probe is also bounded to `binaryProbeWait`, so a lost STUN answer costs one probe and not the window. The loop, the poll and the error wrapping stay.
7. Doc comment of `restore`: say that it runs in every phase but `staged`, including `done`, which is the undo of a finished update that `Inverse` runs, and that a binary already back in place is left alone. No behaviour changes.

In `internal/modules/derp/binary_update_recover.go`: `Inverse` runs the rollback for the phases `done`, `rolling_back` and `failed`; `rolled_back` still answers an empty result and every other phase still refuses with the existing definite error. Update `Inverse`'s doc comment: it refuses only an update that never swapped the binary in, and a `failed` update runs the rollback again.

Nothing else changes: `Recover` still reports a `failed` update as needing repair.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/7 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go test -count=1 -run 'TestW10Bu' ./internal/modules/derp/
timeout 900 go test -race -count=1 ./internal/modules/derp/...
```

Expected: `ok` for both, including every existing `TestBu…` and `TestRC…` test (the existing hung-poll test still sees a bound of at most one poll near the end of the window).

- [ ] **Step 5: Verify and commit**

```sh
export TMPDIR=/root/.cache/w10a-tmp/7 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: build and vet print nothing, every test line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output. Then `rm -rf /root/.cache/w10a-tmp/7 && go clean -testcache`.

```sh
git add internal/modules/derp/binary_update.go internal/modules/derp/binary_update_recover.go internal/modules/derp/binary_update_w10_test.go
git commit -m "fix(derp): bound the binary update rollback"
```

### Task 8: DERP regions, composites and server passes

**Files:**
- Modify: `internal/modules/derp/region_edits.go` (`editOmitAllowed`, `editRemove`; new helper `editProbeFailure`)
- Modify: `internal/modules/derp/composite_region.go` (`certRotateComposer.Steps`; new helper `compositeRegionKnown`)
- Modify: `internal/modules/derp/composite_deploy.go` (`serverUpgradeComposer.Steps`)
- Modify: `internal/modules/derp/servers.go` (`checkServers`, the `const` block; new method `readServerStatuses`)
- Create: `internal/modules/derp/regions_w10_test.go`
- Not modified: `internal/modules/derp/composite_remove.go` (see the note on row 303)

**Interfaces:**
- Consumes: the existing test fixtures `reReady`, `reAgent`, `reRun`, `reOmitInput`, `reAck`, `rePin` (`region_edits_test.go`), `cgSteps`, `cgHost` (`composite_region_test.go`), `srWorld`, `srServe`, `srDeps`, `srPass`, `srPut`, `srLoad`, `srIP`, `srPin` (`servers_test.go`); `coretest.FakeAgent`, `coretest.MapHosts`, `coretest.FakeClock` (`Pending`, `Advance`); `store.DB.Writer`; `serversRegionUnknown`, `probeAgentNoAnswer`.
- Produces: unexported identifiers of package `derp` that no other task uses: the constants `serversReadWait` and `serversReadParallel`, the method `dp.readServerStatuses` and the helpers `editProbeFailure` and `compositeRegionKnown`. Test prefix `TestW10Rg` and helper prefix `w10rg` (Contracts, Test prefixes). Tasks 7, 9 and 23 also edit package `derp`, in other files.

**Items:** 310, 312, 313, 314, 315

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 2 = 6

Rows and parts. Row 310, the part `servers.go:173-184`: a `derp.servers` pass reads the hosts' DERP status one after another with no deadline of its own, so one silent agent stalls the pass. Row 312, the part `region_edits.go:280-286`: `editOmitAllowed` skips a region whose probe errored and then asks for the acknowledgement without saying why. Row 313: `derp.server.upgrade` and `derp.cert.rotate` plan from the stored `RegionID`, which a pass whose policy read failed leaves in place (the record then carries `serversRegionUnknown`), so a published region can look unpublished and the fallback check is skipped. Row 314, the `region_edits.go` items: `editRemove` reads a non-list `grants` member as empty while `editSetEdits` refuses it. Row 315: a pass runs `deleteServerRecord` for every host without a server even when it has no row, and a context cancelled during the region lookup still reaches the writes.

Row 303 (the warning that a visibility grant stays in the policy is lost between the edits step and the composite's output) is not in this task: `core.Composer.Output` receives no read step's output (`internal/core/lifecycle/composite_run.go` skips `ClassRead` steps and `internal/core` is frozen), and `RegionChanged` and `ServerRemoved` have no warnings member (a wire change). The session settles it.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/derp/regions_w10_test.go`:

```go
package derp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// w10rgDrive runs call and advances the host's clock by one second whenever a timer waits on it.
func w10rgDrive(t *testing.T, h *dpHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(time.Second)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass did not finish")
		}
	}
}

// w10rgSeedRegion records the server on cgHost with its region and the record's error text.
func w10rgSeedRegion(t *testing.T, h *dpHost, regionID int, recordError string) {
	t.Helper()
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: cgHost, Mode: "ip", HostName: "203.0.113.9", RegionID: regionID, RegionCode: "dp-eu",
		DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now(), Error: recordError,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestW10RgOmitRefusalNamesTheRegionsItCouldNotProbe: when no other region is known to be ready because a probe
// failed, the refusal that asks for the acknowledgement says which region and why.
func TestW10RgOmitRefusalNamesTheRegionsItCouldNotProbe(t *testing.T) {
	const base = `omitting the default regions needs the acknowledgement "` + reAck + `"`
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"an agent that did not answer", errors.New("simulated agent failure"), base + "; the probes of other regions failed: region 901: its server could not be probed"},
		{"a refusal with a detail", errcode.New(errcode.HostOffline, "host h3 is offline"), base + "; the probes of other regions failed: region 901: host h3 is offline"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := reReady(t)
			h.addHost("h3", "charlie", false)
			reAgent(h, "h3").Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return nil, tc.err }
			err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
				HostID: "h3", Mode: "ip", HostName: "203.0.113.20", RegionID: 901, RegionCode: "us-derp", DerperVersion: "v1.104.1",
				CertName: rePin, UnitActive: true, CheckedAt: h.clock.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			h.policy.Text = `{"derpMap": {"Regions": {"901": {"RegionID": 901, "RegionCode": "us-derp", "RegionName": "US",
				"Nodes": [{"Name": "901a", "RegionID": 901, "HostName": "203.0.113.20"}]}}}}`
			_, err = reRun(t, h, reOmitInput(""))
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.InvalidInput || coded.Detail != tc.want ||
				len(coded.Fields) != 1 || coded.Fields[0].Path != "/acknowledge" || coded.Fields[0].Message != tc.want {
				t.Errorf("err = %#v, want invalid input at /acknowledge with the detail %q", err, tc.want)
			}
		})
	}
}

// TestW10RgRemoveRefusesAGrantsMemberThatIsNotAList: the remove op refuses what the set op refuses, instead of
// reading a grants member that is not a list as an empty one.
func TestW10RgRemoveRefusesAGrantsMemberThatIsNotAList(t *testing.T) {
	h := reReady(t)
	h.policy.Text = `{"derpMap": {"Regions": {"900": {"RegionID": 900, "RegionCode": "eu-derp", "RegionName": "EU",
		"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "203.0.113.10"}]}}}, "grants": {"src": ["*"]}}`
	_, err := reRun(t, h, RegionEditsInput{Op: "remove", RegionID: 900})
	if err == nil || !strings.Contains(err.Error(), "the policy's grants member is not a list") {
		t.Errorf("remove over a grants member that is an object: err = %v, want the error that it is not a list", err)
	}
}

// TestW10RgUpgradeAndRotateRefuseARegionTheLastCheckCouldNotRead: a record whose last pass could not read the
// policy keeps a region that may be out of date, so neither composite plans from it.
func TestW10RgUpgradeAndRotateRefuseARegionTheLastCheckCouldNotRead(t *testing.T) {
	const detail = "the controller could not read the tailnet policy at its last check, so whether the server on " + cgHost +
		" publishes a region is not known; try again after the next check"
	tests := []struct {
		name string
		def  func(dp) core.ActionDef
		in   any
	}{
		{"upgrade", dp.serverUpgrade, UpgradeInput{HostID: cgHost}},
		{"rotate", dp.certRotate, CertRotateInput{HostID: cgHost}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			w10rgSeedRegion(t, h, 905, serversRegionUnknown)
			_, err := cgSteps(t, h, tc.def(h.dp()), tc.in)
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.DERPNotReady || coded.Detail != detail {
				t.Errorf("err = %#v, want derp.not_ready with the detail %q", err, detail)
			}
			w10rgSeedRegion(t, h, 905, "")
			if _, err := cgSteps(t, h, tc.def(h.dp()), tc.in); err != nil {
				t.Errorf("with the region read at the last check: %v, want the steps", err)
			}
		})
	}
}

// TestW10RgPassWritesNothingForAHostWithoutAServer: with the controller database's one writer held by someone
// else, a pass over a host that has no server and no record has nothing to write and so does not wait for it.
func TestW10RgPassWritesNothingForAHostWithoutAServer(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h3", "charlie", false)
	srServe(h, "h3", ServerStatus{})
	conn, err := h.ctlDB.Writer().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	held := true
	release := func() {
		if held {
			held = false
			_ = conn.Close()
		}
	}
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := h.dp().checkServers(ctx, srDeps(h), h.clock.Now()); err != nil {
		t.Errorf("a pass over a host with no server and no record: %v, want no write and no error", err)
	}
	release()
	srPut(t, h, serverRecord{HostID: "h3", Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now()})
	srPass(t, h)
	if _, ok := srLoad(t, h, "h3"); ok {
		t.Error("the record of a host that lost its server is still stored")
	}
}

// TestW10RgCancelledRegionLookupEndsThePassBeforeItsWrites: a pass cancelled while it looks the regions up
// answers the context's own error and stores nothing.
func TestW10RgCancelledRegionLookupEndsThePassBeforeItsWrites(t *testing.T) {
	h := srWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := h.dp()
	connected := d.d.connected
	d.d.connected = func(ctx context.Context, db *store.DB) (bool, error) {
		cancel()
		return connected(ctx, db)
	}
	err := d.checkServers(ctx, srDeps(h), h.clock.Now())
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
		t.Errorf("a pass cancelled during its region lookup returned %v, want the context's own error", err)
	}
	for _, id := range []string{"h1", "h2"} {
		if _, ok := srLoad(t, h, id); ok {
			t.Errorf("the pass stored a record for %s after it was cancelled", id)
		}
	}
}

// TestW10RgPassReadsHostsTogetherAndBoundsEachRead: the read of one silent agent does not hold back the other
// hosts' reads, ends at its own bound, and is stored as a host that could not be read.
func TestW10RgPassReadsHostsTogetherAndBoundsEachRead(t *testing.T) {
	h := srWorld(t)
	srPut(t, h, serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", CertName: srPin, UnitActive: true, CheckedAt: h.clock.Now()})
	answers := map[string]ServerStatus{
		"h2": {Installed: true, Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1", UnitActive: true},
		"h3": {},
	}
	var mu sync.Mutex
	started := map[string]time.Time{}
	hosts := coretest.MapHosts{}
	for _, id := range []string{"h1", "h2", "h3"} {
		hosts[id] = &coretest.FakeAgent{ReadFunc: func(ctx context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			started[id] = h.clock.Now()
			mu.Unlock()
			if id == "h1" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return json.Marshal(answers[id])
		}}
	}
	deps := srDeps(h)
	deps.Hosts = hosts
	begin := h.clock.Now()
	var err error
	w10rgDrive(t, h, func() { err = h.dp().checkServers(context.Background(), deps, h.clock.Now()) })
	if err != nil {
		t.Fatalf("checkServers: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	limit := begin.Add(serversReadWait)
	for _, id := range []string{"h2", "h3"} {
		if at, ok := started[id]; !ok || !at.Before(limit) {
			t.Errorf("the read of %s started at %v, want it started before the read of h1 ended at %v", id, at, limit)
		}
	}
	if r, ok := srLoad(t, h, "h1"); !ok || r.Error != probeAgentNoAnswer {
		t.Errorf("record of h1 = %+v, %v; want its error to be %q", r, ok, probeAgentNoAnswer)
	}
	if _, ok := srLoad(t, h, "h2"); !ok {
		t.Error("no record of h2, whose read answered at once")
	}
	if _, ok := srLoad(t, h, "h3"); ok {
		t.Error("a record of h3, which has no server")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/8 && export TMPDIR=/root/.cache/w10a-tmp/8 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go test -count=1 -run 'TestW10Rg' ./internal/modules/derp/
```

Expected FAIL: the build stops at `undefined: serversReadWait`. Add the two constants of Step 3 with their values and nothing else, run again, and expect: `TestW10RgOmitRefusalNamesTheRegionsItCouldNotProbe` fails with the plain acknowledgement detail; `TestW10RgRemoveRefusesAGrantsMemberThatIsNotAList` fails with `err = <nil>`; `TestW10RgUpgradeAndRotateRefuseARegionTheLastCheckCouldNotRead` fails for both rows with `err = nil`; `TestW10RgPassWritesNothingForAHostWithoutAServer` fails with "store: begin: context deadline exceeded" inside the pass's error; `TestW10RgCancelledRegionLookupEndsThePassBeforeItsWrites` fails because the error is the store's, not "context canceled"; `TestW10RgPassReadsHostsTogetherAndBoundsEachRead` fails with "the pass did not finish" after 5 s. Quote both runs.

- [ ] **Step 3: Implement**

`internal/modules/derp/servers.go`:

1. Add to the `const` block, with a doc comment each: `serversReadWait` is 15 seconds, the longest one host's status read may take in a pass; `serversReadParallel` is 8, the most hosts a pass reads at once.
2. Add the method `func (d dp) readServerStatuses(ctx context.Context, env core.Env, hosts []probeHost) (map[string]ServerStatus, map[string]string, error)`. It reads every host's status with `readStatus(rctx, env, h.ID, nil)`, at most `serversReadParallel` at a time (a `sync.WaitGroup` and a buffered channel as the semaphore; no new dependency). Each read gets its own context: a child of ctx that is cancelled by `env.Clock.AfterFunc(serversReadWait, cancel)`, whose timer is stopped and whose child is cancelled when the read returns. The bound is on `env.Clock`, not `context.WithTimeout`, so the pass's fake clock drives it in tests. Results are stored by host index and merged in host order after every read has returned. If the pass's own ctx has an error after the reads, return `ctx.Err()` before merging, as the loop did; otherwise a failed read is `unreadable[h.ID] = serversReadFailure(err)` (a read ended by its own bound is uncoded, so it becomes `probeAgentNoAnswer`) and a read that succeeded is `statuses[h.ID]`. It returns the two maps. The doc comment says each read has its own bound so one silent agent costs `serversReadWait` and not the pass.
3. `checkServers`: replace the sequential `for _, h := range hosts { readStatus … }` loop by one call to `d.readServerStatuses`, return its error, and compute `anyInstalled` from the statuses it returned. Everything after the reads keeps its order.
4. After `regions, regionsKnown = d.serversRegions(ctx, env)`, return `ctx.Err()` when it is not nil, so a pass cancelled during the lookup stops before any write. The comment above `checkServers` already promises that every read comes before the first write.
5. In the loop that writes, when `!st.Installed`: call `deleteServerRecord` only when the host has a stored row (`had`), then `continue`. A host without a server and without a row is skipped with no write.

`internal/modules/derp/region_edits.go`:

6. Add `editProbeFailure(err error) string`: the detail of a coded error that is not `errcode.Internal` and has a detail, else the text `its server could not be probed`. A doc comment says an uncoded error's text may carry transport internals, as `serversReadFailure` says.
7. `editOmitAllowed`: when `d.probeServer` returns an error and ctx has none, record `fmt.Sprintf("region %d: %s", id, editProbeFailure(err))` and go on to the next region, as it goes on today. When the loop ends without a ready region and the acknowledgement does not match, the refusal's message (the `Detail` and the one field's `Message`, still `/acknowledge`) is the existing text `omitting the default regions needs the acknowledgement "<ack>"` and, when any probe failed, `; the probes of other regions failed: ` followed by the recorded entries joined by `; `. A matching acknowledgement still admits the region.
8. `editRemove`: where it reads `doc["grants"]`, treat an absent or `null` member as empty and a list as today, and return the same error `editSetEdits` returns (`errors.New("derp: the policy's grants member is not a list")`) for any other type. The check stays after the early returns for an absent region and for other custom regions, so those answers do not change.

`internal/modules/derp/composite_region.go` and `composite_deploy.go`:

9. Add `compositeRegionKnown(rec serverRecord) error` in `composite_region.go`, with a doc comment: it returns nil unless `rec.Error == serversRegionUnknown`, and then `errcode.New(errcode.DERPNotReady, ...)` with the exact detail `the controller could not read the tailnet policy at its last check, so whether the server on <rec.HostID> publishes a region is not known; try again after the next check`.
10. `certRotateComposer.Steps` and `serverUpgradeComposer.Steps`: right after the check that the server is known and before `rec.RegionID` is read, `return nil, err` when `compositeRegionKnown(rec)` returns an error.

Leave `composite_remove.go` and `serverRemoveComposer` as they are.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/8 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go test -count=1 -run 'TestW10Rg' ./internal/modules/derp/
timeout 900 go test -race -count=1 ./internal/modules/derp/...
```

Expected: `ok` for both, including every existing `TestRe…`, `TestCG…` and `TestSr…` test.

- [ ] **Step 5: Verify and commit**

```sh
export TMPDIR=/root/.cache/w10a-tmp/8 GOTOOLCHAIN=local
cd /root/repositories/darkpanel
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: build and vet print nothing, every test line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing, and `git status --short schemas docs/api` prints nothing (no wire type changed). Record the real output. Then `rm -rf /root/.cache/w10a-tmp/8 && go clean -testcache`.

```sh
git add internal/modules/derp/region_edits.go internal/modules/derp/composite_region.go internal/modules/derp/composite_deploy.go internal/modules/derp/servers.go internal/modules/derp/regions_w10_test.go
git commit -m "fix(derp): bound passes and refuse stale regions"
```

### Task 9: DERP status reads and probes

**Files:**
- Modify: `internal/modules/derp/server_status.go`
- Modify: `internal/modules/derp/server_probe.go`
- Modify: `internal/modules/derp/fallback_check.go`
- Modify: `internal/modules/derp/cert_renew.go`
- Modify: `internal/modules/derp/server/server.go`
- Test: `internal/modules/derp/server_status_test.go`
- Test: `internal/modules/derp/server_probe_test.go`
- Test: `internal/modules/derp/fallback_check_test.go`
- Test: `internal/modules/derp/cert_renew_test.go`
- Test: `internal/modules/derp/server/server_test.go`

**Interfaces:**
- Consumes: nothing from another task. The tests reuse the fixtures already in the package's test files (`newDPHost`, `ssRead`, `pbWorld`, `fbSetup`, `crInstalled`).
- Produces: nothing another task consumes; every change is inside the two packages. No wire type changes, so `schemas/**` and `docs/api/**` stay as they are. New names, all unexported: the constants `serverProbeReadWait` (30 s) and `serverProbeParallel` (8) and the functions `serverProbeEach` and `serverProbeBounded` in `server_probe.go`; `fallbackWhy` in `fallback_check.go`. Changed signatures: `probeSelves` also returns the hosts whose status could not be read, `certRenewNames` returns `(crt, key string, err error)`, and `serverStatusHandler.certificate` returns `(pin string, notAfter *time.Time, err error)`. `server.Flags` and `server.UnitFile` return nil for a `Config.Mode` other than `server.ModeLetsEncrypt` and `server.ModeIP`.

**Items:** 310, 312, 314

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

This task takes only its own parts of three split rows: the reads without a deadline of `server_probe.go` and `server_status.go` (310), the silent fallbacks of `server_status.go`, `server_probe.go` and `fallback_check.go` (312), and the `server/server.go` Flags and `cert_renew.go` items (314). It does not touch `binary_update.go`, `servers.go`, `region_edits.go` or `server_uninstall.go`; other tasks edit them in the same package.

Decisions this task embodies, because the register rows leave them open:
- `derp.server.status` has no field that could say "unknown", so a status it cannot fill honestly is an error, not a fallback. An ip-mode certificate that cannot be read or parsed is `derp.not_ready` (detail `derper's certificate for <host> cannot be read`). A tailscaled that is reachable but fails or times out is `tailscale.unavailable`, as `visibility` already answers. Only a tailscaled whose socket cannot be reached at all (`tslocal.ErrUnavailable`) still leaves the node off the tailnet without an error, so a host without tailscale keeps answering. The `derp.servers` pass already stores a failed status read as the host's error text, using the coded detail.
- The probe reads agents in parallel, each read bounded by the controller's clock, so one silent agent costs one bound, not one bound per agent.
- A host whose `tailscale.status` cannot be read is named in the probe's `warnings`, because it drops out of the node list silently otherwise.
- The fallback check keeps skipping a region whose probe fails, because one unreachable host only rules out its own region, but the refusal now says why each region was ruled out.

- [ ] **Step 1: Write the failing tests**

Edit the existing tests that pin the behaviour being replaced, then add the new tests. Every new identifier in a test file starts with `w10sp` or `TestW10Sp`. Run `gofmt -w` on each edited file.

1. `internal/modules/derp/server_status_test.go`
   - Add `"fmt"` and `"sync"` to the imports.
   - In `TestSsInstalledIPMode`, delete the rows `no certificate file falls back to the stored values` and `an unreadable certificate file falls back to the stored values`, and change its doc comment to `// TestSsInstalledIPMode: the certificate is read from derper's file.`
   - In `TestSsOffTailnet`, replace the row `{"tailscaled not answering", "Running", errSsBoom}` with `{"tailscaled not running", "Running", fmt.Errorf("%w: no such socket", tslocal.ErrUnavailable)}` and change the doc comment's "tailscaled not answering" to "tailscaled not running".
   - Append:

```go
// w10spLocal wraps the fake local API and records the deadline of the context of every call, in call order.
type w10spLocal struct {
	localAPI
	mu        sync.Mutex
	deadlines []time.Time
}

func (l *w10spLocal) note(ctx context.Context) {
	dl, _ := ctx.Deadline()
	l.mu.Lock()
	l.deadlines = append(l.deadlines, dl)
	l.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
}

func (l *w10spLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	l.note(ctx)
	return l.localAPI.Status(ctx)
}

func (l *w10spLocal) WhoIs(ctx context.Context, addr netip.Addr) (*tslocal.WhoIs, error) {
	l.note(ctx)
	return l.localAPI.WhoIs(ctx, addr)
}

// TestW10SpStatusGivesEachLocalCallItsOwnDeadline: the Status call and each WhoIs call run under a deadline of
// their own, so a slow answer does not use up the time of the lookups after it.
func TestW10SpStatusGivesEachLocalCallItsOwnDeadline(t *testing.T) {
	h := newDPHost(t)
	ssTailnet(h, "Running", []string{"tag:derp"}, "100.64.0.10")
	rec := &w10spLocal{localAPI: thLocal{h.local}}
	d := h.dp()
	d.d.local = func() localAPI { return rec }
	if _, _, err := ssRead(t, d, h.env(""), `{"whois":["100.64.0.20","100.64.0.21","100.64.0.22"]}`); err != nil {
		t.Fatal(err)
	}
	if len(rec.deadlines) != 4 {
		t.Fatalf("%d local API calls, want one Status and three WhoIs", len(rec.deadlines))
	}
	for i, dl := range rec.deadlines {
		if dl.IsZero() {
			t.Errorf("call %d ran without a deadline", i)
		}
		if i > 0 && dl.Sub(rec.deadlines[i-1]) < time.Millisecond {
			t.Errorf("call %d shares the deadline of call %d (%v and %v), want a deadline of its own", i, i-1, dl, rec.deadlines[i-1])
		}
	}
}

// TestW10SpStatusRefusesAnIPServerWhoseCertificateCannotBeRead: the stored pin is not shown as the current
// certificate when derper's file is absent or is not a certificate.
func TestW10SpStatusRefusesAnIPServerWhoseCertificateCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		file []byte
	}{
		{"no certificate file", nil},
		{"a file that is not a certificate", []byte("not a certificate")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssMaps(h)
			row := ssRow(server.ModeIP, ssIP)
			row.CertName, row.CertNotAfter = ssStoredPin, ssStoredAfter
			ssInstall(t, h, row)
			h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
			if tc.file != nil {
				h.files[server.CertDir+"/"+ssIP+".crt"] = tc.file
			}
			_, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			coded := errcode.From(err)
			wantDetail := "derper's certificate for " + ssIP + " cannot be read"
			if err == nil || coded.Code != errcode.DERPNotReady || coded.Detail != wantDetail {
				t.Errorf("err = %v, want derp.not_ready saying %q", err, wantDetail)
			}
		})
	}
}

// TestW10SpStatusRefusesASilentTailscaled: a tailscaled that is running but fails or does not answer is an error,
// not a node that is off the tailnet; only a socket that cannot be reached at all is no error.
func TestW10SpStatusRefusesASilentTailscaled(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"the call times out", context.DeadlineExceeded},
		{"tailscaled answers with an error", &tslocal.APIError{StatusCode: 500, Message: "boom"}},
		{"the answer cannot be read", errSsBoom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			ssTailnet(h, "Running", []string{"tag:derp"}, "100.64.0.10")
			h.local.Errs = map[string]error{"Status": tc.err}
			_, _, err := ssRead(t, h.dp(), h.env(""), `{}`)
			if !errors.Is(err, tc.err) || errcode.From(err).Code != errcode.TailscaleUnavailable {
				t.Errorf("err = %v, want tailscale.unavailable wrapping %v", err, tc.err)
			}
		})
	}
}
```

2. `internal/modules/derp/server_probe_test.go`
   - Add `"sync/atomic"` and `"github.com/darkraise/darkpanel/internal/core/coretest"` to the imports.
   - In `pbWorld`, directly after the line `h.tailnet = true`, add the loop below, and append ` and every host's node logged out` to the doc comment (before the final period). Without it every host of the existing tests would now be reported as unreadable.

```go
	for _, id := range []string{"h1", "h2", "h3"} {
		pbAgent(h, id).Resources["tailscale.status"] = tailscale.Status{BackendState: "NeedsLogin"}
	}
```

   - Append:

```go
// w10spBarrier is how long a faked agent read waits for the other reads before it gives up.
const w10spBarrier = 2 * time.Second

// TestW10SpProbeReadsAgentsConcurrently: the agents' probes are in flight at the same time, and their sources
// still come back in input order.
func TestW10SpProbeReadsAgentsConcurrently(t *testing.T) {
	h := pbWorld(t)
	h.addHost("h4", "delta", false)
	pbAgent(h, "h4").Resources["tailscale.status"] = tailscale.Status{BackendState: "NeedsLogin"}
	var arrived atomic.Int32
	all := make(chan struct{})
	gate := func(json.RawMessage) (any, error) {
		if arrived.Add(1) == 3 {
			close(all)
		}
		select {
		case <-all:
			return NodeProbe{TLS: ProbeResult{OK: true, LatencyMs: 1}, STUN: ProbeResult{OK: true, LatencyMs: 1}}, nil
		case <-time.After(w10spBarrier):
			return nil, errors.New("the other agents were not read at the same time")
		}
	}
	for _, id := range []string{"h2", "h3", "h4"} {
		pbAgent(h, id).Reads["derp.node.probe/v1"] = gate
	}
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"controller=controller", "h2=bravo", "h3=charlie", "h4=delta"}; !slices.Equal(pbNames(got), want) {
		t.Fatalf("sources %v, want %v", pbNames(got), want)
	}
	for _, s := range got.Sources {
		if s.Error != "" {
			t.Errorf("source %s failed with %q, want every agent read to run while the others waited", s.Source, s.Error)
		}
	}
}

// TestW10SpProbeBoundsEveryAgentRead: an agent that never answers fails its own source once 30 s have passed on
// the controller's clock, whatever it was asked, and the probe still answers from the others.
func TestW10SpProbeBoundsEveryAgentRead(t *testing.T) {
	h := pbWorld(t)
	env := h.ctlEnv("")
	hung := &coretest.FakeAgent{
		ReadFunc: func(ctx context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		ResourceFunc: func(ctx context.Context, _ core.Principal, _ string) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	env.Agents.(coretest.MapHosts)["h3"] = hung
	done := make(chan struct{})
	var got ServerProbe
	var err error
	go func() {
		defer close(done)
		got, err = h.dp().probeServer(context.Background(), env, ServerProbeInput{HostID: "h1"})
	}()
	timeout := time.After(10 * time.Second)
wait:
	for {
		select {
		case <-done:
			break wait
		case <-timeout:
			t.Fatal("the probe of a silent agent did not end")
		default:
			if h.clock.Pending() > 0 {
				h.clock.Advance(30 * time.Second)
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"controller=controller", "h2=bravo", "h3=charlie"}; !slices.Equal(pbNames(got), want) {
		t.Fatalf("sources %v, want %v", pbNames(got), want)
	}
	if charlie := got.Sources[2]; charlie.Error != "the agent did not answer" || charlie.TLS.OK || charlie.STUN.OK {
		t.Errorf("charlie source %+v, want a failed source saying %q", charlie, "the agent did not answer")
	}
	if bravo := got.Sources[1]; bravo.Error != "" || !bravo.TLS.OK || !got.Ready {
		t.Errorf("bravo source %+v, ready %v; want it to pass and back the region", bravo, got.Ready)
	}
}

// TestW10SpProbeWarnsAboutAHostWhoseTailscaleStatusCannotBeRead: a host whose status cannot be read is named in
// the warnings; a host whose node is not on the tailnet is not.
func TestW10SpProbeWarnsAboutAHostWhoseTailscaleStatusCannotBeRead(t *testing.T) {
	h := pbWorld(t)
	h.tailnet = false
	pbNodeWorld(h)
	delete(pbAgent(h, "h3").Resources, "tailscale.status")
	pbAgent(h, "h2").Resources["tailscale.status"] = tailscale.Status{BackendState: "NeedsLogin"}
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	const want = "the tailscale status of host charlie could not be read, so its node use is not reported"
	if !slices.Contains(got.Warnings, want) {
		t.Errorf("warnings %q, want %q", got.Warnings, want)
	}
	for _, w := range got.Warnings {
		if strings.Contains(w, "bravo") || strings.Contains(w, "alpha") {
			t.Errorf("warning %q names a host whose status was read", w)
		}
	}
}
```

3. `internal/modules/derp/fallback_check_test.go`
   - Add `"strings"` to the imports.
   - In `TestFbPaths`, change the condition `coded.Detail != wantDetail` to `!strings.HasPrefix(coded.Detail, wantDetail)`; the detail now continues after the sentence when regions were tried.
   - Append:

```go
// TestW10SpFallbackNamesWhyEachRegionWasRuledOut: the refusal says, for every other region it tried, why that
// region does not count: the failing check of its probe, or that its probe could not be read.
func TestW10SpFallbackNamesWhyEachRegionWasRuledOut(t *testing.T) {
	down := fbRegion{id: 901, host: "h2", ip: "203.0.113.20", row: true}
	failing := fbRegion{id: 902, host: "h3", ip: "203.0.113.30", row: true, statusErr: true}
	h := fbSetup(t, true, false, true, []fbRegion{down, failing})
	_, err := fbRun(t, h, FallbackCheckInput{HostID: fbHost, RegionID: 900})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.DERPNoFallback || !core.IsDefinite(err) {
		t.Fatalf("err = %v, want a definite %s refusal", err, errcode.DERPNoFallback)
	}
	const base = "the DERP host's link depends on region 900 and no other region is reachable; add a second region or clear omitDefaultRegions"
	if !strings.HasPrefix(coded.Detail, base+": ") {
		t.Errorf("detail %q, want it to start with %q", coded.Detail, base+": ")
	}
	for _, want := range []string{"region 901: controller: tls: ", "region 902: the probe could not be read"} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q, want it to contain %q", coded.Detail, want)
		}
	}
	if strings.Contains(coded.Detail, "the agent does not answer") {
		t.Errorf("detail %q repeats the transport error of an uncoded failure", coded.Detail)
	}
}
```

4. `internal/modules/derp/cert_renew_test.go`
   - Add `"errors"` to the imports.
   - Append:

```go
// w10spUnits is a unit client whose State fails.
type w10spUnits struct {
	core.Units
	err error
}

func (u w10spUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{}, u.err
}

// TestW10SpCertRenewRefusesAnAddressDerperNamesNoFileFor: an address none of whose characters derper keeps in a file
// name has no certificate file, which is an error, not an absent certificate.
func TestW10SpCertRenewRefusesAnAddressDerperNamesNoFileFor(t *testing.T) {
	h := crInstalled(t)
	_, err := h.dp().certRenew().Handler.Observe(context.Background(), h.env(""), core.Effect{Resource: "derp.cert:::", Kind: core.EffectModify})
	if err == nil || !strings.HasPrefix(err.Error(), "derp:") || !strings.Contains(err.Error(), `"::"`) {
		t.Errorf("observe of derp.cert::: = %v, want an error naming the address that starts with %q", err, "derp:")
	}
}

// TestW10SpCertRenewVerifyErrorsStartWithThePackageName: every way Verify can fail is an error that starts with
// "derp:".
func TestW10SpCertRenewVerifyErrorsStartWithThePackageName(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(t *testing.T) (*dpHost, core.Env)
	}{
		{"no server is recorded", func(t *testing.T) (*dpHost, core.Env) {
			h := newDPHost(t)
			return h, h.env("c1")
		}},
		{"the recorded server is not in ip mode", func(t *testing.T) (*dpHost, core.Env) {
			h := newDPHost(t)
			at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
			err := insertLocalServer(ctx, h.db, localServer{
				ChangeID: "c0", Mode: server.ModeLetsEncrypt, HostName: "derp.example.com", DerperVersion: "v1.104.1",
				BinarySHA256: strings.Repeat("a", 64), InstalledAt: at, UpdatedAt: at,
			})
			if err != nil {
				t.Fatal(err)
			}
			return h, h.env("c1")
		}},
		{"the unit state cannot be read", func(t *testing.T) (*dpHost, core.Env) {
			h := crInstalled(t)
			env := h.env("c1")
			env.Units = w10spUnits{Units: h.units, err: errors.New("bus closed")}
			return h.dpHost, env
		}},
		{"the unit is not active", func(t *testing.T) (*dpHost, core.Env) {
			h := crInstalled(t)
			h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}
			return h.dpHost, h.env("c1")
		}},
		{"the certificate cannot be read", func(t *testing.T) (*dpHost, core.Env) {
			h := crInstalled(t)
			delete(h.files, crCrt)
			return h.dpHost, h.env("c1")
		}},
		{"the stored pin differs", func(t *testing.T) (*dpHost, core.Env) {
			h := crInstalled(t)
			if err := setLocalCert(ctx, h.db, h.newPin, crNewAfter, h.clock.Now()); err != nil {
				t.Fatal(err)
			}
			return h.dpHost, h.env("c1")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, env := tc.setup(t)
			err := h.dp().certRenew().Handler.Verify(ctx, env, json.RawMessage(`{}`), json.RawMessage(`{}`))
			if err == nil || !strings.HasPrefix(err.Error(), "derp:") {
				t.Errorf("Verify = %v, want an error that starts with %q", err, "derp:")
			}
		})
	}
}
```

5. `internal/modules/derp/server/server_test.go`: append:

```go
// TestW10SpFlagsAndUnitFileRefuseAnUnknownMode: a mode that is neither letsencrypt nor ip gets no flags and no
// unit, instead of the letsencrypt flags.
func TestW10SpFlagsAndUnitFileRefuseAnUnknownMode(t *testing.T) {
	for _, mode := range []string{"", "manual", "IP"} {
		c := Config{Mode: mode, HostName: "203.0.113.7", VerifyClients: true}
		if got := Flags(c); got != nil {
			t.Errorf("Flags with mode %q = %q, want nil", mode, got)
		}
		if got := UnitFile(c); len(got) != 0 {
			t.Errorf("UnitFile with mode %q = %q, want nothing", mode, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/9 && export TMPDIR=/root/.cache/w10a-tmp/9 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Sp' ./internal/modules/derp/ ./internal/modules/derp/server/
```

Expected: FAIL, every new test on a failed assertion, none on a build error:
- `TestW10SpStatusGivesEachLocalCallItsOwnDeadline`: `call 1 shares the deadline of call 0`.
- `TestW10SpStatusRefusesAnIPServerWhoseCertificateCannotBeRead` and `TestW10SpStatusRefusesASilentTailscaled`: `err = <nil>, want ...`.
- `TestW10SpProbeReadsAgentsConcurrently`: `source h2 failed with "the agent did not answer"` after a few seconds, because the first read waits out its barrier alone.
- `TestW10SpProbeBoundsEveryAgentRead`: `the probe of a silent agent did not end` after 10 s.
- `TestW10SpProbeWarnsAboutAHostWhoseTailscaleStatusCannotBeRead`: `warnings [], want "the tailscale status of host charlie ..."`.
- `TestW10SpFallbackNamesWhyEachRegionWasRuledOut`: the detail does not continue after the sentence.
- `TestW10SpCertRenewRefusesAnAddressDerperNamesNoFileFor`: `observe of derp.cert::: = <nil>`.
- `TestW10SpCertRenewVerifyErrorsStartWithThePackageName/the_unit_state_cannot_be_read`: `Verify = bus closed`; the other five rows already pass and guard the prefix.
- `TestW10SpFlagsAndUnitFileRefuseAnUnknownMode`: `Flags with mode "" = [...]`, want nil.

- [ ] **Step 3: Implement**

`internal/modules/derp/server/server.go`
1. `Flags`: the `-certmode` value is `letsencrypt` only for `ModeLetsEncrypt` and `manual` only for `ModeIP`; any other `Mode` returns nil. Say so in its doc comment.
2. `UnitFile`: return nil when `Flags(c)` is nil, so no unit with a half-built `ExecStart` is written; say so in its doc comment. `server_install.go` is not changed: its `precheck` refuses any other mode with `CheckConfig` before `UnitFile` runs.

`internal/modules/derp/server_status.go`
1. `Read`: remove the one `context.WithTimeout(ctx, availableWait)` shared by the Status call and every WhoIs call. `readTailnet(ctx, st)` and `visibility(ctx, addr)` take only the caller's `ctx` and each create their own `context.WithTimeout(ctx, availableWait)`, cancelled when the call returns. `availableWait` stays the bound of one call.
2. `readTailnet`, when `Status` fails: if the caller's `ctx.Err()` is set, return it (as now); else if `errors.Is(err, tslocal.ErrUnavailable)`, return nil, leaving `OnTailnet` false and the lists empty; else return `errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")`, the same error `visibility` returns. The rest of the function is unchanged.
3. `certificate`: new signature `(hostName, crt string) (pin string, notAfter *time.Time, err error)`. It reads `server.CertDir + "/" + crt` with `h.d.d.readFile` and parses it with `server.ParseCert`; the stored `row.CertName` and `row.CertNotAfter` are no longer a fallback. A read or parse failure returns `errcode.Wrap(errcode.DERPNotReady, err, "derper's certificate for "+hostName+" cannot be read")`. `readServer` passes `row.HostName` and returns that error. A Let's Encrypt server is unchanged: `CertFiles` reports `ok` false and no certificate is read.
4. Update the doc comments of `readTailnet` and `certificate` to say what they now do.

`internal/modules/derp/server_probe.go`
1. Add the constants `serverProbeReadWait = 30 * time.Second` (the bound of each read of an agent during a probe) and `serverProbeParallel = 8` (the most reads in flight), with doc comments.
2. `serverProbeEach[T any](n int, f func(i int) T) []T`: runs `f(0)` to `f(n-1)` with at most `serverProbeParallel` running at once (a buffered channel as the semaphore and a `sync.WaitGroup`), writes each result at its own index and returns when all have ended, so the results keep input order.
3. `serverProbeBounded(ctx context.Context, env core.Env) (context.Context, func())`: returns a child of `ctx` that is cancelled when `serverProbeReadWait` has passed on `env.Clock` (`core.SystemClock{}` when `env.Clock` is nil), the way `deps.available` bounds its call, and a function that stops the timer and cancels the child. The bound uses the controller's clock so a test advances it.
4. `probeSelf` runs its `Resource` read under `serverProbeBounded`.
5. `probeSelves(ctx, env, hosts)` returns `(selves map[string]tailscale.Status, unreadable []probeHost, err error)`. It reads every host through `serverProbeEach`. A host whose read fails goes into `unreadable` (in host order); one that reads but whose node has no address is left out of both, as now. After the reads, if the caller's `ctx.Err()` is set, return it as the error. Update its doc comment.
6. `probeServer`: take the third result of `probeSelves`; read the agents with `serverProbeEach`, keeping the order of `agents`, and return the first error in that order; and append one warning per unreadable host after the existing ones (the probe's own warnings first, then the node-cap warnings, then these). The warning text is exactly `the tailscale status of host <name> could not be read, so its node use is not reported`, `<name>` being the host's `Name`. Put the text in a small unexported helper.
7. `probeFromAgent`: run `readNodeProbe` under `serverProbeBounded`. Its error rules do not change: when the caller's own `ctx.Err()` is set it returns that error; any other failure, including the bound firing, is a failed source carrying the coded detail when there is one and `the agent did not answer` otherwise.

`internal/modules/derp/fallback_check.go`
1. In `fallbackRead`, collect one reason per region that was tried and ruled out: for a `probeServer` error (when the caller's `ctx.Err()` is not set) `region <id>: <fallbackWhy(err)>`; for a probe that is not ready, `region <id>: <reason>` with the reason `probeReady` returns. Regions without a server row are not listed.
2. `fallbackWhy(err error) string`: the detail of a coded error that is not `errcode.Internal` and has a detail, else `the probe could not be read`. An uncoded error may carry transport internals, so its text is never used.
3. The `derp.no_fallback` refusal keeps its code, its definiteness and its first sentence; when any reason was collected it continues with `: ` and the reasons joined by `; `.

`internal/modules/derp/cert_renew.go`
1. `certRenewNames(ip string) (crt, key string, err error)`: return an error `derp: derper names no certificate file for the address "<ip>"` when `server.CertFiles` reports `ok` false or the file name has nothing before `.crt` (every character of the address was dropped, as for `::`). Update every caller: `certRenewCheck` and `Inverse` mark it `core.Definite`, `Apply`, `Verify` and `Observe` return it as it is.
2. `Verify`: wrap the error of `env.Units.State` as `derp: read the state of <unit>: %w`. Every other `Verify` error already starts with `derp:`.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
mkdir -p /root/.cache/w10a-tmp/9 && export TMPDIR=/root/.cache/w10a-tmp/9 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Sp|TestSs|TestPb|TestFb|TestCr|TestSv' ./internal/modules/derp/ ./internal/modules/derp/server/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1200 go test -count=1 ./internal/modules/derp/...
timeout 1800 go test -race -count=1 ./internal/modules/derp/ ./internal/modules/derp/server/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test passes; `go vet` and `go mod tidy -diff` print nothing; `golangci-lint` prints `0 issues.`; every line of `go test ./...` starts with `ok` or `?`. The derp package alone takes about four minutes. Record the real output in the report. Then clean up: `rm -rf /root/.cache/w10a-tmp/9 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/server_status.go internal/modules/derp/server_status_test.go
git commit -m "fix(derp): refuse status facts it cannot read"
git add internal/modules/derp/server_probe.go internal/modules/derp/server_probe_test.go internal/modules/derp/fallback_check.go internal/modules/derp/fallback_check_test.go
git commit -m "fix(derp): bound probe reads and name failures"
git add internal/modules/derp/server/server.go internal/modules/derp/server/server_test.go internal/modules/derp/cert_renew.go internal/modules/derp/cert_renew_test.go
git commit -m "fix(derp): refuse unknown modes and file names"
```

### Task 10: Tailscale pin rollback and tailnet fixes

**Files:**
- Modify: `internal/modules/tailscale/package_pin.go`
- Modify: `internal/modules/tailnet/tailnet.go`
- Modify: `internal/modules/tailnet/client.go`
- Modify: `internal/modules/tailnet/export.go` (its comment only)
- Modify: `internal/modules/tailnet/policy/policy.go`
- Test: `internal/modules/tailscale/package_pin_test.go`
- Test: `internal/modules/tailnet/export_test.go`
- Test: `internal/modules/tailnet/policy/policy_test.go`
- Test: `internal/modules/tailnet/policy/fuzz_test.go`

**Interfaces:**
- Consumes: nothing from another task. The tests reuse `pnNewSetup`, `pnCoded`, `pnPin`, `pnPinEffect` (package `tailscale`) and `edCanon`, `edInvalid` (package `policy`).
- Produces: nothing another task consumes. New unexported names: the constant `pinRollbackBudget` (10 minutes) in `package_pin.go` and the type `absentTransport` in `client.go`; `policy.member` now returns `(*hujson.Value, error)`. `policy.Apply` refuses, as `request.invalid_input` with the field `/edits/<i>`, an edit that has to pass through or end at a member an object names twice; its detail is `edit <i>: the object names "<name>" more than once`.

**Items:** 301, 319, 418

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

This task takes of row 301 only the rollback of `package_pin.go:240` on the forward context; the rest of that row belongs to other tasks. Three independent fixes, in three packages:
- A pin whose forward steps fail rolls back on a context of its own, so the rollback still runs when the 20-minute apply budget has expired.
- The tailnet module's actions and its exported readers share one token cache, and a test binary can never reach Tailscale's API through the exported readers.
- `policy.Apply` converges on every document by refusing an edit that would reach only one of two members of one name, instead of editing the first and leaving the second.

- [ ] **Step 1: Write the failing tests**

Add the tests below. Every new identifier starts with `w10tp` or `TestW10Tp`.

1. `internal/modules/tailscale/package_pin_test.go`: add `"github.com/darkraise/darkpanel/internal/platform/tslocal"` to the imports and append:

```go
// w10tpLocal ends the pin's forward context the first time tailscaled is asked for its status while the new
// version is installed, which is the check step, as the expiry of the apply budget would.
type w10tpLocal struct {
	localAPI
	apt    *pnApt
	cancel context.CancelFunc
}

func (l w10tpLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	if l.apt.version == pnTarget {
		l.cancel()
	}
	return l.localAPI.Status(ctx)
}

// w10tpCall is one command the runner was given and the state of the context it was given with.
type w10tpCall struct {
	vector   string
	err      error
	deadline time.Duration // until the context's deadline when it was given; 0 without one
}

// w10tpExec records the context of every command before it passes the command on.
type w10tpExec struct {
	inner core.Runner
	calls []w10tpCall
}

func (r *w10tpExec) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	c := w10tpCall{vector: pnVec(cmd.Path, cmd.Args), err: ctx.Err()}
	if dl, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(dl)
	}
	r.calls = append(r.calls, c)
	return r.inner.Run(ctx, cmd)
}

// TestW10TpPinRollsBackAfterTheForwardContextEnds: the rollback of a pin whose check ran out of the apply budget
// runs on a context of its own, bounded by 10 minutes, and still restores the prior version and hold.
func TestW10TpPinRollsBackAfterTheForwardContextEnds(t *testing.T) {
	s := pnNewSetup(t, true)
	s.apt.backend[pnTarget], s.apt.backend[pnPrior] = "Stopped", "Running"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := s.t.d.local
	s.t.d.local = func() localAPI { return w10tpLocal{localAPI: inner(), apt: s.apt, cancel: cancel} }
	rec := &w10tpExec{inner: s.apt}
	s.env.Exec = rec

	_, err := s.t.packagePin().Handler.Task(ctx, s.env, pnInput(pnTarget))
	coded := pnCoded(t, err, errcode.TailscalePinFailed)
	if !strings.Contains(coded.Detail, "tailscale is back at "+pnPrior) {
		t.Errorf("detail %q, want it to say the prior version is back", coded.Detail)
	}
	if p := pnPin(t, s.h); p.Phase != "rolled_back" {
		t.Errorf("pin phase %s, want rolled_back", p.Phase)
	}
	if s.apt.version != pnPrior || !s.apt.held {
		t.Errorf("host has version %s, held %v; want %s, held", s.apt.version, s.apt.held, pnPrior)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
		t.Errorf("effect state %s, want skipped: the host is as before", e.State)
	}
	rollbackInstall := pnVec(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior)))
	found := false
	for _, c := range rec.calls {
		if c.vector != rollbackInstall {
			continue
		}
		found = true
		if c.err != nil || c.deadline <= 0 || c.deadline > 10*time.Minute {
			t.Errorf("the rollback's install ran with context error %v and %v to its deadline, want a live context bounded by 10 minutes",
				c.err, c.deadline)
		}
	}
	if !found {
		t.Errorf("commands %+v, want the rollback to reinstall the staged prior package", rec.calls)
	}
}
```

2. `internal/modules/tailnet/export_test.go`: append:

```go
// TestW10TpModuleSharesTheExportedTokenCache: the module's actions and the exported readers use one token cache,
// so one account has one access token.
func TestW10TpModuleSharesTheExportedTokenCache(t *testing.T) {
	m, ok := Module().(module)
	if !ok {
		t.Fatalf("Module() is %T, want the tailnet module", Module())
	}
	if m.t.tokens != exportTN.tokens {
		t.Error("the module's actions and the exported readers keep separate token caches")
	}
}

// TestW10TpProductionTailnetCannotReachTheProviderInTests: the exported readers and the module's actions send
// nothing to Tailscale's API from a test binary, whether or not a test connects an account.
func TestW10TpProductionTailnetCannotReachTheProviderInTests(t *testing.T) {
	m, ok := Module().(module)
	if !ok {
		t.Fatalf("Module() is %T, want the tailnet module", Module())
	}
	for name, tt := range map[string]tn{"the exported readers": exportTN, "the module's actions": m.t} {
		if tt.d.http == nil {
			t.Errorf("%s use cloud's default client, which reaches %s", name, tt.d.baseURL)
			continue
		}
		res, err := tt.d.http.Get(tt.d.baseURL + oauthPath)
		if err == nil {
			_ = res.Body.Close()
			t.Errorf("%s: a request to %s was answered", name, tt.d.baseURL)
		}
	}
}
```

3. `internal/modules/tailnet/policy/policy_test.go`
   - Extend the test `policy_test.go::TestEDApplyRefusals` with these rows, placed directly before its row `the failing edit is named by its index`:

```go
		{name: "set on a member named twice", field: "/edits/0", in: `{"a": 1, "a": 2}`,
			edits: []Edit{{Op: "set", Path: "/a", Value: edVal(`3`)}}},
		{name: "delete of a member named twice", field: "/edits/0", in: `{"a": 1, "a": 2}`,
			edits: []Edit{{Op: "delete", Path: "/a"}}},
		{name: "addItem to a member named twice", field: "/edits/0", in: `{"a": [1], "a": [2]}`,
			edits: []Edit{{Op: "addItem", Path: "/a", Value: edVal(`3`)}}},
		{name: "removeItem from a member named twice", field: "/edits/0", in: `{"a": [1], "a": [2]}`,
			edits: []Edit{{Op: "removeItem", Path: "/a", Value: edVal(`1`)}}},
		{name: "a path through a member named twice", field: "/edits/0", in: `{"a": {"b": 1}, "a": {"b": 2}}`,
			edits: []Edit{{Op: "set", Path: "/a/b", Value: edVal(`3`)}}},
```

   - Append:

```go
// TestW10TpApplyNamesTheRepeatedMember: the refusal names the member, and a member named twice elsewhere in the
// document does not stop an edit of another member.
func TestW10TpApplyNamesTheRepeatedMember(t *testing.T) {
	_, err := Apply([]byte(`{"a": 1, "a": 2}`), []Edit{{Op: "delete", Path: "/a"}})
	var e *errcode.Error
	if !errors.As(err, &e) || e.Detail != `edit 0: the object names "a" more than once` {
		t.Errorf("error %v, want the detail %q", err, `edit 0: the object names "a" more than once`)
	}
	const doc = `{"a": 1, "a": 2, "b": 3}`
	once, err := Apply([]byte(doc), []Edit{{Op: "delete", Path: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(once), `"b"`) || strings.Count(string(once), `"a"`) != 2 {
		t.Errorf("Apply = %s, want the member b gone and both members a kept", once)
	}
	twice, err := Apply(once, []Edit{{Op: "delete", Path: "/b"}})
	if err != nil || string(twice) != string(once) {
		t.Errorf("the second delete gave %q, %v; want the text unchanged", twice, err)
	}
}
```

4. `internal/modules/tailnet/policy/fuzz_test.go`: `FuzzApply` skips every document with a repeated member name, which is the hole this task closes, so the skip goes. Delete the function `fz8Repeats` and its doc comment. In `FuzzApply` replace `doc, err := hujson.Parse(bytes.Clone(text))` and the `if err != nil { return }` after it with `if _, err := hujson.Parse(bytes.Clone(text)); err != nil { return }`, and delete the block `if fz8Repeats(doc) { return }`. The seed `{"a":1,"a":2}` with `delete /a` then runs as part of an ordinary `go test` and must converge.

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/10 && export TMPDIR=/root/.cache/w10a-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Tp|TestED|FuzzApply' ./internal/modules/tailscale/ ./internal/modules/tailnet/ ./internal/modules/tailnet/policy/
```

Expected: FAIL.
- `TestW10TpPinRollsBackAfterTheForwardContextEnds`: `detail "check failed (context canceled) and the rollback to 1.82.0 failed (tailscale: read pin c1: context canceled); tailscale needs repair"`, pin phase `checking`.
- `TestW10TpModuleSharesTheExportedTokenCache`: `keep separate token caches`.
- `TestW10TpProductionTailnetCannotReachTheProviderInTests`: `the exported readers use cloud's default client, which reaches https://api.tailscale.com`, and the same for `the module's actions`.
- `TestW10TpApplyNamesTheRepeatedMember` and the new `TestEDApplyRefusals` rows: `Apply succeeded`, error `<nil>`.
- `FuzzApply/seed#4`: `edit {Op:delete Path:/a Value:} of "{\"a\":1,\"a\":2}" does not converge`.

- [ ] **Step 3: Implement**

`internal/modules/tailscale/package_pin.go`
1. Add the constant `pinRollbackBudget = 10 * time.Minute` to the first `const` block, with a doc comment: the bound of the rollback of a failed pin, which runs on a context of its own.
2. `txFail` begins with `ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinRollbackBudget)` and `defer cancel()`, and uses that `ctx` for everything it does: `loadPin`, `rollback`, `Effects.Skipped`. The rollback therefore runs, journals its steps and writes its phases after the forward context has ended, and still ends after the budget. The forward steps keep the forward context. The one other `h.rollback` call, in `Task`'s repository-file failure path, is not on this row; leave it. Say in `txFail`'s doc comment why the context is detached.

`internal/modules/tailnet/tailnet.go`, `client.go`, `export.go`
1. `Module()` returns `module{t: exportTN}`, so the module's actions and `ReadPolicy`/`ListDevices` share one `tokenCache`.
2. `productionTN()` keeps the base URL `https://api.tailscale.com` and the token cache; under `testing.Testing()` it also sets `deps.http` to `&http.Client{Transport: absentTransport{}}`. `absentTransport` is a new type whose `RoundTrip` returns the error `tailnet: a test binary never reaches the Tailscale API` without touching the network. Give it a doc comment saying a test binary that links the module must never reach the real API, whether or not a test connects an account (the same reason `absentSocket` exists in the tailscale module). `cloud.New` copies a supplied client and keeps its transport, so every request of the module's actions is refused, `ValidatePolicy` included. Import `testing` in `client.go`. The existing `TestAPProductionTN` compares only the base URL and the token cache, so it stays as it is and must still pass.
3. `export.go`: reword the comment on `exportTN` to say it is the one value the module's actions and the exported readers share, so one token cache and one access token per account.

`internal/modules/tailnet/policy/policy.go`
1. `member(obj, name)` returns `(*hujson.Value, error)`: nil and no error when no member has the name, the value when exactly one has, and `refusal(fmt.Sprintf("the object names %q more than once", name))` when more than one does. Update its doc comment.
2. `walkToParent` and `applyEdit` (the lookup of `target`) handle the new error and return it unchanged; `Apply` already turns a `refusal` into `request.invalid_input` with the field `/edits/<i>` and the detail `edit <i>: <refusal>`. A name that is repeated in an object an edit does not touch, and every edit of a document without repeated names, behave as before.
3. Add to `Apply`'s doc comment that an object that names a member twice, where an edit passes or ends, is one of the refusals.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
mkdir -p /root/.cache/w10a-tmp/10 && export TMPDIR=/root/.cache/w10a-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 ./internal/modules/tailscale/ ./internal/modules/tailnet/...
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1800 go test -race -count=1 ./internal/modules/tailscale/ ./internal/modules/tailnet/...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test passes, including the unchanged `TestPn*`, `TestEx*` and `TestED*`; `go vet` and `go mod tidy -diff` print nothing; `golangci-lint` prints `0 issues.`; every line of `go test ./...` starts with `ok` or `?`. Record the real output in the report. Then clean up: `rm -rf /root/.cache/w10a-tmp/10 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_pin.go internal/modules/tailscale/package_pin_test.go
git commit -m "fix(tailscale): roll back a failed pin detached"
git add internal/modules/tailnet/tailnet.go internal/modules/tailnet/client.go internal/modules/tailnet/export.go internal/modules/tailnet/export_test.go
git commit -m "fix(tailnet): share one token cache"
git add internal/modules/tailnet/policy/policy.go internal/modules/tailnet/policy/policy_test.go internal/modules/tailnet/policy/fuzz_test.go
git commit -m "fix(tailnet): refuse edits to repeated names"
```

### Task 11: Module availability after a successful change

**Files:**
- Modify: `internal/controller/dispatch/availability.go`
- Modify: `internal/controller/dispatch/apply.go`
- Test: `internal/controller/dispatch/availability_test.go`

**Interfaces:**
- Consumes: nothing from another task. The test reuses `newDav`, `davHandler`, `wantCode` and `alice` from the package's test files.
- Produces: nothing another task consumes. New unexported names: `(*availabilityCache).retire(availabilityKey)` and `applySucceeded(core.ChangeView) bool`. No exported signature or wire type changes.

**Items:** 298

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

This task implements ruling W10-R6: a successful apply of a change of module M on host H retires the availability cache entry for (H, M), so the next request asks the agent again and an install that brings the module up is seen at once. Today the 60-second cache keeps answering `module.unavailable` with the reason cached before the install, which is misleading.

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/dispatch/availability_test.go`. Every new identifier starts with `w10av`, `refW10av` or `TestW10Av`. The module `w10av` has an install action the fake agent always reports available and a pin action it reports unavailable until the answer is changed; `newDav` already routes the agent's `Availability` to `f.agent`.

```go
var (
	refW10avInstall = core.ActionRef{Name: "w10av.pkg.install", Version: 1}
	refW10avPin     = core.ActionRef{Name: "w10av.pkg.pin", Version: 1}
)

// w10avModule has two agent actions: install, which a host can always run, and pin, which the fake agent reports
// unavailable until the install has brought the module up.
type w10avModule struct{}

func (w10avModule) Manifest() core.Manifest {
	spec := func(ref core.ActionRef) core.ActionSpec {
		return core.ActionSpec{Name: ref.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, ForwardOnly: true, Converges: true}
	}
	return core.Manifest{ID: "w10av", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineMutating(spec(refW10avInstall), davHandler{}),
		core.DefineMutating(spec(refW10avPin), davHandler{}),
	}}
}

// w10avAnswer has w10av.pkg.install available and w10av.pkg.pin unavailable with pinReason, available when it is empty.
func w10avAnswer(pinReason string) core.ModuleAvailability {
	return core.ModuleAvailability{Available: true, Actions: []core.ActionAvailability{
		{Name: refW10avInstall.Name, Available: true},
		{Name: refW10avPin.Name, Available: pinReason == "", Reason: pinReason},
	}}
}

// w10avDown is a fixture whose agent reports pin unavailable, with that answer cached and an install planned.
func w10avDown(t *testing.T) (*davFixture, core.PlanView) {
	t.Helper()
	f := newDav(t, w10avModule{})
	f.agent.set(w10avAnswer("tailscaled does not answer on its socket"), nil)
	install, err := f.plan(refW10avInstall, testHost)
	if err != nil {
		t.Fatalf("Plan of the install: %v", err)
	}
	_, err = f.plan(refW10avPin, testHost)
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	if n := f.agent.count(); n != 1 {
		t.Fatalf("the agent was asked %d times, want 1", n)
	}
	return f, install
}

// TestW10AvSuccessfulApplyRetiresTheModulesCachedAnswer: once an apply of a change of the module succeeds on the
// host, the next request asks the agent again instead of using the answer cached before the apply.
func TestW10AvSuccessfulApplyRetiresTheModulesCachedAnswer(t *testing.T) {
	f, install := w10avDown(t)
	v, err := f.d.Apply(context.Background(), alice, testHost, refW10avInstall, install.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply of the install: %v", err)
	}
	if v.State != core.StateCompleted {
		t.Fatalf("the install ended in state %q, want completed", v.State)
	}
	f.agent.set(w10avAnswer(""), nil)
	if _, err := f.plan(refW10avPin, testHost); err != nil {
		t.Fatalf("Plan of the pin after the install: %v, want the module asked again and the pin available", err)
	}
	if n := f.agent.count(); n != 2 {
		t.Errorf("the agent was asked %d times, want 2: once before the install, once after", n)
	}
}

// TestW10AvFailedApplyKeepsTheModulesCachedAnswer: an apply the agent refuses leaves the cached answer in place; the
// same plan applied again and succeeding retires it.
func TestW10AvFailedApplyKeepsTheModulesCachedAnswer(t *testing.T) {
	f, install := w10avDown(t)
	rt := f.h.rt
	client := func(apply func(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error)) *coretest.FakeAgent {
		return &coretest.FakeAgent{
			HelloFunc: rt.Hello, ReadFunc: rt.Read, PlanFunc: rt.Plan, ChangeFunc: rt.Change,
			SetModuleEnabledFunc: rt.SetModuleEnabled, AvailabilityFunc: f.agent.availability, ApplyFunc: apply,
		}
	}
	f.h.clients.Add(testHost, client(func(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error) {
		return core.ChangeView{}, errcode.New(errcode.LockHeld, "w10av is locked")
	}))
	ctx := context.Background()
	_, err := f.d.Apply(ctx, alice, testHost, refW10avInstall, install.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.LockHeld)
	f.agent.set(w10avAnswer(""), nil)
	_, err = f.plan(refW10avPin, testHost)
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	if n := f.agent.count(); n != 1 {
		t.Fatalf("the agent was asked %d times, want 1: the failed apply keeps the cached answer", n)
	}

	f.h.clients.Add(testHost, client(rt.Apply))
	if _, err := f.d.Apply(ctx, alice, testHost, refW10avInstall, install.PlanID, core.ApplyOptions{}); err != nil {
		t.Fatalf("second Apply of the install: %v", err)
	}
	if _, err := f.plan(refW10avPin, testHost); err != nil {
		t.Fatalf("Plan of the pin after the install succeeded: %v", err)
	}
	if n := f.agent.count(); n != 2 {
		t.Errorf("the agent was asked %d times, want 2", n)
	}
}

// TestW10AvPendingApplyRetiresTheModulesCachedAnswer: a revertible change that the agent applied and holds pending
// for its confirmation is a successful apply, so it retires the cached answer too.
func TestW10AvPendingApplyRetiresTheModulesCachedAnswer(t *testing.T) {
	f, install := w10avDown(t)
	rt := f.h.rt
	f.h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: rt.Hello, ReadFunc: rt.Read, PlanFunc: rt.Plan, ChangeFunc: rt.Change,
		SetModuleEnabledFunc: rt.SetModuleEnabled, AvailabilityFunc: f.agent.availability,
		ApplyFunc: func(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			v, err := rt.Apply(ctx, p, planID, opts)
			if err == nil {
				v.State = core.StatePending
			}
			return v, err
		},
	})
	if _, err := f.d.Apply(context.Background(), alice, testHost, refW10avInstall, install.PlanID, core.ApplyOptions{}); err != nil {
		t.Fatalf("Apply of the install: %v", err)
	}
	f.agent.set(w10avAnswer(""), nil)
	if _, err := f.plan(refW10avPin, testHost); err != nil {
		t.Fatalf("Plan of the pin after a pending install: %v, want the module asked again and the pin available", err)
	}
	if n := f.agent.count(); n != 2 {
		t.Errorf("the agent was asked %d times, want 2: once before the install, once after", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/11 && export TMPDIR=/root/.cache/w10a-tmp/11 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Av' ./internal/controller/dispatch/
```

Expected: FAIL. `TestW10AvSuccessfulApplyRetiresTheModulesCachedAnswer`: `Plan of the pin after the install: module.unavailable: action w10av.pkg.pin is unavailable on host h1: tailscaled does not answer on its socket`. `TestW10AvFailedApplyKeepsTheModulesCachedAnswer` passes its first half (the refused apply keeps the cache) and fails the same way after its second, successful apply. `TestW10AvPendingApplyRetiresTheModulesCachedAnswer` fails like the first test, with `Plan of the pin after a pending install: module.unavailable: ...`.

- [ ] **Step 3: Implement**

`internal/controller/dispatch/availability.go`
1. Add `(*availabilityCache).retire(k availabilityKey)`: under the cache's lock, delete the entry for `k`; deleting an absent entry is fine. Doc comment: it drops the cached answer, so the next request asks again.
2. Add `applySucceeded(v core.ChangeView) bool`: true exactly when `v.State` is `core.StateCompleted`, `core.StatePending` (applied, waiting for its confirmation) or `core.StateConfirmed`. A failed, interrupted, reverted or still applying change is not a success.
3. Extend the doc comment of `Availability`: an answer is also dropped when an apply of a change of the module succeeds on the host.

`internal/controller/dispatch/apply.go`
1. In `Apply`, the apply's result reaches dispatch in the block at its end, `if v.ID != "" && def.Spec.Side == core.SideAgent { d.index(...); d.ingest(...) }`. After `d.ingest(ctx, hostID)` in that block, add: when `err == nil && applySucceeded(v)`, call `d.avail.retire(availabilityKey{hostID, moduleOf(a.Name)})`.
2. Nothing else changes: a controller-side action has no host entry, a change the agent refuses or fails leaves the cache alone, and `checkApplied` still runs first, so a view of another host or action never retires anything. Update `Apply`'s doc comment with one sentence on the retirement.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
mkdir -p /root/.cache/w10a-tmp/11 && export TMPDIR=/root/.cache/w10a-tmp/11 GOTOOLCHAIN=local
timeout 600 go test -count=1 ./internal/controller/dispatch/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -count=1 ./internal/controller/dispatch/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test passes, including the unchanged `TestAvailabilityCached` and `TestDispatchRefusesUnavailableAction`; `go vet` and `go mod tidy -diff` print nothing; `golangci-lint` prints `0 issues.`; every line of `go test ./...` starts with `ok` or `?`. Record the real output in the report. Then clean up: `rm -rf /root/.cache/w10a-tmp/11 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/availability.go internal/controller/dispatch/apply.go internal/controller/dispatch/availability_test.go
git commit -m "fix(dispatch): retire availability after an apply"
```

### Task 12: NPM upgrade rollback

**Files:**
- Create: `internal/modules/npm/migrations/agent/0002_npm_upgrade_failed_from.sql`
- Create: `internal/modules/npm/upgrade_store.go`
- Modify: `internal/modules/npm/instance_upgrade.go`
- Modify: `internal/modules/npm/upgrade_recover.go`
- Test: `internal/modules/npm/instance_upgrade_test.go`
- Test: `internal/modules/npm/upgrade_recover_test.go`

**Interfaces:**
- Consumes: nothing from another task. The tests reuse `upNewSetup`, `upSetup`, `urSetup` and the Docker fakes of `testhost_test.go`.
- Produces: the migration `0002_npm_upgrade_failed_from.sql`, which adds the column `npm_upgrades.failed_from`. In `upgrade_store.go`: `loadUpgradeFailedFrom(ctx context.Context, q queryer, changeID string) (string, error)`, `setUpgradeFailed(ctx context.Context, db *store.DB, changeID, from, result string, at time.Time) error`, and `upgradeArchivesInUse(ctx context.Context, q queryer) (map[string]bool, error)`, moved from `instance_upgrade.go` with its name and behaviour unchanged. In `instance_upgrade.go`: `(instanceUpgradeHandler).dropPartialArchives(ctx, env, changeID) error` and `notDefinite(err error) error`. Any later NPM task that adds a migration numbers it `0003` or higher.

**Items:** 327, 351, 353, 354, 355

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 3 = 7
**Lenses:** silent-failures

`store.go` (where `upgradeRow`, `loadUpgrade` and `setUpgradePhase` live) is not edited: the new column is read and written only by the two functions of `upgrade_store.go`, so this task shares no file with the other NPM tasks.

Decisions this task embodies:
- Row 327. A rollback that fails ends the transaction in phase `failed`, which forgets whether the original container had been removed. The row now records the phase the rollback failed in (`failed_from`), and a later rollback of a `failed` row takes the branch of that phase: it only starts the original container when it was never removed (`recorded`, `stopped`, `archiving`, `archived`) and restores it from the verified archives otherwise (`recreating`, `waiting`, `done`). A `failed` row written before the column existed has `failed_from` empty and takes the restore branch, as today. `Recover` still reports a `failed` row as `needs_repair` and `Inverse` still refuses it; this task makes the rollback itself correct for the repair path that will call it.
- Row 351. An upgrade whose context ends while it waits for the API says it was interrupted, not that the API did not answer within 120 s. A failed upgrade is never definite, whatever its cause says, because the host has changed by then. The raw `npm_upgrades` SQL leaves the handler file.
- Row 354. `Recover`'s doc comment says what it does. `checkArchives` is `restoreArchives` without its contents. `recoverOriginal` and `resume` share the removal of half-written archives, and both leave the stored archive list as it is.
- Row 355. The rollback-failure test asserts exactly `failed`, and an archive read error is returned with the `npm:` prefix.
- Row 353. The rollback paths that had no test now have one.

- [ ] **Step 1: Write the failing tests**

Every new identifier starts with `w10nu` or `TestW10Nu`. Run `gofmt -w` on each edited file.

1. `internal/modules/npm/instance_upgrade_test.go`: append (the file already imports everything the code uses):

```go
// w10nuFailedFrom is the failed_from column of the upgrade row of change c1.
func w10nuFailedFrom(t *testing.T, h *thHost) string {
	t.Helper()
	var from string
	if err := h.db.Read().QueryRow(`SELECT failed_from FROM npm_upgrades WHERE change_id = 'c1'`).Scan(&from); err != nil {
		t.Fatal(err)
	}
	return from
}

// TestW10NuRollbackOfAFailedRowKeepsItsBranch: an upgrade whose rollback failed records the phase it failed in, and
// a later rollback of the failed row takes that phase's branch: it starts the original container when it was never
// removed, and restores it from the archives when it was.
func TestW10NuRollbackOfAFailedRowKeepsItsBranch(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(s *upSetup)
		failedFrom string
		check      func(t *testing.T, s *upSetup)
	}{
		{"the original was only stopped", func(s *upSetup) {
			s.files.failWrite = func(name string) error {
				if name == "c1-letsencrypt.tar" {
					return errors.New("disk full")
				}
				return nil
			}
			s.dock.fail = func(call string, n int) error {
				if call == "StartContainer" && n == 1 {
					return errors.New("docker: start refused")
				}
				return nil
			}
		}, "archiving", func(t *testing.T, s *upSetup) {
			c, ok := s.h.docker.container(s.inst.ContainerID)
			if !ok || !c.State.Running || s.dock.counts["RemoveContainer"] != 0 || s.dock.counts["CreateContainer"] != 0 {
				t.Errorf("original %+v (found %v), removals %d, creates %d; want it running again and nothing removed or created",
					c, ok, s.dock.counts["RemoveContainer"], s.dock.counts["CreateContainer"])
			}
		}},
		{"the original was removed", func(s *upSetup) {
			s.apiDown()
			s.dock.fail = func(call string, n int) error {
				if call == "CopyToContainer /etc" && n == 1 {
					return errors.New("docker: no space left on device")
				}
				return nil
			}
		}, "waiting", func(t *testing.T, s *upSetup) {
			c, ok := s.h.docker.container(containerName)
			row := s.instanceRow()
			if !ok || !c.State.Running || c.Config.Image != upFromImage || c.ID != row.ContainerID || row.Image != upFromImage {
				t.Errorf("container %+v (found %v) with the instance row at %s %s; want a running container of %s that the row names",
					c, ok, row.ContainerID, row.Image, upFromImage)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := upNewSetup(t, upFromImage, true)
			tc.setup(s)
			if _, err := s.apply(); err == nil {
				t.Fatal("the upgrade succeeded, want it to fail")
			}
			if got := s.phase(); got != "failed" {
				t.Fatalf("phase %q, want failed", got)
			}
			if got := w10nuFailedFrom(t, s.h); got != tc.failedFrom {
				t.Fatalf("failed_from %q, want %q", got, tc.failedFrom)
			}
			s.dock.fail, s.files.failWrite = nil, nil
			if err := (instanceUpgradeHandler{n: s.n}).rollback(context.Background(), s.env, s.upgrade()); err != nil {
				t.Fatalf("rollback of the failed row: %v", err)
			}
			if got := s.phase(); got != "rolled_back" {
				t.Errorf("phase %q, want rolled_back", got)
			}
			tc.check(t, s)
			if effects := s.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
				t.Errorf("journal %+v, want one skipped effect", effects)
			}
		})
	}
}

// TestW10NuRollbackFailuresAreRecordedWithTheirPhase: each step of a rollback that fails ends the transaction in
// phase failed with the reason and the phase it failed in, whichever branch it was on.
func TestW10NuRollbackFailuresAreRecordedWithTheirPhase(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		fail       string // the Docker method that fails once; empty: none
		noInstance bool   // the instance row is gone
		want       string // in the error
	}{
		{"the replacement cannot be removed", "waiting", "RemoveContainer", false, "remove container"},
		{"a volume cannot be removed", "waiting", "RemoveVolume", false, "remove volume"},
		{"a volume cannot be created", "waiting", "CreateVolume", false, "create volume"},
		{"the original cannot be created", "waiting", "CreateContainer", false, "create container"},
		{"an archive cannot be copied back", "waiting", "CopyToContainer", false, "copy the archive"},
		{"the recreated original cannot be started", "waiting", "StartContainer", false, "start container"},
		{"the stopped original cannot be started", "stopped", "StartContainer", false, "start container"},
		{"no instance is recorded", "stopped", "", true, "no Nginx Proxy Manager instance is recorded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := urSetup(t)
			var refs []archiveRef
			if tc.phase == "waiting" {
				f.replace(urTo)
				refs = f.archives()
			} else {
				f.setRunning(f.inst.ContainerID, false)
			}
			f.record(tc.phase, "", refs)
			if tc.fail != "" {
				f.h.docker.Fail(tc.fail, errors.New("docker refused"))
			}
			if tc.noInstance {
				err := f.h.db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, `DELETE FROM npm_instances`); return err })
				if err != nil {
					t.Fatal(err)
				}
			}
			u, ok, err := loadUpgrade(ctx, f.h.db.Read(), urChange)
			if err != nil || !ok {
				t.Fatalf("upgrade row: found %v, error %v", ok, err)
			}
			err = (instanceUpgradeHandler{n: f.n}).rollback(ctx, f.h.env(urChange), u)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rollback = %v, want an error containing %q", err, tc.want)
			}
			got, _, err := loadUpgrade(ctx, f.h.db.Read(), urChange)
			if err != nil {
				t.Fatal(err)
			}
			if got.Phase != "failed" || got.Result == "" || !strings.Contains(got.Result, tc.want) {
				t.Errorf("upgrade row %+v, want phase failed with a result containing %q", got, tc.want)
			}
			var from string
			if err := f.h.db.Read().QueryRow(`SELECT failed_from FROM npm_upgrades WHERE change_id = ?`, urChange).Scan(&from); err != nil || from != tc.phase {
				t.Errorf("failed_from %q (error %v), want %q", from, err, tc.phase)
			}
		})
	}
}

// TestW10NuInterruptedWaitIsNotReportedAsATimeout: an upgrade whose context ends while it waits for the new
// container's API says it was interrupted, not that the API did not answer within the wait, and still restores the
// original.
func TestW10NuInterruptedWaitIsNotReportedAsATimeout(t *testing.T) {
	s := upNewSetup(t, upFromImage, true)
	s.apiDown()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = s.n.instanceUpgrade().Handler.Apply(ctx, s.env, json.RawMessage(`{}`))
	}()
	timeout := time.After(30 * time.Second)
wait:
	for {
		select {
		case <-done:
			break wait
		case <-timeout:
			t.Fatal("the upgrade did not end")
		default:
			if s.h.clock.Pending() > 0 {
				cancel()
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want an npm.unhealthy error", err)
	}
	const want = "the upgrade was interrupted while waiting for the new Nginx Proxy Manager container; the original container and volumes were restored"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	s.wantRestored()
}

// w10nuDefiniteDown is an NPM client whose API fails with an error marked definite.
type w10nuDefiniteDown struct{ npmAPI }

func (w10nuDefiniteDown) Health(context.Context) (npmapi.Health, error) {
	return npmapi.Health{}, core.Definite(errors.New("npmapi: refused"))
}

// TestW10NuFailedUpgradeIsNeverDefinite: the host has changed when a step fails, so a failure is not definite
// however definite its cause was, whether or not the rollback succeeded.
func TestW10NuFailedUpgradeIsNeverDefinite(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *upSetup)
	}{
		{"the API check fails definitely", func(s *upSetup) {
			inner := s.n.d.anon
			s.n.d.anon = func() npmAPI { return w10nuDefiniteDown{npmAPI: inner()} }
		}},
		{"the API check fails definitely and the rollback fails", func(s *upSetup) {
			inner := s.n.d.anon
			s.n.d.anon = func() npmAPI { return w10nuDefiniteDown{npmAPI: inner()} }
			s.dock.fail = func(call string, _ int) error {
				if call == "CopyToContainer /etc" {
					return errors.New("docker: no space left on device")
				}
				return nil
			}
		}},
		{"an archive reads back with a definite failure", func(s *upSetup) {
			s.n.d.readFile = func(string) ([]byte, error) { return nil, core.Definite(errors.New("read refused")) }
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := upNewSetup(t, upFromImage, true)
			tc.setup(s)
			_, err := s.apply()
			if err == nil || core.IsDefinite(err) {
				t.Errorf("error %v (definite %v), want an error that is not definite", err, core.IsDefinite(err))
			}
		})
	}
}
```

2. `internal/modules/npm/upgrade_recover_test.go`
   - Extend the test `upgrade_recover_test.go::TestUrRecoverWhenTheRollbackFails`: replace its last assertion with the exact phase:

```go
	if got := f.phase(); got != "failed" {
		t.Errorf("phase %q, want failed", got)
	}
```

   - Append:

```go
// TestW10NuInverseArchiveReadErrorsStartWithThePackageName: an archive that cannot be read is a definite refusal
// whose text starts with "npm:".
func TestW10NuInverseArchiveReadErrorsStartWithThePackageName(t *testing.T) {
	f := urSetup(t)
	f.replace(urTo)
	f.record("done", "", f.archives())
	f.n.d.readFile = func(string) ([]byte, error) { return nil, errors.New("disk exploded") }
	mark := f.mark()
	err := f.inverse(urResource)
	if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "npm:") || !strings.Contains(err.Error(), "disk exploded") ||
		len(f.callsSince(mark)) != 0 {
		t.Errorf("error %v (definite %v) after Docker calls %v, want a definite refusal that starts with %q before the host is touched",
			err, core.IsDefinite(err), f.callsSince(mark), "npm:")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/12 && export TMPDIR=/root/.cache/w10a-tmp/12 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Nu|TestUrRecoverWhenTheRollbackFails' ./internal/modules/npm/
```

Expected: FAIL.
- `TestW10NuRollbackOfAFailedRowKeepsItsBranch` and `TestW10NuRollbackFailuresAreRecordedWithTheirPhase`: `SQL logic error: no such column: failed_from`.
- `TestW10NuInterruptedWaitIsNotReportedAsATimeout`: the detail says `did not answer within 120 s`.
- `TestW10NuFailedUpgradeIsNeverDefinite`: `want an error that is not definite`, three rows.
- `TestW10NuInverseArchiveReadErrorsStartWithThePackageName`: the error text is `disk exploded`, without the prefix.
- `TestUrRecoverWhenTheRollbackFails` already passes: it guards the exact phase through the implementation.

- [ ] **Step 3: Implement**

1. `internal/modules/npm/migrations/agent/0002_npm_upgrade_failed_from.sql`: create it with exactly this content. The embed pattern `migrations/agent/*.sql` in `store.go` picks it up, and a table rebuild is not needed.

```sql
-- The phase an upgrade's rollback was in when it failed. A later rollback of the same row takes the same branch:
-- before the original container is removed it only starts that container again, and afterwards it restores the
-- original from the archives. Empty for a row that never failed, and for one that failed before this column existed.
ALTER TABLE npm_upgrades ADD COLUMN failed_from TEXT NOT NULL DEFAULT '' CHECK (failed_from IN ('', 'recorded', 'stopped', 'archiving', 'archived', 'recreating', 'waiting', 'done'));
```

2. `internal/modules/npm/upgrade_store.go` (new, package `npm`):
   - `loadUpgradeFailedFrom(ctx, q queryer, changeID)`: `SELECT failed_from FROM npm_upgrades WHERE change_id = ?`; no row is an error `npm: read upgrade <id>: no such upgrade`; any other failure is wrapped `npm: read upgrade <id>: %w`.
   - `setUpgradeFailed(ctx, db, changeID, from, result, at)`: one `UPDATE npm_upgrades SET phase = 'failed', failed_from = ?, result = ?, updated_at = ? WHERE change_id = ?` through `db.Write`, the time through `storeTime`, the archives column untouched; no row affected is an error; failures are wrapped `npm: set the phase of upgrade <id>: %w`, as `setUpgradePhase` does.
   - Move `upgradeArchivesInUse` here from `instance_upgrade.go` unchanged. It is the raw `npm_upgrades` SQL and the archives JSON decoding the register row wants out of the handler file.

3. `internal/modules/npm/instance_upgrade.go`
   - `rollback(ctx, env, u)`: `from` is `u.Phase`, except that for `u.Phase == "failed"` it is `loadUpgradeFailedFrom(ctx, env.DB.Read(), u.ChangeID)` (return its error without changing the row). Branch on `from`: `rolled_back` returns nil; `recorded`, `stopped`, `archiving`, `archived` call `resume` with a copy of `u` whose `Phase` is `from`; `recreating`, `waiting`, `done` and the empty string call `restore`; anything else is the unknown-phase error, which now names `from`. A failure ends through `setUpgradeFailed(ctx, env.DB, u.ChangeID, from, upgradeResult(err), env.Clock.Now())` instead of `setUpgradePhase(..., "failed", ...)`; the success path is unchanged. Update its doc comment: a `failed` row takes the branch of the phase it failed in.
   - New `dropPartialArchives(ctx, env, changeID)`: for each volume of `instanceUpgradeVolumes`, `env.Files.RemoveManaged(ctx, ID, backupDir, archiveName(changeID, v.volume))`, ignoring `fs.ErrNotExist` and returning other failures as `npm: remove the archive <name>: %w`. `resume` calls it, for phase `archiving`, in place of its loop over `u.Archives`.
   - `restoreArchives`: a read error that matches `fs.ErrNotExist` returns `errcode.New(errcode.NotFound, "the backup archive "+a.File+" of the upgrade no longer exists")`; other read errors keep their `npm:` text.
   - New `notDefinite(err error) error`: nil stays nil; an error for which `core.IsDefinite` is true, however deep in its chain, is replaced by `errors.New(err.Error())`; any other error is returned as it is.
   - `fail`: begin by replacing `cause` with `notDefinite(cause)` and, after the rollback has run, `rbErr` with `notDefinite(rbErr)`, so nothing it returns satisfies `core.IsDefinite`. In the `wait` step, when `errors.Is(cause, context.Canceled)` or `errors.Is(cause, context.DeadlineExceeded)`, the lead of the detail is `the upgrade was interrupted while waiting for the new Nginx Proxy Manager container` instead of `the new Nginx Proxy Manager container did not answer within <wait>`; both continue as today (`; the original container and volumes were restored`, or `; restoring the original failed and the instance needs repair`). The error codes do not change. Update its doc comment.
   - Remove `upgradeArchivesInUse`, and any import it leaves unused.

4. `internal/modules/npm/upgrade_recover.go`
   - Rewrite `Recover`'s doc comment: it decides by the row alone; the rollback it runs from `recreating` or `waiting` records the upgrade's effect as skipped, and the engine settles every other outcome through `Observe`. Drop "journals nothing".
   - `checkArchives(u)` becomes `_, err := h.restoreArchives(u); return err`, so `Inverse` checks the count, the volumes and the checksums that the restore itself will check, and a pruned archive is still a `NotFound` refusal (through the `restoreArchives` change above). Remove the code it no longer needs and the imports that leaves unused.
   - `recoverOriginal`: use `dropPartialArchives(ctx, env, env.ChangeID)` for the `archiving` phase and write `rolled_back` with a nil archive list (`setUpgradePhase` keeps the stored list on nil), as `rollback` does.

   `Recover`'s `failed` case and `Inverse`'s refusal of `failed` do not change.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
mkdir -p /root/.cache/w10a-tmp/12 && export TMPDIR=/root/.cache/w10a-tmp/12 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Nu|TestUp|TestUr|TestSd' ./internal/modules/npm/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1200 go test -count=1 ./internal/modules/npm/...
timeout 1800 go test -race -count=1 ./internal/modules/npm/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test passes, including the unchanged `TestUp*`, `TestUr*` and `TestSd*` (`TestUpPrunesOldArchiveSets` covers the moved `upgradeArchivesInUse`, `TestUrInverse` the `NotFound` refusal and `TestUpFailureBeforeTheRecreateStartsTheOriginalAgain` the shared archive removal); `go vet` and `go mod tidy -diff` print nothing; `golangci-lint` prints `0 issues.`; every line of `go test ./...` starts with `ok` or `?`. This task changes no wire type, so `schemas/**` and `docs/api/**` stay as they are. Record the real output in the report. Then clean up: `rm -rf /root/.cache/w10a-tmp/12 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/migrations/agent/0002_npm_upgrade_failed_from.sql internal/modules/npm/upgrade_store.go internal/modules/npm/instance_upgrade.go internal/modules/npm/upgrade_recover.go internal/modules/npm/instance_upgrade_test.go internal/modules/npm/upgrade_recover_test.go
git commit -m "fix(npm): restore a failed upgrade by its branch"
```

### Task 13: NPM proxy, claims and access-list validation

**Files:**
- Modify: `internal/modules/npm/proxyhost_add.go` (`proxyhostAddHostProblem`, `checkUpstream`, `proxyhostAddHandler.check`, `proxyhostAddHandler.Inverse`)
- Modify: `internal/modules/npm/proxyhost_edit.go` (`proxyHostUpdateHandler.check`, `proxyHostRemoveHandler.target`, `proxyHostRemoveHandler.Apply`)
- Modify: `internal/modules/npm/network_claims.go` (`networkAttachHandler.check`, `networkClaims.networkUndoConnect`, `networkClaims.networkUndoDisconnect`)
- Modify: `internal/modules/npm/accesslist.go` (`accessListAddHandler.Verify`)
- Modify: `internal/modules/npm/instance_deploy.go` (`checkAcmeEmail`, `deployHandler.checkPorts`, `deployHandler.Apply`)
- Test: `internal/modules/npm/proxyhost_add_test.go`
- Test: `internal/modules/npm/proxyhost_edit_test.go`
- Test: `internal/modules/npm/network_claims_test.go`
- Test: `internal/modules/npm/accesslist_test.go`
- Test: `internal/modules/npm/instance_deploy_test.go`

**Interfaces:**
- Consumes: the test host of `testhost_test.go` (`newTHHost`, `thHost`, `thDocker`, `thAPIError`) and the helpers of the five test files above, all on `main`. Nothing from another task.
- Produces: nothing another task consumes. No wire type changes, so `schemas/**` and `docs/api/**` stay as they are.

**Items:** 336, 338, 339, 340, 342, 343, 344

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

This task changes several behaviours of the module's agent actions, each with its own test. The fixes are independent of each other; do them in the order of the tests below.

- [ ] **Step 1: Write the failing test**

The tests below use literal status numbers and the imports their files already have; no import changes.

**1a. `proxyhost_add_test.go`.** Extend the test `proxyhost_add_test.go::TestPaValidate`: add these three rows to its `tests` table, after the row `"host with a scheme"`:

```go
		{name: "an IPv6 literal host", mutate: func(in *ProxyHostInput) { in.ForwardHost = "2001:db8::1" }, field: "/forwardHost"},
		{name: "the IPv6 loopback host", mutate: func(in *ProxyHostInput) { in.ForwardHost = "::1" }, field: "/forwardHost"},
		{name: "an IPv6 host with a zone", mutate: func(in *ProxyHostInput) { in.ForwardHost = "fe80::1%eth0" }, field: "/forwardHost"},
```

The zone row passes before the fix, because the zone refusal exists; it pins that refusal. The two literal rows fail.

Append to `internal/modules/npm/proxyhost_add_test.go` (its imports already hold everything these use):

```go
// TestW10NvHostProblemSaysWhyAHostIsRefused: an IPv6 literal is refused because NPM writes the forward host into
// proxy_pass without brackets, and a zone is refused for its own reason.
func TestW10NvHostProblemSaysWhyAHostIsRefused(t *testing.T) {
	tests := []struct {
		host string
		want string // a word the problem holds; "" accepted
	}{
		{"fe80::1%eth0", "zone"},
		{"2001:db8::1", "brackets"},
		{"::1", "brackets"},
		{"::ffff:10.0.0.5", "brackets"},
		{"10.0.0.5", ""},
		{"web", ""},
		{"web.internal", ""},
	}
	for _, tc := range tests {
		got := proxyhostAddHostProblem(tc.host)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("proxyhostAddHostProblem(%q) = %q, want a problem naming %q (none when empty)", tc.host, got, tc.want)
		}
	}
}

// TestW10NvAddEngineReadFailureIsNotARefusal: a failed read of the Engine says nothing about the upstream, so neither
// the plan nor the apply marks it as a refusal.
func TestW10NvAddEngineReadFailureIsNotARefusal(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		err  error
		code errcode.Code // "": the error carries the Engine's own answer
	}{
		{"the Engine is unreachable", dockerapi.ErrUnavailable, errcode.DockerEngineUnavailable},
		{"the Engine answers 500", thAPIError(500, "boom"), ""},
	}
	for _, tc := range tests {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" at "+stage, func(t *testing.T) {
				h, n := paHost(t)
				h.docker.Fail("InspectContainer", tc.err)
				var err error
				if stage == "plan" {
					_, err = n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, paInput("a.example.com")))
				} else {
					_, err = paApply(t, h, n, "c1", "", paInput("a.example.com"))
				}
				if err == nil {
					t.Fatal("the read failure was swallowed")
				}
				if core.IsDefinite(err) {
					t.Errorf("error %v is marked Definite, though a failed read refuses nothing", err)
				}
				var coded *errcode.Error
				var answer *dockerapi.APIError
				switch {
				case tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code):
					t.Errorf("error %v, want code %s", err, tc.code)
				case tc.code == "" && !errors.As(err, &answer):
					t.Errorf("error %v does not carry the Engine's answer", err)
				}
				if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
					t.Error("a failed read created a proxy host or journaled an effect")
				}
			})
		}
	}
}

// TestW10NvAddRefusesASecondAdminExposure: while one exposure of NPM's admin UI is recorded, another npm.admin.expose
// is refused with npm.reserved naming the hostname already exposed, and nothing is created for it. The panel's own
// exposure is not an admin exposure, and the refusal ends with the exposure it names.
func TestW10NvAddRefusesASecondAdminExposure(t *testing.T) {
	ctx := context.Background()
	admin := func(domain string) ProxyHostInput {
		return ProxyHostInput{Domains: []string{domain}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81}
	}
	h, n := paHost(t)
	if _, err := paApply(t, h, n, "c1", paAdmin, admin("npm.example.com")); err != nil {
		t.Fatal(err)
	}
	effects, reserved := len(h.journal.effects()), reservedCount(t, h)

	for _, stage := range []string{"plan", "apply"} {
		t.Run("a second admin exposure at "+stage, func(t *testing.T) {
			var err error
			if stage == "plan" {
				_, err = n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", paAdmin), paJSON(t, admin("npm2.example.com")))
			} else {
				_, err = paApply(t, h, n, "c2", paAdmin, admin("npm2.example.com"))
			}
			coded := paRefused(t, err, errcode.NPMReserved)
			if !strings.Contains(coded.Detail, "npm.example.com") {
				t.Errorf("detail %q does not name the hostname already exposed", coded.Detail)
			}
			if len(h.npm.ProxyHosts()) != 1 || len(h.journal.effects()) != effects || reservedCount(t, h) != reserved {
				t.Error("a refused exposure created a proxy host, journaled an effect or reserved an object")
			}
		})
	}

	t.Run("the panel's exposure is allowed", func(t *testing.T) {
		panel := ProxyHostInput{Domains: []string{"panel.example.com"}, ForwardScheme: "http", ForwardHost: "172.31.255.1", ForwardPort: 9443}
		if _, err := paApply(t, h, n, "c3", paPanel, panel); err != nil {
			t.Errorf("the panel exposure was refused: %v", err)
		}
	})

	t.Run("a released exposure allows another", func(t *testing.T) {
		err := h.db.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, "npm.example.com", paAdmin) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := paApply(t, h, n, "c4", paAdmin, admin("npm2.example.com")); err != nil {
			t.Errorf("the admin exposure was refused after the first was released: %v", err)
		}
	})
}

// TestW10NvInverseReleasesOnlyItsOwnReservations: the undo of one add drops the reservations that add made, and
// keeps those other exposures hold, even when they were recorded under the same change.
func TestW10NvInverseReleasesOnlyItsOwnReservations(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "c", Provider: "letsencrypt", DomainNames: []string{"npm.example.com"}})
	in := ProxyHostInput{Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81,
		CertificateID: cert.ID, SSLForced: true}
	out, err := paApply(t, h, n, "c1", paAdmin, in)
	if err != nil {
		t.Fatal(err)
	}
	others := []reservedRow{
		{Kind: "proxy_host", ObjectID: out.ID + 100, Owner: paPanel, Exposure: "panel.example.com", ChangeID: "c1"},
		{Kind: "certificate", ObjectID: cert.ID + 100, Owner: paPanel, Exposure: "panel.example.com", ChangeID: "c1"},
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range others {
			if err := addReserved(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.proxyhostAdd().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reservedOf(ctx, h.db.Read(), "proxy_host", out.ID); ok {
		t.Error("the undone host is still reserved")
	}
	if _, ok, _ := reservedOf(ctx, h.db.Read(), "certificate", cert.ID); ok {
		t.Error("the certificate the undone add reserved is still reserved")
	}
	for _, r := range others {
		if _, ok, _ := reservedOf(ctx, h.db.Read(), r.Kind, r.ObjectID); !ok {
			t.Errorf("the undo dropped the reservation of %s %d, which another exposure holds", r.Kind, r.ObjectID)
		}
	}
}
```

**1b. `proxyhost_edit_test.go`.** Extend the test `proxyhost_edit_test.go::TestPuUpdateRefusals`: add these two rows to its `tests` table, after the row `"the admin port of NPM as upstream"`. The zone row passes before the fix; the literal row fails.

```go
		{"a forward host with an IPv6 zone", func(_ *testing.T, _ *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			in := puInput(func(in *ProxyHostInput) { in.ForwardHost = "fe80::1%eth0" })
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: in}
		}, errcode.InvalidInput},
		{"an IPv6 literal as forward host", func(_ *testing.T, _ *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			in := puInput(func(in *ProxyHostInput) { in.ForwardHost = "2001:db8::1" })
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: in}
		}, errcode.InvalidInput},
```

Append to `internal/modules/npm/proxyhost_edit_test.go` (its imports already hold everything these use):

```go
// TestW10NvRemoveTreatsAMissingHostAsDeleted: NPM answering 404 to the delete means the host is gone, which is what the
// removal wants, so the effect is made, the reservation is released and no refusal is raised.
func TestW10NvRemoveTreatsAMissingHostAsDeleted(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput())
	h.addChange("c1", "npm.proxyhost.remove")
	puReserve(t, h, "proxy_host", host.ID, "npm.admin.expose", puDomain)
	n := puFaulty(h, &puFaults{remove: puFault{err: &npmapi.APIError{Status: 404, Message: "Not Found"}}})

	out, err := n.proxyhostRemove().Handler.Apply(ctx, h.child("c1", "npm.admin.unexpose"), puJSON(t, ProxyHostRemoveInput{ID: host.ID}))
	if err != nil {
		t.Fatalf("the removal of a host NPM no longer holds failed: %v", err)
	}
	var view ProxyHostView
	if err := json.Unmarshal(out, &view); err != nil || view.ID != host.ID {
		t.Errorf("view %+v (%v), want the host that was removed", view, err)
	}
	if effects := puEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectMade {
		t.Errorf("journal %+v, want one made effect, not a skipped one", effects)
	}
	if puReserved(t, h, "proxy_host", host.ID) {
		t.Error("the reservation of the removed host was not released")
	}
}

// TestW10NvRemoveByIDOfAnAbsentHostReleasesItsOwnersRecords: a removal by id that was cut off after NPM deleted the host
// is finished by running it again, as one by domain is; only the owner's removal releases the records.
func TestW10NvRemoveByIDOfAnAbsentHostReleasesItsOwnersRecords(t *testing.T) {
	ctx := context.Background()
	h, _ := puSeed(t, puInput())
	h.addChange("c1", "npm.proxyhost.remove")
	puReserve(t, h, "proxy_host", 4242, "npm.admin.expose", "npm.example.com")
	puReserve(t, h, "certificate", 77, "npm.admin.expose", "npm.example.com")
	puReserve(t, h, "proxy_host", 4343, "panel.exposure.add", "panel.example.com")
	def := h.nm().proxyhostRemove()
	raw := puJSON(t, ProxyHostRemoveInput{ID: 4242})

	if _, err := def.Handler.Apply(ctx, h.child("c1", "panel.exposure.remove"), raw); err != nil {
		t.Fatal(err)
	}
	if !puReserved(t, h, "proxy_host", 4242) || !puReserved(t, h, "certificate", 77) {
		t.Fatal("a removal under panel.exposure.remove released records that npm.admin.expose owns")
	}
	if _, err := def.Handler.Apply(ctx, h.child("c1", "npm.admin.unexpose"), raw); err != nil {
		t.Fatal(err)
	}
	if puReserved(t, h, "proxy_host", 4242) || puReserved(t, h, "certificate", 77) {
		t.Error("the owner's removal by id did not release its records")
	}
	if !puReserved(t, h, "proxy_host", 4343) {
		t.Error("the removal released the records of another owner")
	}
}
```

**1c. `network_claims_test.go`.** Append to `internal/modules/npm/network_claims_test.go` (its imports already hold everything these use):

```go
// TestW10NvUndoRepeatedAfterALostResponse: the undo of a connection or a disconnection that an earlier run already made
// is done, not refused; a refusal while the endpoint is still as it was stays a refusal.
func TestW10NvUndoRepeatedAfterALostResponse(t *testing.T) {
	ctx := context.Background()

	t.Run("a reconnect repeated is done", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		var endpoint core.Effect
		for _, e := range h.journal.effects() {
			if e.Kind == core.EffectModify {
				endpoint = e
			}
		}
		for i := 1; i <= 2; i++ {
			res, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
			if err != nil {
				t.Fatalf("inverse %d: %v", i, err)
			}
			if res.Resource != "npm.endpoint:appnet" {
				t.Errorf("inverse %d names %q, want npm.endpoint:appnet", i, res.Resource)
			}
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
	})

	t.Run("a disconnect repeated is done", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		endpoint := h.journal.effects()[0]
		for i := 1; i <= 2; i++ {
			if _, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
				t.Fatalf("inverse %d: %v", i, err)
			}
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM is still on the network")
		}
	})

	t.Run("a refused reconnect stays refused", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		var endpoint core.Effect
		for _, e := range h.journal.effects() {
			if e.Kind == core.EffectModify {
				endpoint = e
			}
		}
		h.docker.Fail("ConnectNetwork", thAPIError(403, "forbidden"))
		_, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal while NPM is not on the network", err)
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM joined the network despite the refusal")
		}
	})

	t.Run("a refused disconnect stays refused", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		h.docker.Fail("DisconnectNetwork", thAPIError(403, "forbidden"))
		_, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0])
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal while NPM is on the network", err)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM left the network despite the refusal")
		}
	})
}

// TestW10NvAttachFollowsEveryClaimsPreexistingMark: when the claims on a network disagree on whether NPM was there
// before the first claim, a new claim takes the safe answer, that it was, so a later detach never removes NPM from a
// network it may have been on.
func TestW10NvAttachFollowsEveryClaimsPreexistingMark(t *testing.T) {
	h, user := nwSetup(t)
	nwConnect(t, h, user, nil)
	nwClaim(t, h, nwNet, "a-claimant", "c0", false)
	nwClaim(t, h, nwNet, "b-claimant", "c0", true)

	out, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Preexisting {
		t.Errorf("attachment %+v, want Preexisting, as the claim of b-claimant says", out)
	}
	for _, c := range nwClaims(t, h, nwNet) {
		if c.Claimant == nwClaimant && !c.Preexisting {
			t.Errorf("the new claim %+v was recorded as not preexisting", c)
		}
	}
}
```

**1d. `accesslist_test.go`.** Extend the test `accesslist_test.go::TestAlAddPlanRefusesInvalidInput`: add this row to its table, after the row `"not an address"`. It passes before the fix and pins the zone refusal.

```go
		{"an address with an IPv6 zone", AccessListInput{Clients: []AccessClientInput{{Address: "fe80::1%eth0", Directive: "allow"}}}, "/clients/0/address"},
```

Extend the test `accesslist_test.go::TestAlAddVerifyFindsTheListFromTheChangeAlone`: its last block pinned the behaviour this task removes. Replace the last statement, `h.npm.SeedAccessList(...)` and the `if err := handler.Verify(...); err == nil { ... }` block after it, with:

```go
	h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-9", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
	if err := handler.Verify(ctx, env, alInput(), AccessListView{}); err != nil {
		t.Errorf("Verify with the zero output failed although one of the change's two lists holds the rules: %v", err)
	}
```

That block fails before the fix: two lists carry the change id and Verify wants exactly one.

Append to `internal/modules/npm/accesslist_test.go` (its imports already hold everything these use):

```go
// TestW10NvAddVerifyReadsOnlyTheChangesLists: with no id in the output, Verify looks at the lists named
// darkpanel-<change id>-<sequence number>, no others, and wants one of them to hold the requested rules.
func TestW10NvAddVerifyReadsOnlyTheChangesLists(t *testing.T) {
	ctx := context.Background()
	rules := []npmapi.AccessClient{{Address: "10.0.0.0/8", Directive: "allow"}, {Address: "10.9.0.5", Directive: "deny"}}

	t.Run("no list of the change holds the rules", func(t *testing.T) {
		h, n := alSetup(t)
		h.addChange("c1", "npm.accesslist.add")
		env := h.env("c1")
		handler := accessListAddHandler{n: n}
		if _, err := handler.Apply(ctx, env, alInput()); err != nil {
			t.Fatal(err)
		}
		h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-9", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
		other := AccessListInput{Clients: []AccessClientInput{{Address: "192.0.2.1", Directive: "allow"}}}
		if err := handler.Verify(ctx, env, other, AccessListView{}); err == nil {
			t.Error("Verify passed for rules none of the change's lists holds")
		}
	})

	t.Run("a list of another change whose id extends this one", func(t *testing.T) {
		h, n := alSetup(t)
		h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-2-1", Clients: rules})
		handler := accessListAddHandler{n: n}
		if err := handler.Verify(ctx, h.env("c1"), alInput(), AccessListView{}); err == nil {
			t.Error("Verify of change c1 passed on the list darkpanel-c1-2-1, which belongs to change c1-2")
		}
	})
}
```

**1e. `instance_deploy_test.go`.** Extend the test `instance_deploy_test.go::TestDpPlan`, subtest `emails`: add `"ops@127.1", "ops@1.2.3", "ops@mail.123"` to the list of refused emails, after `"ops@router.home.arpa"`. They fail before the fix: none is an IP address or a reserved name, and each holds a dot.

Extend the same test, subtest `ports`: add these two rows to the table that holds `"nothing listens"`, after the row `"both"`. `sock` is the closure defined at the top of the subtest.

```go
			{"the same port on both address families", []netstat.Socket{
				sock(netstat.ProtoTCP, 80, netstat.StateListen, "nginx"),
				{Proto: netstat.ProtoTCP, Local: netip.AddrPortFrom(netip.IPv6Unspecified(), 80), State: netstat.StateListen,
					Processes: []netstat.Process{{PID: 7, Comm: "nginx"}}},
			}, []string{"80/tcp is in use by nginx"}},
			{"a port whose first listener has no process", []netstat.Socket{
				sock(netstat.ProtoTCP, 80, netstat.StateListen, ""),
				sock(netstat.ProtoTCP, 80, netstat.StateListen, "nginx"),
			}, []string{"80/tcp is in use by nginx"}},
```

Extend the test `instance_deploy_test.go::TestDpApplyRefuses`: add this subtest after `"an Engine object created since the plan"`.

```go
	t.Run("an Engine object created during the image pull", func(t *testing.T) {
		h := newTHHost(t)
		n := h.nm()
		n.d.docker = &w10nvPullDocker{thDocker: h.docker, during: func() {
			h.docker.putVolume(dockerapi.Volume{Name: certVolume})
		}}
		_, err := dpApply(h, n, "c1")
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.DockerInUse {
			t.Errorf("error %v, want a Definite docker.in_use", err)
		}
		if got := dpMutated(h); !slices.Equal(got, []string{"PullImage"}) {
			t.Errorf("Engine changes %v, want the pull alone: nothing may be created or adopted after it", got)
		}
		if len(h.journal.effects()) != 0 {
			t.Errorf("effects %+v, want none", h.journal.effects())
		}
	})
```

Append to `internal/modules/npm/instance_deploy_test.go` the helper that subtest uses:

```go
// w10nvPullDocker is the test Engine whose image pull runs a hook before it answers, as another client acts while the
// image downloads.
type w10nvPullDocker struct {
	*thDocker
	during func()
}

func (d *w10nvPullDocker) PullImage(ctx context.Context, ref string) (pullStream, error) {
	s, err := d.thDocker.PullImage(ctx, ref)
	d.during()
	return s, err
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w10a-tmp/13 && export TMPDIR=/root/.cache/w10a-tmp/13 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 300 go test -count=1 -run 'TestW10Nv|TestPaValidate|TestPuUpdateRefusals|TestAlAddPlanRefusesInvalidInput|TestAlAddVerifyFindsTheListFromTheChangeAlone|TestDpPlan|TestDpApplyRefuses' ./internal/modules/npm/
```

Expected: FAIL, with a failed assertion in each of these (none is a build failure, because the tests use only names that exist):

- `TestW10NvHostProblemSaysWhyAHostIsRefused`: IPv6 hosts return no problem.
- `TestW10NvAddEngineReadFailureIsNotARefusal`: the error is marked Definite.
- `TestW10NvAddRefusesASecondAdminExposure`: the second exposure is created.
- `TestW10NvInverseReleasesOnlyItsOwnReservations`: the other reservations are dropped.
- `TestW10NvRemoveTreatsAMissingHostAsDeleted`: the 404 becomes a Definite `request.invalid_input` and the effect is skipped.
- `TestW10NvRemoveByIDOfAnAbsentHostReleasesItsOwnersRecords`: the records stay.
- `TestW10NvUndoRepeatedAfterALostResponse`, subtests `a reconnect repeated is done` and `a disconnect repeated is done`: the second undo is refused.
- `TestW10NvAttachFollowsEveryClaimsPreexistingMark`: the attachment is not preexisting.
- `TestW10NvAddVerifyReadsOnlyTheChangesLists`, subtest `a list of another change whose id extends this one`.
- `TestPaValidate` rows `an IPv6 literal host` and `the IPv6 loopback host`; `TestPuUpdateRefusals` row `an IPv6 literal as forward host`; `TestAlAddVerifyFindsTheListFromTheChangeAlone`; `TestDpPlan` subtests `emails` and `ports` (two new rows); `TestDpApplyRefuses` subtest `an Engine object created during the image pull`.

These pass before the fix and pin refusals already in place: the zone rows of `TestPaValidate` and `TestPuUpdateRefusals`, the zone row of `TestAlAddPlanRefusesInvalidInput`, the two refused-stays-refused subtests of `TestW10NvUndoRepeatedAfterALostResponse`, and the first subtest of `TestW10NvAddVerifyReadsOnlyTheChangesLists`. For the access-list zone row, prove it can fail: temporarily delete the `a.Zone() != ""` branch of `accessListRules`, see that row fail, restore the branch. Quote the RED output of the failing tests verbatim in the report.

- [ ] **Step 3: Implement**

1. `proxyhostAddHostProblem` in `proxyhost_add.go`: keep the zone refusal first. After it, refuse every address that `netip.Addr.Is6()` reports (IPv4-mapped literals included) with the problem `forwardHost must not be an IPv6 address: Nginx Proxy Manager writes it into the nginx configuration without brackets`. IPv4 addresses and names stay accepted. `validateProxyHostInput` already calls this function for both add and update, so no other validation changes.
2. `checkUpstream` in `proxyhost_add.go`: return a failed Engine read as `engineCoded(err)` itself, not wrapped in `fmt.Errorf`, as its doc comment already says. `proxyhostAddHandler.check` and `proxyHostUpdateHandler.check` return what `checkUpstream` returns without wrapping it in `core.Definite`: a refusal from `proxy.CheckUpstream` is already Definite inside `checkUpstream`, and an Engine failure is not a refusal. `proxyHostRemoveHandler.Inverse` keeps its `core.Definite` around `checkUpstream`, because an undo that read nothing and changed nothing must leave its effect made, not unresolved.
3. Refusal of a second admin exposure, in `proxyhostAddHandler.check` of `proxyhost_add.go`, after the upstream check and before the NPM client is opened, so the plan and the apply both refuse before anything is created. When `env.Call.ParentAction` is `proxyhostAddAdminOwner`, read the reservations with `env.DB.Read()`: `SELECT exposure FROM npm_reserved WHERE owner = ? AND kind = 'proxy_host' ORDER BY exposure LIMIT 1`, with `proxyhostAddAdminOwner` as the argument. A row means the admin UI is already exposed: return `core.Definite(errcode.New(errcode.NPMReserved, ...))` whose detail says that NPM's admin UI is already exposed on that hostname and names it. A failed read is returned as a Definite error with the `npm:` prefix. Do not call `statusAdminExposure`: another task changes that file.
4. `proxyhostAddHandler.Inverse`: after deleting the host (or finding it gone), release only what this add reserved. In one `env.DB.Write`: read the reservation of the proxy host id with `reservedOf(ctx, tx, "proxy_host", id)`; when there is none, or its `ChangeID` is not `e.ChangeID`, release nothing. Otherwise delete that row, and delete the `certificate` rows with the same `change_id` and the same `exposure` as that row (an add reserves its certificate under the host's exposure). No other row of the change is touched. The existing "already gone is done" behaviour stays: a second inverse finds no reservation and succeeds.
5. `proxyHostRemoveHandler.Apply` in `proxyhost_edit.go`: in the closure given to `h.n.run`, an `npmapi.IsNotFound` error from `DeleteProxyHost` returns `resource, nil` (the host is gone, which is the goal) instead of going through `proxyhostAddAnswer`; every other error keeps its mapping.
6. `proxyHostRemoveHandler.target` and `Apply` in `proxyhost_edit.go`: when the removal names an id (`in.ID > 0`) and NPM no longer holds the host, read `reservedOf(ctx, env.DB.Read(), "proxy_host", in.ID)` and keep the row in the target. In `Apply`'s absent-host branch, for an id removal, release `proxyHostEditRelease(ctx, tx, row.Exposure, row.Owner)` only when the row exists and `env.Call.ParentAction` is `proxyHostEditRemoval[row.Owner]`; any other caller releases nothing and raises no refusal. The removal by domain keeps its present behaviour.
7. `networkClaims.networkUndoDisconnect` in `network_claims.go`: when `ConnectNetwork` fails with a `refused` error, inspect the NPM container; when it exists and `c.NetworkSettings.Networks[network]` is not nil, the reconnect is already done: return `InverseResult{Resource: resourceOf(nounEndpoint, network)}, nil`. Any other failure, and a refusal while the container is not on the network, keeps today's mapping. A failed inspect keeps today's mapping too.
8. `networkClaims.networkUndoConnect` in `network_claims.go`: the same rule mirrored. After a `refused` error from `DisconnectNetwork`, inspect the container; when it does not exist, or `Networks[network]` is nil, return nil. Otherwise keep today's Definite refusal.
9. `networkAttachHandler.check` in `network_claims.go`: the `preexisting` mark of an attach is `true` when any claim of the network carries `Preexisting`, or, with no claims, when NPM is already on the network. A claimant that already holds a claim keeps its own mark, as before.
10. `accessListAddHandler.Verify` in `accesslist.go`, the branch without an id in the output: the change's lists are those whose name is `darkpanel-<change id>-` followed by one or more digits and nothing else. Verify passes when at least one of them has the input's `SatisfyAny` and exactly its rules (`slices.Equal` on the clients); otherwise it returns an error that says how many lists of the change it read and that none holds the requested mode and rules. The branch with an id is unchanged.
11. `checkAcmeEmail` in `instance_deploy.go`: after the IP-literal and dot checks, refuse a domain whose last label does not start with a letter (`a`-`z`; the domain is lower-cased already), with the problem `acmeEmail's domain <domain> is not a public domain: its last label must start with a letter`. The refusal stays the existing Definite `request.invalid_input` at `/acmeEmail`.
12. `deployHandler.checkPorts` in `instance_deploy.go`: report each of 80 and 443 at most once, in the order each port first appears in the socket list. The entry's process is that of the first listening socket on the port that has a process, else `an unknown process`. The text of an entry and the `; ` join stay as they are.
13. `deployHandler.Apply` in `instance_deploy.go`: call `h.checkEngine(ctx)` again right after `h.ensureImage(ctx)` and before the password is generated, so an object another client created while the image downloaded is refused as `docker.in_use` instead of being adopted and later deleted by the undo.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w10a-tmp/13 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestW10Nv|TestPaValidate|TestPuUpdateRefusals|TestAlAddPlanRefusesInvalidInput|TestAlAddVerifyFindsTheListFromTheChangeAlone|TestDpPlan|TestDpApplyRefuses' ./internal/modules/npm/
timeout 600 go test -count=1 ./internal/modules/npm/...
```

Expected: both `ok`. If a test elsewhere in the package asserted the old wrapped message of an Engine read failure or the old exactly-one-list wording, change that assertion to the new behaviour and say so in the report.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w10a-tmp/13 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short schemas docs/api
rm -rf /root/.cache/w10a-tmp/13 && go clean -testcache
```

Expected: build and vet print nothing, every test line is `ok` or `?`, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing, and `git status --short schemas docs/api` prints nothing (this task changes no wire type). Record the real output.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm/proxyhost_add.go internal/modules/npm/proxyhost_edit.go internal/modules/npm/network_claims.go \
  internal/modules/npm/accesslist.go internal/modules/npm/instance_deploy.go internal/modules/npm/proxyhost_add_test.go \
  internal/modules/npm/proxyhost_edit_test.go internal/modules/npm/network_claims_test.go internal/modules/npm/accesslist_test.go \
  internal/modules/npm/instance_deploy_test.go
git commit -m "fix(npm): tighten proxy, claim and list checks"
```

### Task 14: NPM status reads and admin exposures

**Files:**
- Modify: `internal/modules/npm/instance_status.go` (`statusReader.Read`)
- Modify: `internal/modules/npm/deps.go` (`apiVia`)
- Modify: `internal/modules/npm/effects.go` (`found`)
- Modify: `internal/modules/npm/store.go` (`saveInstance`, `insertUpgrade`, `setUpgradePhase`)
- Test: `internal/modules/npm/instance_status_test.go`
- Test: `internal/modules/npm/deps_test.go`
- Test: `internal/modules/npm/effects_test.go`
- Test: `internal/modules/npm/store_test.go`

**Interfaces:**
- Consumes: the test host of `testhost_test.go` (`newTHHost`, `thDocker`) and the helpers of the four test files above, all on `main`.
- Produces: nothing another task consumes. `statusAdminExposure(ctx context.Context, q queryer) (*AdminExposure, error)` keeps its signature, and `storeTime` and `parseStoreTime` keep their behaviour (the zero time is still `""`), so other files keep compiling. No wire type changes.

**Items:** 345, 346

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

Row 336 (a second `npm.admin.expose` is refused with `npm.reserved`, the detail naming the existing hostname) is carried out by Task 13, in `proxyhost_add.go`. The composite's `Steps` may read only its input and the controller database, never the host, and the reservations live in the agent's database; the agent-side `npm.proxyhost.add` step is where they can be read before anything is created. This task therefore leaves `composite_admin.go` and its test as they are.

- [ ] **Step 1: Write the failing test**

**1a. `deps_test.go`.** Add `"github.com/darkraise/darkpanel/internal/modules/npm/fake"` to its imports. Extend the test `deps_test.go::TestSdAPIVia`: add this subtest at its end.

```go
	t.Run("the wording of a failed login follows its status", func(t *testing.T) {
		for _, tc := range []struct {
			status  int
			refused bool
		}{{401, true}, {500, false}} {
			h := newTHHost(t)
			h.deployed()
			h.npm.Fail("POST", "/api/tokens", fake.Fault{Status: tc.status})
			_, err := h.nm().d.api(ctx, h.env(""))
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
				t.Fatalf("status %d: error %v, want %s", tc.status, err, errcode.NPMUnhealthy)
			}
			if want := fmt.Sprintf("(HTTP %d)", tc.status); !strings.Contains(coded.Detail, want) {
				t.Errorf("status %d: detail %q does not name %s", tc.status, coded.Detail, want)
			}
			if got := strings.Contains(coded.Detail, "refused"); got != tc.refused {
				t.Errorf("status %d: detail %q says refused: %t, want %t; only a 4xx is a refused login", tc.status, coded.Detail, got, tc.refused)
			}
		}
	})
```

The 401 row passes before the fix; the 500 row fails.

**1b. `effects_test.go`.** Add `"slices"` to its imports. Append to `internal/modules/npm/effects_test.go`:

```go
// TestW10NsFoundLeavesTheCallersSliceAlone: found orders the candidates it reports, not the slice the caller passed.
func TestW10NsFoundLeavesTheCallersSliceAlone(t *testing.T) {
	second := core.Candidate{Resource: "npm.proxyhost:2"}
	first := core.Candidate{Resource: "npm.proxyhost:1"}
	candidates := []core.Candidate{second, first}
	obs := found(candidates)
	resources := func(cs []core.Candidate) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Resource
		}
		return out
	}
	if got := resources(candidates); !slices.Equal(got, []string{"npm.proxyhost:2", "npm.proxyhost:1"}) {
		t.Errorf("the caller's slice is now %v, want it in the order it was passed", got)
	}
	if got := resources(obs.Candidates); obs.Matches != 2 || !slices.Equal(got, []string{"npm.proxyhost:1", "npm.proxyhost:2"}) {
		t.Errorf("observation %+v, want two matches reported in resource order", obs)
	}
}
```

**1c. `store_test.go`.** Append to `internal/modules/npm/store_test.go` (its imports already hold everything this uses):

```go
// TestW10NsWritersRefuseAZeroTime: a time column holds a real time. storeTime turns the zero time into "", which the
// NOT NULL columns accept, so the writers refuse a zero time before it is stored.
func TestW10NsWritersRefuseAZeroTime(t *testing.T) {
	ctx := context.Background()
	row := instanceRow{ContainerID: "c1", Image: "img1", AdminEmail: "ops@darkpanel.dev", PasswordCT: []byte{1}, Public: "no", CreatedAt: thStart, UpdatedAt: thStart}
	for name, mutate := range map[string]func(*instanceRow){
		"no created time": func(r *instanceRow) { r.CreatedAt = time.Time{} },
		"no updated time": func(r *instanceRow) { r.UpdatedAt = time.Time{} },
	} {
		t.Run("instance with "+name, func(t *testing.T) {
			h := newTHHost(t)
			r := row
			mutate(&r)
			err := h.db.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, r) })
			if err == nil {
				t.Fatal("saveInstance stored a zero time")
			}
			if _, ok, _ := loadInstance(ctx, h.db.Read()); ok {
				t.Error("a refused save left an instance row")
			}
		})
	}
	t.Run("an upgrade with no time", func(t *testing.T) {
		h := newTHHost(t)
		err := insertUpgrade(ctx, h.db, upgradeRow{ChangeID: "c1", FromImage: "img1", ToImage: "img2", Config: containerConfig{Name: containerName}})
		if err == nil {
			t.Fatal("insertUpgrade stored a zero time")
		}
		if _, ok, _ := loadUpgrade(ctx, h.db.Read(), "c1"); ok {
			t.Error("a refused insert left an upgrade row")
		}
	})
	t.Run("a phase change with no time", func(t *testing.T) {
		h := newTHHost(t)
		u := upgradeRow{ChangeID: "c1", FromImage: "img1", ToImage: "img2", Config: containerConfig{Name: containerName}, UpdatedAt: thStart}
		if err := insertUpgrade(ctx, h.db, u); err != nil {
			t.Fatal(err)
		}
		if err := setUpgradePhase(ctx, h.db, "c1", "archived", "", nil, time.Time{}); err == nil {
			t.Fatal("setUpgradePhase stored a zero time")
		}
		got, _, _ := loadUpgrade(ctx, h.db.Read(), "c1")
		if got.Phase != "recorded" || !got.UpdatedAt.Equal(thStart) {
			t.Errorf("after a refused change the upgrade is %+v, want phase recorded at %v", got, thStart)
		}
	})
}
```

**1d. `instance_status_test.go`.** Append to `internal/modules/npm/instance_status_test.go` (its imports already hold everything these use):

```go
// w10nsHookDocker is the test Engine that runs onInspect before every container inspection, so a test can act as
// another request or fail the call.
type w10nsHookDocker struct {
	*thDocker
	onInspect func() error
}

func (d *w10nsHookDocker) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	if err := d.onInspect(); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	return d.thDocker.InspectContainer(ctx, id)
}

// TestW10NsStatusReadsTheStoreBeforeTheEngine: the instance row and the admin exposure come from one read of the
// store, made before any Engine or NPM call, so a change another request makes while the Engine answers cannot give
// a status whose two parts belong to different moments.
func TestW10NsStatusReadsTheStoreBeforeTheEngine(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	h.deployed()
	stReserve(t, h, "proxy_host", 5, "npm.admin.expose", "npm.example.com")
	n := h.nm()
	n.d.docker = &w10nsHookDocker{thDocker: h.docker, onInspect: func() error {
		err := h.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "DELETE FROM npm_reserved")
			return err
		})
		if err != nil {
			t.Error(err)
		}
		return nil
	}}
	st := stRead(t, h, n)
	if st.AdminExposure == nil || st.AdminExposure.Hostname != "npm.example.com" {
		t.Errorf("admin exposure %+v, want the one the store held when the read began", st.AdminExposure)
	}
}

// TestW10NsStatusReturnsACancellation: a request that ended is an error of the read, not an instance that does not run
// or an API that is not ready.
func TestW10NsStatusReturnsACancellation(t *testing.T) {
	t.Run("the Engine call", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		n := h.nm()
		n.d.docker = &w10nsHookDocker{thDocker: h.docker, onInspect: func() error { return context.Canceled }}
		_, err := n.instanceStatus().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want context.Canceled", err)
		}
	})
	t.Run("the probe of NPM", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		n := h.nm()
		probe := &stHealth{npmAPI: n.d.anon(), err: context.Canceled}
		n.d.anon = func() npmAPI { return probe }
		_, err := n.instanceStatus().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want context.Canceled", err)
		}
	})
}
```

Also in `instance_status_test.go`, `TestStStatusAdminExposure`: remove the field `other bool` from the anonymous struct of its table and the trailing `false` of each row; the field is never read. This edit passes before and after the change.

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w10a-tmp/14 && export TMPDIR=/root/.cache/w10a-tmp/14 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 300 go test -count=1 -run 'TestW10Ns|TestSdAPIVia|TestStStatusAdminExposure' ./internal/modules/npm/
```

Expected: FAIL in `TestSdAPIVia` (the 500 row: the detail says the login was refused), `TestW10NsFoundLeavesTheCallersSliceAlone` (the caller's slice is sorted), `TestW10NsWritersRefuseAZeroTime` (each case stores the zero time), `TestW10NsStatusReadsTheStoreBeforeTheEngine` (the exposure is nil) and both subtests of `TestW10NsStatusReturnsACancellation` (the status is returned without an error). The 401 row and the `other` field removal pass. Quote the RED output verbatim.

- [ ] **Step 3: Implement**

1. `statusReader.Read` in `instance_status.go`: read the stored state first, in one read transaction on `env.DB.Read()` (`BeginTx(ctx, nil)`, as `internal/modules/tailnet/account_actions.go` does), released before any Engine or NPM call. In that transaction call `loadInstance` and, when an instance is deployed, `statusAdminExposure` (its signature stays), both with the transaction as the `queryer`. A missing instance still returns `InstanceStatus{PinnedImage: Image, Public: "no"}` without reading the exposure or calling the Engine. Put the transaction in a small unexported helper so it is rolled back on every path.
2. After the transaction: inspect the container, then probe NPM, as today. When the inspection or the probe fails and either `ctx.Err()` is not nil or the error is `context.Canceled`, return that error from `Read`. Any other failure keeps reading as not running or not ready. Update the doc comment of `Read` to say a cancelled request is an error.
3. `apiVia` in `deps.go`: for an `*npmapi.APIError` that `npmapi.IsClientError` reports (4xx), keep the detail `NPM refused the stored administrator's login (HTTP %d)`. For any other status use `Nginx Proxy Manager failed while logging in the stored administrator (HTTP %d)`. The code stays `npm.unhealthy` and the cause stays wrapped. No message carries the password.
4. `found` in `effects.go`: sort a clone of `candidates` (`slices.Clone`) and report the clone, so the caller's slice keeps its order. Nothing else about the three cases changes.
5. `store.go`: `saveInstance` returns an error, with the `npm:` prefix and saying the created and updated times must be set, when either time is zero, before it writes. `insertUpgrade` and `setUpgradePhase` do the same for `u.UpdatedAt` and `at`, before they write. `storeTime` and `parseStoreTime` do not change.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w10a-tmp/14 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestW10Ns|TestSdAPIVia|TestStStatusAdminExposure' ./internal/modules/npm/
timeout 600 go test -count=1 ./internal/modules/npm/...
```

Expected: both `ok`. If another test in the package passes a zero time to `saveInstance`, `insertUpgrade` or `setUpgradePhase`, give it a real time and say so in the report.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w10a-tmp/14 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short schemas docs/api
rm -rf /root/.cache/w10a-tmp/14 && go clean -testcache
```

Expected: build and vet print nothing, every test line is `ok` or `?`, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing, and `git status --short schemas docs/api` prints nothing. Record the real output.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm/instance_status.go internal/modules/npm/deps.go internal/modules/npm/effects.go internal/modules/npm/store.go \
  internal/modules/npm/instance_status_test.go internal/modules/npm/deps_test.go internal/modules/npm/effects_test.go internal/modules/npm/store_test.go
git commit -m "fix(npm): harden status, login and store reads"
```

### Task 15: NPM schema bounds

**Files:**
- Modify: `internal/modules/npm/wire.go` (`CheckInput`, `PublicInput`, `DeployPublicInput`, `ExposeInput`, `ExposeRemoveInput`, `AdminExposeInput`, `AdminUnexposeInput`)
- Modify (generated): `schemas/npm.instance.check/v1.json`, `schemas/npm.public.check/v1.json`, `schemas/npm.instance.deploy-public/v1.json`, `schemas/npm.expose.container/v1.json`, `schemas/npm.expose.remove/v1.json`, `schemas/npm.admin.expose/v1.json`, `schemas/npm.admin.unexpose/v1.json`, by `make schemas`
- Modify (generated): `docs/api/npm.md` and `docs/api/openapi.json`, by `make api-docs`
- Test: `internal/modules/npm/register_test.go` (`TestRegInputsRefuseWhatTheWireTypesRequire`)
- Test: `internal/modules/npm/composite_deploy_expose_test.go`

**Interfaces:**
- Consumes: `regRefusal`, `regDefs`, `regModule`, `regSamples` of `register_test.go`, and `cxJSON`, `cxHost` of `composite_deploy_expose_test.go`, all on `main`.
- Produces: the seven `hostId` members now carry `minLength=1` and `ExposeInput.domains` carries `minItems=1`; the committed snapshots under `schemas/npm.*` and the pages under `docs/api/**` are regenerated from them. Task 27 later edits `wire.go` again in another wave and regenerates then.

**Items:** 337, 359

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

This is one of the two tasks of the plan (with Task 27) that change a wire type, so it is the only one of Tasks 13 to 15 that runs `make schemas` and `make api-docs`.

Row 359 has two further findings that this task does not change: `cxResolve` and `caResolve` copy the engine's reference resolver, which is unexported in `internal/core/lifecycle` (a frozen package), so the copy stays; and `composite_admin_test.go` is not this task's file.

- [ ] **Step 1: Write the failing test**

Extend the test `register_test.go::TestRegInputsRefuseWhatTheWireTypesRequire`: add these rows to its table, after the row `"expose without the host"`. Each sets the host id to the empty string, which the schema accepts before the change, so the engine fails later with an internal-class error instead of `request.invalid_input`.

```go
		{"check with an empty host", "npm.instance.check", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"public check with an empty host", "npm.public.check", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"deploy-public with an empty host", "npm.instance.deploy-public", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"expose with an empty host", "npm.expose.container", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"expose remove with an empty host", "npm.expose.remove", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"admin expose with an empty host", "npm.admin.expose", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"admin unexpose with an empty host", "npm.admin.unexpose", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
```

Append to `internal/modules/npm/composite_deploy_expose_test.go` (its imports already hold everything this uses):

```go
// TestW10NbExposeContainerRefusesNoDomains: npm.expose.container with an empty domain list is refused as an invalid
// input at the schema, before Steps would name a claimant from a domain that does not exist.
func TestW10NbExposeContainerRefusesNoDomains(t *testing.T) {
	def := newTHHost(t).nm().exposeContainer()
	v := schema.NewValidator()
	empty := ExposeInput{HostID: cxHost, Container: "web", Port: 8080, Network: "appnet", Domains: []string{}}
	if err := v.ValidateInput(def, cxJSON(t, empty)); !regRefusal(err, "/domains") {
		t.Errorf("input with no domains: %v, want request.invalid_input naming /domains", err)
	}
	one := empty
	one.Domains = []string{"app.example.com"}
	if err := v.ValidateInput(def, cxJSON(t, one)); err != nil {
		t.Errorf("input with one domain: %v, want it accepted", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w10a-tmp/15 && export TMPDIR=/root/.cache/w10a-tmp/15 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 300 go test -count=1 -run 'TestRegInputsRefuseWhatTheWireTypesRequire|TestW10NbExposeContainerRefusesNoDomains' ./internal/modules/npm/
```

Expected: FAIL. Seven lines say `want request.invalid_input naming /hostId` for the new rows, and `TestW10NbExposeContainerRefusesNoDomains` says `input with no domains: <nil>, want request.invalid_input naming /domains`. Quote the output verbatim.

- [ ] **Step 3: Implement**

1. In `wire.go`, give the `HostID` member of `CheckInput`, `PublicInput`, `DeployPublicInput`, `ExposeInput`, `ExposeRemoveInput`, `AdminExposeInput` and `AdminUnexposeInput` the tag `jsonschema:"minLength=1"`, written after its `json` tag as `DeployInput.AcmeEmail`'s is: `` `json:"hostId" jsonschema:"minLength=1"` ``. Give `ExposeInput.Domains` the tag `` `json:"domains" jsonschema:"minItems=1"` ``. No other member, and no output type, changes.
2. Regenerate the committed snapshots and the API pages:

```bash
export TMPDIR=/root/.cache/w10a-tmp/15 GOTOOLCHAIN=local
timeout 300 make schemas
timeout 300 make api-docs
git status --short schemas docs/api
```

Expected: exactly the seven `schemas/npm.*/v1.json` files named under Files, `docs/api/npm.md` and `docs/api/openapi.json` are modified, and nothing else under `schemas/` or `docs/api/`. Each snapshot diff adds only `"minLength": 1` to its `hostId` property, and `schemas/npm.expose.container/v1.json` also adds `"minItems": 1` to `domains`. If any other file changes, stop and report it.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w10a-tmp/15 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestRegInputsRefuseWhatTheWireTypesRequire|TestW10NbExposeContainerRefusesNoDomains|TestRegSnapshotsAreCommitted' ./internal/modules/npm/
timeout 600 go test -count=1 ./internal/modules/npm/... ./internal/core/schema/... ./internal/docs/... ./internal/apidocs/... ./internal/modules/
```

Expected: every line `ok`. `TestAdAPIDocsCurrent` and the snapshot-currency test of `internal/modules` pass because the generated files were committed with the change.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w10a-tmp/15 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/15 && go clean -testcache
```

Expected: build and vet print nothing, every test line is `ok` or `?`, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm/wire.go internal/modules/npm/register_test.go internal/modules/npm/composite_deploy_expose_test.go \
  schemas/npm.instance.check schemas/npm.public.check schemas/npm.instance.deploy-public schemas/npm.expose.container \
  schemas/npm.expose.remove schemas/npm.admin.expose schemas/npm.admin.unexpose docs/api/npm.md docs/api/openapi.json
git commit -m "fix(npm): bound host ids and exposure domains"
```

### Task 16: Cloudflare connector and hostname fixes

**Files:**
- Modify: `internal/modules/cloudflare/connector_run.go` (`connectorRunHandler.precheck`, `removeConnector`)
- Modify: `internal/modules/cloudflare/connector_network.go` (`networkAttachHandler.check`, `networkAttachHandler.Apply`)
- Modify: `internal/modules/cloudflare/hostname_add.go` (`hostnameAddHandler.Apply`)
- Test: `internal/modules/cloudflare/connector_run_test.go`
- Test: `internal/modules/cloudflare/connector_network_test.go`
- Test: `internal/modules/cloudflare/connector_network_detach_test.go`
- Test: `internal/modules/cloudflare/connector_testhost_test.go`
- Test: `internal/modules/cloudflare/hostname_add_test.go`
- Test: `internal/modules/cloudflare/connector_stop_test.go`
- Test: `internal/modules/cloudflare/connector_logsecrets_test.go`
- Test: `internal/modules/cloudflare/register_test.go`

**Interfaces:**
- Consumes: the agent test host of `connector_testhost_test.go` (`agHost`, `agDocker`, `agNetwork`, `agContainer`, `agToken`) and the helpers of the test files above (`newCR`, `crInput`, `crRefused`, `newNA`, `naInput`, `naApply`, `naOnWeb`, `naClaims`, `newND`, `ndAttach`, `ndApply`, `ndClaims`, `ndOn`, `ndEffects`, `ndRefused`, `ndInput`, `cpApply`, `clSeed`), all on `main`.
- Produces: nothing another task consumes. No wire type changes, so `schemas/**` and `docs/api/**` stay as they are.

**Items:** 382, 383

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

Row 382's text predates commit 57e1a14 (`fix(cloudflare): close deferred review minors`), which already closed these findings; this task does not repeat them: `connector_deploy.go` refuses an empty `hostId` and checks the run step's tunnel (`deployOutput`), `connector_network_detach.go` has a Definite prefixed read before the disconnect and a `Verify` that validates its input and counts one claim as one, and `expose.go` says a name starts with a letter or digit and reads both outputs through `exposeStepOutput`. What remains of row 382 is below.

Two findings of row 383 are decided here, not changed. `connector_deps.go` keeps `testing.Testing()`: `docker`, `derp`, `npm`, `panel` and `tailscale` use the same guard, and it is what keeps every test binary that links a module away from the host's Docker socket. `TestClLogSecretsAreRegistered` keeps its shape: `internal/modules/docker` has no way to remove a registration, that package is not this task's, and the leftover entry exists only in a run where the test has already failed. Say both in the report.

- [ ] **Step 1: Write the failing test**

**1a. `connector_run_test.go`.** Append to `internal/modules/cloudflare/connector_run_test.go` (its imports already hold everything these use):

```go
// w10cfDropTable removes a table of the agent database, so the next read of it fails.
func w10cfDropTable(t *testing.T, h *agHost, table string) {
	t.Helper()
	ctx := context.Background()
	err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DROP TABLE "+table)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestW10CfRunRefusesAnUnreadableStore: the plan reads the store before it changes anything, so a failed read is
// Definite, and it carries the module's prefix.
func TestW10CfRunRefusesAnUnreadableStore(t *testing.T) {
	h := newCR(t)
	w10cfDropTable(t, h.agHost, "cloudflare_connectors")
	_, err := h.cfg.connectorRun().Handler.Plan(context.Background(), h.env(""), crInput(crTunnel, agToken(crTunnel)))
	if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "cloudflare: ") {
		t.Errorf("error %v, want a Definite error with the cloudflare: prefix", err)
	}
	if got := crMutated(h); len(got) != 0 {
		t.Errorf("a refused plan changed the Engine: %v", got)
	}
}

// TestW10CfRemoveConnectorRefusesABadTunnel: removeConnector validates the tunnel id it builds names and paths from,
// whoever its caller is, and touches nothing when the id is bad.
func TestW10CfRemoveConnectorRefusesABadTunnel(t *testing.T) {
	ctx := context.Background()
	for _, tunnel := range []string{"", "x", "../" + crTunnel, strings.ToUpper(crTunnel)} {
		t.Run(fmt.Sprintf("%q", tunnel), func(t *testing.T) {
			h := newCR(t)
			removed, err := removeConnector(ctx, h.env("c2"), h.cfg, tunnel)
			coded := crRefused(t, err, errcode.InvalidInput)
			if !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == "/tunnel" }) {
				t.Errorf("fields %+v, want one at /tunnel", coded.Fields)
			}
			if removed || len(h.docker.calls()) != 0 || len(h.rec.removes) != 0 {
				t.Errorf("removed %t, Engine calls %v, removed files %v; want nothing touched", removed, h.docker.calls(), h.rec.removes)
			}
		})
	}
}
```

**1b. `connector_network_test.go`.** Append to `internal/modules/cloudflare/connector_network_test.go` (its imports already hold everything these use):

```go
// TestW10CfAttachRefusesAnUnreadableStore: a failed read of the store before the attach changes anything is Definite,
// as a failed Engine read is, and carries the module's prefix.
func TestW10CfAttachRefusesAnUnreadableStore(t *testing.T) {
	ctx := context.Background()
	for _, table := range []string{"cloudflare_connectors", "cloudflare_attachments"} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(table+" at "+stage, func(t *testing.T) {
				h := newNA(t)
				w10cfDropTable(t, h, table)
				def := h.cf().networkAttach()
				var err error
				if stage == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), naInput())
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), naInput())
				}
				if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "cloudflare: ") {
					t.Errorf("error %v, want a Definite error with the cloudflare: prefix", err)
				}
				if naOnWeb(t, h) {
					t.Error("the connector joined web despite the failed read")
				}
			})
		}
	}
}

// TestW10CfAttachObserveFailure: a failed read of the endpoint before the connect changed nothing, so it is Definite,
// keeps the Engine's code, carries the prefix and journals nothing, as the detach's does.
func TestW10CfAttachObserveFailure(t *testing.T) {
	h := newNA(t)
	h.docker.Fail("InspectContainer", nil)
	h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
	_, err := naApply(t, h, "c1", func(in *NetworkAttachInput) { in.Target = "" })
	if err == nil || !core.IsDefinite(err) || errcode.From(err).Code != errcode.DockerEngineUnavailable || !strings.HasPrefix(err.Error(), "cloudflare: ") {
		t.Errorf("error %v, want a Definite docker.engine_unavailable with the cloudflare: prefix", err)
	}
	if effs := ndEffects(t, h, "c1"); len(effs) != 0 || len(naClaims(t, h)) != 0 || naOnWeb(t, h) {
		t.Errorf("effects %+v, claims %+v, on web %t; want nothing journaled, no claim and no attachment", effs, naClaims(t, h), naOnWeb(t, h))
	}
}
```

**1c. `connector_network_detach_test.go`.** Append to `internal/modules/cloudflare/connector_network_detach_test.go` (its imports already hold everything these use):

```go
// TestW10CfDetachEngineReadFailure: a failed Engine read of a network, whether the claim names it or the input names it
// by id, changed nothing, so it is Definite, keeps the Engine's answer and the module's prefix, and keeps the claim.
func TestW10CfDetachEngineReadFailure(t *testing.T) {
	for _, tc := range []struct{ name, network string }{
		{"a network the claim names", "web"},
		{"a network named by id", "0123456789ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newND(t)
			ndAttach(t, h, "c1", "web", ndClaimant)
			h.docker.Fail("InspectNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
			_, err := ndApply(t, h, "c2", func(in *NetworkDetachInput) { in.Network = tc.network })
			var answer *dockerapi.APIError
			if err == nil || !core.IsDefinite(err) || !errors.As(err, &answer) || answer.StatusCode != http.StatusInternalServerError ||
				!strings.HasPrefix(err.Error(), "cloudflare: inspect network ") {
				t.Errorf("error %v, want a Definite error that wraps the Engine's 500 and starts with cloudflare: inspect network", err)
			}
			if effs := ndEffects(t, h, "c2"); len(effs) != 0 || len(ndClaims(t, h, ndTunnel)) != 1 || !ndOn(t, h, "web") {
				t.Errorf("effects %+v, claims %+v; want nothing journaled and the claim and attachment kept", effs, ndClaims(t, h, ndTunnel))
			}
		})
	}
}

// TestW10CfDetachRefusalDetailJoinsEveryProblem: a refused input names every bad field, and the detail joins their
// messages in the order tunnel, network, claimant.
func TestW10CfDetachRefusalDetailJoinsEveryProblem(t *testing.T) {
	h := newND(t)
	bad := func(in *NetworkDetachInput) { in.Tunnel, in.Network, in.Claimant = "x", "bad name", "a b" }
	_, err := h.cf().networkDetach().Handler.Plan(context.Background(), h.env(""), ndInput(bad))
	coded := ndRefused(t, err, errcode.InvalidInput)
	want := "tunnel must be a lower-case UUID; network must be a Docker network name; claimant must not contain whitespace or control characters"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}
```

The second test passes before the change and pins the joined text. For the first test, the `a network the claim names` case also passes before the change (the detach already marks this read Definite); the `a network named by id` case is the one `detachClaimsNamed` reaches. To prove both pins can fail, temporarily remove the `core.Definite` from `attachEngine` and change the `"; "` separator of `attachInvalid`, see the tests fail, and restore both.

**1d. `connector_testhost_test.go`.** Extend the test `connector_testhost_test.go::TestAgDockerCreatesInspectsAndConnects`: add this block at the end of the function, after the final `calls since the failure` assertion. The test host must answer as the Engine does: a list with no match is an empty list, and the `host` and `none` networks refuse a connection.

```go
	none, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{All: true, Filters: dockerapi.Filters{"label": {"no.such=label"}}})
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("ListContainers with no match = %#v, %v; want an empty list that is not nil, as the Engine's [] decodes", none, err)
	}
	for _, name := range []string{"host", "none"} {
		err := d.ConnectNetwork(ctx, name, app.ID, nil)
		var answer *dockerapi.APIError
		if !errors.As(err, &answer) || answer.StatusCode < 400 || answer.StatusCode > 499 {
			t.Errorf("ConnectNetwork to %s = %v, want a 4xx refusal", name, err)
		}
		if got, _ := d.container(app.ID); got.NetworkSettings.Networks[name] != nil {
			t.Errorf("the container joined the %s network", name)
		}
	}
```

**1e. `hostname_add_test.go`.** Extend the test `hostname_add_test.go::TestRsAddPlanWhenTheServiceHostCannotBeResolved`: replace the subtest `"a timeout is refused"` with this one. The new assertions pass before the change; they pin that the refusal is Definite and keeps the resolver's error as its cause.

```go
	t.Run("a timeout is refused", func(t *testing.T) {
		timeout := &net.DNSError{Err: "i/o timeout", Name: "web.app.test", IsTimeout: true}
		f, l, err := run(t, timeout, nil)
		if got := errcode.From(err); got.Code != errcode.CloudflareReserved || !strings.Contains(got.Detail, "web.app.test") {
			t.Fatalf("plan error = %v, want %s naming the host", err, errcode.CloudflareReserved)
		}
		if !core.IsDefinite(err) {
			t.Errorf("plan error %v is not Definite, though the check changed nothing", err)
		}
		if !errors.Is(err, timeout) {
			t.Errorf("plan error %v does not keep the resolver's error as its cause", err)
		}
		if l.calls != 1 || len(f.writes()) != 0 {
			t.Errorf("%d lookups and writes %v, want one lookup and no write", l.calls, f.writes())
		}
	})
```

**1f. `connector_stop_test.go`.** Extend the test `connector_stop_test.go::TestCpApply`, subtest `"an Engine failure keeps the record and a repeat finishes"`: replace its first failing call, `if _, err := cpApply(t, h, "stop1", cpTunnel); err == nil { t.Fatal("the stop succeeded although the Engine failed") }`, with the block below. It passes before the change and pins that the failure is not Definite, because part of the removal may have happened, and wraps the Engine's answer.

```go
		_, err := cpApply(t, h, "stop1", cpTunnel)
		if err == nil {
			t.Fatal("the stop succeeded although the Engine failed")
		}
		var answer *dockerapi.APIError
		if core.IsDefinite(err) || !errors.As(err, &answer) || answer.StatusCode != 500 {
			t.Errorf("error %v, want a failure that is not Definite and wraps the Engine's 500", err)
		}
```

**1g. `connector_logsecrets_test.go`.** Extend the test `connector_logsecrets_test.go::TestClLogSecretsFailClosed`: its stored value was the literal `"not a sealed value"`, so the check that the error omits the token could never fail. Seal the real token of tunnel A under another tunnel's associated data, so the row holds a genuine ciphertext of the token that cannot be opened for tunnel A. Replace the `err := env.DB.Write(...)` statement and its `if err != nil { t.Fatal(err) }` with:

```go
	sealedForAnother, err := env.Sealer.Seal(connectorSealAD(clTunnelB), []byte(agToken(clTunnelA)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := deleteConnector(ctx, tx, clTunnelA); err != nil {
			return err
		}
		return insertConnector(ctx, tx, connectorRow{TunnelID: clTunnelA, ContainerID: "container-a", TokenCT: sealedForAnother,
			ChangeID: "c0", CreatedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
```

The assertions after it stay. The test passes before and after; it now has a real token behind the failing seal.

**1h. `register_test.go`.** Extend the test `register_test.go::TestRegCompositeSteps`: a step whose action is not registered skips the bookkeeping of which steps it follows, because of the `continue` after `t.Errorf("step %s: %s is not registered", ...)`. Move the block that builds `set` and assigns `follows[s.ID] = set` (the comment `// A step follows the steps of its After list and, as the engine reckons it, ...` through `follows[s.ID] = set`) to just after `ids[s.ID] = i`, before `child, ok := reg.Action(s.Action)`. Leave the `followed` loop, the resolution and the validation after the lookup. This edit changes no result on the current tree; it keeps later steps' follows complete when one step is unregistered.

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w10a-tmp/16 && export TMPDIR=/root/.cache/w10a-tmp/16 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 300 go test -count=1 -run 'TestW10Cf|TestAgDockerCreatesInspectsAndConnects|TestRsAddPlanWhenTheServiceHostCannotBeResolved|TestCpApply|TestClLogSecretsFailClosed|TestRegCompositeSteps' ./internal/modules/cloudflare/
```

Expected: FAIL in `TestW10CfRunRefusesAnUnreadableStore` (the error is not Definite), `TestW10CfRemoveConnectorRefusesABadTunnel` (no refusal, the Engine is called), `TestW10CfAttachRefusesAnUnreadableStore` (not Definite), `TestW10CfAttachObserveFailure` (not Definite) and `TestAgDockerCreatesInspectsAndConnects` (the empty list is nil and the connection to `host` succeeds). `TestW10CfDetachEngineReadFailure`, `TestW10CfDetachRefusalDetailJoinsEveryProblem`, `TestRsAddPlanWhenTheServiceHostCannotBeResolved`, `TestCpApply`, `TestClLogSecretsFailClosed` and `TestRegCompositeSteps` pass, as the notes in Step 1 say. Quote the RED output verbatim.

- [ ] **Step 3: Implement**

1. `connectorRunHandler.precheck` in `connector_run.go`: return the `loadConnector` error wrapped in `core.Definite`; its text already starts with `cloudflare:`. The Engine read of the container keeps its present mapping.
2. `removeConnector` in `connector_run.go`: before any Engine or file call, refuse a tunnel id that `tunnelIDProblem` rejects with `hostnameInvalid("/tunnel", problem)` and return `false` with it.
3. `networkAttachHandler.check` in `connector_network.go`: wrap the errors of `loadConnector` and `connectorClaims` in `core.Definite`, as `detachCheck` does for its reads, and change the doc comment to say every failure of `check` is Definite because `check` changes nothing.
4. `networkAttachHandler.Apply` in `connector_network.go`: the error of the `h.Observe` call before the connect becomes `core.Definite(fmt.Errorf("cloudflare: observe %s: %w", resource, err))`, as in `networkDetachHandler.Apply`.
5. `hostnameAddHandler.Apply` in `hostname_add.go`: where the function has its own `err`, replace each `if err := X; err != nil {` that shadows it (the `insertHostname` write, the `PutConfiguration` call and the two `env.Effects.Made` calls) with `err = X` followed by `if err != nil {`, and keep every returned value as it is. This changes no behaviour.
6. In `connector_testhost_test.go`, make the test host answer as the Engine does: `agDocker.ListContainers` starts from `out := []dockerapi.ContainerSummary{}` instead of a nil slice, and `agDocker.ConnectNetwork` refuses a network named `host` or `none` with `agAPIError(http.StatusForbidden, ...)` before it changes anything.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w10a-tmp/16 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestW10Cf|TestAgDockerCreatesInspectsAndConnects|TestRsAddPlanWhenTheServiceHostCannotBeResolved|TestCpApply|TestClLogSecretsFailClosed|TestRegCompositeSteps' ./internal/modules/cloudflare/
timeout 600 go test -count=1 ./internal/modules/cloudflare/...
```

Expected: both `ok`. If a test elsewhere relied on a connection to `host` or `none` or on a nil list, fix that test to the Engine's behaviour and say so in the report.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w10a-tmp/16 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short schemas docs/api
rm -rf /root/.cache/w10a-tmp/16 && go clean -testcache
```

Expected: build and vet print nothing, every test line is `ok` or `?`, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing, and `git status --short schemas docs/api` prints nothing. Record the real output.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/cloudflare/connector_run.go internal/modules/cloudflare/connector_network.go internal/modules/cloudflare/hostname_add.go \
  internal/modules/cloudflare/connector_run_test.go internal/modules/cloudflare/connector_network_test.go \
  internal/modules/cloudflare/connector_network_detach_test.go internal/modules/cloudflare/connector_testhost_test.go \
  internal/modules/cloudflare/hostname_add_test.go internal/modules/cloudflare/connector_stop_test.go \
  internal/modules/cloudflare/connector_logsecrets_test.go internal/modules/cloudflare/register_test.go
git commit -m "fix(cloudflare): mark connector reads Definite"
```

### Task 17: NPM web list validation and panel errors

**Files:**
- Create: `web/src/modules/npm/list-replies.ts`
- Create: `web/src/modules/npm/list-replies.test.ts`
- Modify: `web/src/modules/npm/instance-panel.tsx`
- Modify: `web/src/modules/npm/access-lists-tab.tsx`
- Modify: `web/src/modules/npm/certificates-tab.tsx`
- Modify: `web/src/modules/npm/proxy-hosts-tab.tsx`
- Modify: `web/src/modules/npm/container-actions.tsx`
- Test: `web/src/modules/npm/instance-panel.test.tsx`, `access-lists-tab.test.tsx`, `certificates-tab.test.tsx`, `proxy-hosts-tab.test.tsx`, `container-actions.test.tsx` (all in `web/src/modules/npm/`)
- Read only: `web/src/modules/npm/types.ts` (its `ProxyHostView`, `AccessListView`, `CertificateView`, `ProxyHostList`, `AccessListList`, `CertificateList` types and its `isInstanceStatus` guard; Task 32 owns edits to this file, so the new guards live in `list-replies.ts`), `web/src/modules/npm/status.ts` (`messageOf`), `web/src/modules/npm/fixtures.ts`

**Interfaces:**
- Consumes: the types of `types.ts` named above, `NPM_ACTION_VERSION` and `messageOf` (`status.ts`). No Contracts entry is involved.
- Produces, all in `web/src/modules/npm/list-replies.ts` and used only by the three tabs and this task's tests:
  - `isProxyHostList(v: unknown): v is ProxyHostList`, `isAccessListList(v: unknown): v is AccessListList`, `isCertificateList(v: unknown): v is CertificateList`.
  - `readProxyHostList(hostId: string): Promise<ProxyHostList>`, `readAccessListList(hostId: string): Promise<AccessListList>`, `readCertificateList(hostId: string): Promise<CertificateList>`. Each reads `npm.proxyhost.list`, `npm.accesslist.list` or `npm.certificate.list` at `NPM_ACTION_VERSION` on the host and throws an `Error` whose message is exactly `The host answered with an unexpected list of NPM proxy hosts.`, `The host answered with an unexpected list of NPM access lists.` or `The host answered with an unexpected list of NPM certificates.` when its guard refuses the reply.

**Items:** 370, 371, 373

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

Create `web/src/modules/npm/list-replies.test.ts` with this content:

```ts
import { expect, test } from "vitest"
import { npAccessList, npCertificate, npProxyHost } from "./fixtures"
import { isAccessListList, isCertificateList, isProxyHostList } from "./list-replies"

type Json = Record<string, unknown>

const lrGuards = [isProxyHostList, isAccessListList, isCertificateList]

function lrJSON(v: unknown): unknown {
  return JSON.parse(JSON.stringify(v))
}

test("each list guard accepts the module's JSON, with items or without", () => {
  expect(isProxyHostList(lrJSON({ items: [npProxyHost(), npProxyHost({ id: 4, domains: [], certificateId: 0, accessListId: 0 })] }))).toBe(true)
  expect(isAccessListList(lrJSON({ items: [npAccessList(), npAccessList({ id: 3, clients: [], satisfyAny: true })] }))).toBe(true)
  expect(isCertificateList(lrJSON({ items: [npCertificate(), npCertificate({ id: 4, name: "", domains: [] })] }))).toBe(true)
  for (const guard of lrGuards) expect(guard({ items: [] })).toBe(true)
})

test.each([
  ["null", null],
  ["a string", "items"],
  ["an array", []],
  ["an empty object", {}],
  ["items that is null", { items: null }],
  ["items that is an object", { items: {} }],
])("every list guard refuses a reply that is %s", (_, reply) => {
  for (const guard of lrGuards) expect(guard(reply)).toBe(false)
})

test("every list guard refuses a list with an item that is not an object", () => {
  for (const guard of lrGuards) expect(guard({ items: [null] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "3" }],
  ["domains that is a string", { domains: "app.example.com" }],
  ["a domain that is a number", { domains: [1] }],
  ["an unknown scheme", { forwardScheme: "ftp" }],
  ["a forwardHost that is a number", { forwardHost: 1 }],
  ["a forwardPort that is a string", { forwardPort: "80" }],
  ["a missing certificateId", { certificateId: undefined }],
  ["an accessListId that is a string", { accessListId: "0" }],
  ["an sslForced that is a string", { sslForced: "yes" }],
  ["a missing hsts", { hsts: undefined }],
  ["a websockets that is a number", { websockets: 1 }],
  ["a reserved that is null", { reserved: null }],
  ["a missing unclaimed", { unclaimed: undefined }],
])("isProxyHostList refuses an item with %s", (_, over) => {
  expect(isProxyHostList({ items: [npProxyHost(), { ...npProxyHost(), ...over }] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "2" }],
  ["a name that is a number", { name: 7 }],
  ["a satisfyAny that is a string", { satisfyAny: "yes" }],
  ["clients that is null", { clients: null }],
  ["a client address that is a number", { clients: [{ address: 1, directive: "allow" }] }],
  ["a client with an unknown directive", { clients: [{ address: "203.0.113.0/24", directive: "permit" }] }],
  ["a reserved that is null", { reserved: null }],
  ["a missing unclaimed", { unclaimed: undefined }],
])("isAccessListList refuses an item with %s", (_, over) => {
  expect(isAccessListList({ items: [npAccessList(), { ...npAccessList(), ...over }] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "3" }],
  ["a missing name", { name: undefined }],
  ["domains that is a string", { domains: "app.example.com" }],
  ["an expiresOn that is a number", { expiresOn: 1 }],
  ["a reserved that is a number", { reserved: 1 }],
  ["an unclaimed that is a string", { unclaimed: "no" }],
])("isCertificateList refuses an item with %s", (_, over) => {
  expect(isCertificateList({ items: [npCertificate(), { ...npCertificate(), ...over }] })).toBe(false)
})
```

In `web/src/modules/npm/instance-panel.test.tsx`, change the line `import type { InstanceStatus } from "./types"` to `import { npmKey, type InstanceStatus } from "./types"`, then add this test after `shows the reason when the status cannot be read`:

```tsx
test("a failed background refetch keeps the panel and an open change dialog", async () => {
  let reads = 0
  const { client } = ipRender(ipRunning(), {
    [IP_STATUS]: () => {
      reads += 1
      if (reads === 1) return { body: { output: ipRunning() } }
      return { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Upgrade" }))
  const dialog = await screen.findByRole("dialog", { name: "Upgrade Nginx Proxy Manager" })
  await within(dialog).findByLabelText("Planned changes")
  await client.invalidateQueries({ queryKey: npmKey("h1", "instance.status") })
  await waitFor(() => expect(client.getQueryState(npmKey("h1", "instance.status"))?.status).toBe("error"))
  expect(screen.getByRole("dialog", { name: "Upgrade Nginx Proxy Manager" })).toBeTruthy()
  expect(screen.getByText("Admin email")).toBeTruthy()
  expect(screen.queryByText("the NPM module is unavailable")).toBeNull()
})
```

In `web/src/modules/npm/proxy-hosts-tab.test.tsx`, add after `shows the reason when the proxy hosts cannot be read`:

```tsx
test.each([
  ["no items", {}],
  ["an item without domains", { items: [{ id: 3 }] }],
])("shows an error and no table when the reply has %s", async (_, output) => {
  phRender([], { [PH_LIST]: { body: { output } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected list of NPM proxy hosts.")
  expect(screen.queryByRole("table")).toBeNull()
})
```

In `web/src/modules/npm/access-lists-tab.test.tsx`, add after `shows the reason when the access lists cannot be read`:

```tsx
test.each([
  ["no items", {}],
  ["an item without clients", { items: [{ id: 2, name: "office" }] }],
])("shows an error and no table when the reply has %s", async (_, output) => {
  alRender([], { [AL_LIST]: { body: { output } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected list of NPM access lists.")
  expect(screen.queryByRole("table")).toBeNull()
})
```

In `web/src/modules/npm/certificates-tab.test.tsx`, add after `shows the reason when the certificates cannot be read`:

```tsx
test.each([
  ["no items", {}],
  ["an item without domains", { items: [{ id: 5, name: "app.example.com" }] }],
])("shows an error and no table when the reply has %s", async (_, output) => {
  ceRender([], { [CE_LIST]: { body: { output } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected list of NPM certificates.")
  expect(screen.queryByRole("table")).toBeNull()
})
```

In `web/src/modules/npm/container-actions.test.tsx`:

1. Change the line `import type { ContainerDetail, EndpointView } from "@/modules/docker/types"` to `import { dockerKey, type ContainerDetail, type EndpointView } from "@/modules/docker/types"`.
2. Add one row to the table of the existing `test.each` named `%s %j keeps Plan off`, after the `["Access list ID (optional)", "abc"]` row: `["Access list ID (optional)", "0"],`.
3. Add these tests after `shows the reason when the container cannot be read`:

```tsx
test("shows the reason when the networks cannot be read", async () => {
  caRender({ [CA_NETWORKS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
  expect(screen.queryByRole("form")).toBeNull()
})

test.each([
  ["a container reply without networks", CA_INSPECT, { output: { id: "c1" } }, "The host answered with an unexpected container."],
  ["a networks reply that is not a list", CA_NETWORKS, { networks: "none" }, "The host answered with an unexpected list of Docker networks."],
])("shows an error and no form for %s", async (_, route, body, message) => {
  caRender({ [route]: { body } })
  expect((await screen.findByRole("alert")).textContent).toBe(message)
  expect(screen.queryByRole("form")).toBeNull()
})

test("a network picked earlier is not selected again when it returns to the supported set", async () => {
  let backend = "bridge"
  const { client } = caRender({
    [CA_NETWORKS]: () => ({ body: { networks: caNetworks().map((n) => (n.name === "backend" ? { ...n, driver: backend } : n)) } }),
  })
  const user = userEvent.setup()
  const form = await caForm()
  await user.click(within(form).getByRole("radio", { name: "backend" }))
  expect(within(form).getByRole("radio", { name: "backend", checked: true })).toBeTruthy()

  backend = "overlay"
  await client.invalidateQueries({ queryKey: dockerKey("h1", "networks") })
  await waitFor(() => expect(within(form).getAllByRole("radio")).toHaveLength(1))
  expect(within(form).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()

  backend = "bridge"
  await client.invalidateQueries({ queryKey: dockerKey("h1", "networks") })
  await waitFor(() => expect(within(form).getAllByRole("radio")).toHaveLength(2))
  expect(within(form).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
  expect(within(form).getByRole("radio", { name: "backend", checked: false })).toBeTruthy()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```sh
mkdir -p /root/.cache/w10a-tmp/17 && export TMPDIR=/root/.cache/w10a-tmp/17 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/npm/list-replies.test.ts src/modules/npm/instance-panel.test.tsx src/modules/npm/access-lists-tab.test.tsx src/modules/npm/certificates-tab.test.tsx src/modules/npm/proxy-hosts-tab.test.tsx src/modules/npm/container-actions.test.tsx
```

Expected: FAIL.
- `list-replies.test.ts` fails to load because `./list-replies` does not exist.
- The instance-panel test fails at `findByRole("dialog"...)`/`getByRole("dialog"...)`: the error alert replaced the panel and its dialog.
- The three tab tests fail with `TypeError: Cannot read properties of undefined` (reading `length`, `join` or `map`), thrown while rendering the reply.
- In `container-actions.test.tsx` the two guard rows fail with a `TypeError` for `networks`/`map`, and the picked-network test fails at its last assertion: `backend` is checked again instead of `alpha`.
- The failed-networks-read test and the access-list `0` row pass already: they only add coverage the register names as missing.

- [ ] **Step 3: Implement**

1. `list-replies.ts`: a private `isRecord` and `isStringArray`, the three exported guards and the three exported readers named in Interfaces.
   - A guard accepts only a record whose `items` is an array in which every element passes the item check.
   - Proxy host item: `id`, `forwardPort`, `certificateId`, `accessListId` are numbers; `domains` is an array of strings; `forwardScheme` is `"http"` or `"https"`; `forwardHost` and `reserved` are strings; `sslForced`, `hsts`, `websockets` and `unclaimed` are booleans.
   - Access list item: `id` is a number; `name` and `reserved` are strings; `satisfyAny` and `unclaimed` are booleans; `clients` is an array of records with a string `address` and a `directive` of `"allow"` or `"deny"`.
   - Certificate item: `id` is a number; `name`, `expiresOn` and `reserved` are strings; `domains` is an array of strings; `unclaimed` is a boolean.
   - Each reader calls `readAction<unknown>(action, NPM_ACTION_VERSION, {}, hostId)` and throws the exact message from Interfaces when its guard refuses the body, as `readInstanceStatus` in `status.ts` does.
2. `access-lists-tab.tsx`, `certificates-tab.tsx`, `proxy-hosts-tab.tsx`: replace each `readAction<{ items: ... }>` query function with the matching reader. Show a failed read with `messageOf` from `./status` in place of `errorMessage`, because `errorMessage` answers `Something went wrong. Try again.` for an `Error` that is not an `ApiError`; an `ApiError` keeps its text through `messageOf`. Drop the imports that become unused.
3. `proxy-hosts-tab.tsx`: remove the unchecked `input.domains as string[]` in `plan`. `ProxyHostForm` calls `onPlan(domains, input)` with the `string[]` it already parsed, and `plan(domains, input)` builds the title from `domains` and keeps the input as it is today (the `update` branch still prepends `id`).
4. `instance-panel.tsx`, `InstancePanel`: render the error alert only while the query holds no data. Branch on `query.data === undefined` first (a spinner while `isPending`, the alert otherwise), so a failed background refetch of a query that has data leaves the status rows, the buttons and any open `NpmChange` or `NpmControllerChange` dialog in place. Do not add another alert for the failed refetch.
5. `container-actions.tsx`:
   - In `ExposeLoader`, wrap the two reads in readers that validate what `ExposeForm` uses and throw `The host answered with an unexpected container.` when the inspect reply lacks an array `networks` of records with a string `network`, or an array `ports` of records with a number `privatePort`, and `The host answered with an unexpected list of Docker networks.` when the networks reply is not a record with an array `networks` of records with string `name` and `driver`. Keep the query keys.
   - Show a failed read with `messageOf` (already imported) in place of `errorMessage`.
   - In `ExposeForm`, reset `picked` to the empty string whenever it is not in `supported`: do it during render, guarded by that condition (`if (picked !== "" && !supported.includes(picked)) setPicked("")`), not in an effect. The derived `network` expression stays.
6. Do not change `ExposeAction`'s own status error branch, `types.ts` or any other file.

- [ ] **Step 4: Run the tests to verify they pass**

Run the command of Step 2 again.
Expected: PASS, every file green.

- [ ] **Step 5: Run the web checks**

```sh
export TMPDIR=/root/.cache/w10a-tmp/17 GOTOOLCHAIN=local
timeout 1200 make lint-web test-web
rm -rf /root/.cache/w10a-tmp/17 && go clean -testcache
```

Expected: `eslint . --max-warnings 0` and both `tsc --noEmit` runs print nothing, and `vitest run` ends with every file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/npm/list-replies.ts web/src/modules/npm/list-replies.test.ts web/src/modules/npm/instance-panel.tsx web/src/modules/npm/instance-panel.test.tsx web/src/modules/npm/access-lists-tab.tsx web/src/modules/npm/access-lists-tab.test.tsx web/src/modules/npm/certificates-tab.tsx web/src/modules/npm/certificates-tab.test.tsx web/src/modules/npm/proxy-hosts-tab.tsx web/src/modules/npm/proxy-hosts-tab.test.tsx web/src/modules/npm/container-actions.tsx web/src/modules/npm/container-actions.test.tsx
git commit -m "fix(web): validate NPM list reads and keep panels"
```

### Task 18: DERP and Cloudflare web fixes

**Files:**
- Modify: `web/src/modules/derp/probe-panel.tsx`
- Modify: `web/src/modules/derp/region-dialog.tsx`
- Modify: `web/src/modules/derp/deploy-dialog.tsx`
- Modify: `web/src/modules/cloudflare/connectors-page.tsx`
- Test: `web/src/modules/derp/probe-panel.test.tsx`, `web/src/modules/derp/region-dialog.test.tsx`, `web/src/modules/derp/deploy-dialog.test.tsx`, `web/src/modules/cloudflare/connectors-page.test.tsx`

**Interfaces:**
- Consumes: nothing from another task and no Contracts entry. `RegionDialog`, `DeployDialog`, `ProbePanel` and `ConnectorsPage` keep their exported props.
- Produces: nothing another task consumes.

**Items:** 320, 385

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

In `web/src/modules/derp/probe-panel.test.tsx`, change the vitest import to `import { expect, test, vi } from "vitest"` and add after `lists the probe's warnings and says when the server is not ready`:

```tsx
test("lists repeated warnings without a duplicate key", async () => {
  const errors = vi.spyOn(console, "error").mockImplementation(() => {})
  try {
    stubFetch({ [PP_PROBE]: { body: { output: { ...ppProbe, ready: false, regionId: 0, regionCode: "", warnings: ["same", "same"] } } } })
    renderWithProviders(<ProbePanel hostId="h2" />)
    const list = await screen.findByRole("list", { name: "Warnings" })
    expect(within(list).getAllByRole("listitem").map((i) => i.textContent)).toEqual(["same", "same"])
    expect(errors.mock.calls.flat().join(" ")).not.toContain("same key")
  } finally {
    errors.mockRestore()
  }
})
```

In `web/src/modules/derp/region-dialog.test.tsx`:

1. In the test `Cancel closes the dialog and plans nothing`, replace the line `expect(onClose).toHaveBeenCalledTimes(1)` with these two lines:

```tsx
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(onClose).toHaveBeenCalledWith()
```

2. Add this test after `test.each(rdInvalid)("Review stays off for $what", ...)`:

```tsx
test("marks an empty region code and a region name over 64 characters invalid", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  const code = within(dialog).getByLabelText("Region code")
  const name = within(dialog).getByLabelText("Region name")
  expect(code.getAttribute("aria-invalid")).toBe("true")
  await user.type(code, "fra-x")
  expect(code.getAttribute("aria-invalid")).toBe("false")

  await user.type(name, "n".repeat(65))
  expect(name.getAttribute("aria-invalid")).toBe("true")
  expect(within(dialog).getByText("Use 1 to 64 characters.").className).toContain("text-destructive")
  expect(within(dialog).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)

  await user.clear(name)
  await user.type(name, "Frankfurt X")
  expect(name.getAttribute("aria-invalid")).toBe("false")
  expect(within(dialog).getByText("Use 1 to 64 characters.").className).not.toContain("text-destructive")
})
```

In `web/src/modules/derp/deploy-dialog.test.tsx`, in the test `Cancel closes the dialog and plans nothing`, replace the line `expect(onClose).toHaveBeenCalledTimes(1)` with these two lines:

```tsx
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(onClose).toHaveBeenCalledWith()
```

In `web/src/modules/cloudflare/connectors-page.test.tsx`, add these two tests inside the `describe("ConnectorsPage", ...)` block, after `a tunnel list that is still pending does not hold back the connectors`:

```tsx
  test("while the connector list is pending no tunnel is offered", async () => {
    cnRender([cnHost("h1", "web-1")], { [cnList("h1")]: () => new Promise(() => {}) })
    expect(await screen.findByLabelText("Checking which tunnels have a connector")).toBeTruthy()
    expect(screen.queryByRole("form", { name: "Deploy a connector" })).toBeNull()
    expect(screen.queryByRole("radio", { name: "darkpanel-c2" })).toBeNull()
  })

  test("a connector list that cannot be read leaves only a new tunnel on offer", async () => {
    cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: {
        status: 503,
        body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker Engine is not reachable", code: "docker.engine_unavailable" },
      },
    })
    expect((await screen.findByRole("alert")).textContent).toBe("the Docker Engine is not reachable")
    const form = await screen.findByRole("form", { name: "Deploy a connector" })
    expect(within(form).getAllByRole("radio")).toHaveLength(1)
    expect(within(form).getByRole("radio", { name: "Create a new tunnel" })).toBeTruthy()
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```sh
mkdir -p /root/.cache/w10a-tmp/18 && export TMPDIR=/root/.cache/w10a-tmp/18 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/derp/probe-panel.test.tsx src/modules/derp/region-dialog.test.tsx src/modules/derp/deploy-dialog.test.tsx src/modules/cloudflare/connectors-page.test.tsx
```

Expected: FAIL in five tests.
- The probe test: the joined console output contains `Encountered two children with the same key`.
- The two Cancel tests: `toHaveBeenCalledWith()` receives a click event instead of no arguments.
- The region test: the empty code has `aria-invalid="false"`.
- The first connector test: `Unable to find a label with the text of: Checking which tunnels have a connector`.
- The second connector test: three radios, not one.
- Every other test in these files passes.

- [ ] **Step 3: Implement**

1. `probe-panel.tsx`, `ReportList`: key each `li` by its index in the list, not by its text, so repeated lines each render under their own key.
2. `region-dialog.tsx`:
   - Mark the region code invalid whenever it does not match `CODE_PATTERN`, so an empty code is `aria-invalid` and its help line takes the destructive style like any other bad code (the `code !== "" &&` guard goes).
   - Give the region name input `aria-invalid` and an `aria-describedby` pointing at a help line with id `derp-region-name-help` and the text `Use 1 to 64 characters.`. The input is invalid when `name !== ""` and the trimmed name is not 1 to 64 characters; the help line then takes `text-xs text-destructive`, and otherwise `text-xs text-muted-foreground`. Review stays disabled by the same `ready` as today.
   - The Cancel button calls `onClose()` through `onClick={() => onClose()}`, so it passes no event.
3. `deploy-dialog.tsx`: the Cancel button calls `onClose()` the same way.
4. `connectors-page.tsx`:
   - `HostConnectors` passes the connector list query result to `DeployCard`, in place of the `taken` set, and `DeployCard` passes `taken` to `DeployForm` only once the list is known.
   - In `DeployCard`, after the existing tunnel-list spinner and error alert: while the connector list holds no data and is pending, render a spinner with `aria-label="Checking which tunnels have a connector"` and no form; when the connector list holds no data because its read failed, render the form with an empty tunnel list, so only `Create a new tunnel` is offered. Add no second alert there: the connectors card already shows the error, and the existing test of a failed read expects exactly one `alert`.
   - A connector list that has data keeps today's behaviour, including when a later background refetch fails.
5. Leave the rest of row 385 as it is: the doc comments of `web/src/modules/cloudflare/container-actions.tsx` and the jsdom `getContext` output of the full web run change no behaviour, and neither file is in this task.

- [ ] **Step 4: Run the tests to verify they pass**

Run the command of Step 2 again.
Expected: PASS, the four files green.

- [ ] **Step 5: Run the web checks**

```sh
export TMPDIR=/root/.cache/w10a-tmp/18 GOTOOLCHAIN=local
timeout 1200 make lint-web test-web
rm -rf /root/.cache/w10a-tmp/18 && go clean -testcache
```

Expected: eslint and both `tsc --noEmit` runs print nothing, and `vitest run` ends with every file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/derp/probe-panel.tsx web/src/modules/derp/probe-panel.test.tsx web/src/modules/derp/region-dialog.tsx web/src/modules/derp/region-dialog.test.tsx web/src/modules/derp/deploy-dialog.tsx web/src/modules/derp/deploy-dialog.test.tsx web/src/modules/cloudflare/connectors-page.tsx web/src/modules/cloudflare/connectors-page.test.tsx
git commit -m "fix(web): correct DERP dialogs and connector deploy"
```

### Task 19: Harness test fixes

**Files:**
- Create: `test/harness/w10hx_unit_test.go`
- Modify: `test/harness/m5_derp_helpers_test.go` (`m5dProbe`)
- Modify: `test/harness/m5dw_workflow_unit_test.go` (`m5dwUploadRE`)
- Modify: `test/harness/m6_npm_test.go` (`harnessSettings`, `exposeRemove`, `upgrade`, `admin`, `logDiagnostics`)

**Interfaces:**
- Consumes: `m6nResponse`, `m6nIsApp`, `m6nAppBody` and `m6nDiagnosticTimeout` of `m6_npm_test.go`, and `m5dwUploadRE` of `m5dw_workflow_unit_test.go`. No Contracts entry beyond "Test prefixes" (Task 19: `TestW10Hx…Unit…`).
- Produces, unexported helpers carrying the task's `w10hx` prefix, used by the scenarios in the same package and by `w10hx_unit_test.go`:
  - `w10hxProbeURL(host string) string` in `m5_derp_helpers_test.go`.
  - `w10hxGone(resp m6nResponse, err error) error`, `w10hxRecreated(before, after string) error`, `w10hxSetContainerCmd(db, id string) string`, `w10hxChanged(out string) error` and `w10hxDiagnosticContext(parent context.Context) (context.Context, context.CancelFunc)` in `m6_npm_test.go`.

**Items:** 321, 365, 366

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

- [ ] **Step 1: Write the failing tests**

Create `test/harness/w10hx_unit_test.go` with this content:

```go
//go:build harness

package harness

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestW10HxProbeURLUnit(t *testing.T) {
	tests := []struct{ host, want string }{
		{"203.0.113.10", "https://203.0.113.10/derp/probe"},
		{"derp.example.com", "https://derp.example.com/derp/probe"},
		{"2001:db8::10", "https://[2001:db8::10]/derp/probe"},
	}
	for _, tc := range tests {
		got := w10hxProbeURL(tc.host)
		if got != tc.want {
			t.Errorf("w10hxProbeURL(%q) = %s, want %s", tc.host, got, tc.want)
		}
		u, err := url.Parse(got)
		if err != nil || u.Hostname() != tc.host {
			t.Errorf("%s parses to host %q (%v), want %q", got, u.Hostname(), err, tc.host)
		}
	}
}

func TestW10HxUploadRegexpUnit(t *testing.T) {
	step := "        with:\n          name: harness\n\n\n          path: bin/harness\n"
	loc := m5dwUploadRE.FindStringIndex(step)
	if loc == nil {
		t.Fatalf("m5dwUploadRE found no upload line in %q", step)
	}
	if want := strings.Index(step, "          path:"); loc[0] != want {
		t.Errorf("m5dwUploadRE starts at %d, want %d, the start of the path line: its whitespace class crossed a line break", loc[0], want)
	}
	for _, other := range []string{"          path: bin/harness/derper-*\n", "          path: bin/harness-extra\n"} {
		if m5dwUploadRE.MatchString(other) {
			t.Errorf("m5dwUploadRE matches %q, which is not the upload line", other)
		}
	}
}

func TestW10HxGoneUnit(t *testing.T) {
	app := m6nResponse{Status: http.StatusNotFound, Body: []byte(m6nAppBody + "\n")}
	tests := []struct {
		name string
		resp m6nResponse
		err  error
		want string
	}{
		{"the target still answers", app, nil, "still reaches the removed target"},
		{"NPM answers with a page of its own", m6nResponse{Status: http.StatusOK, Body: []byte("Congratulations!")}, nil, ""},
		{"NPM answers with its own 404", m6nResponse{Status: http.StatusNotFound, Body: []byte("<html>not found</html>")}, nil, ""},
		{"the request failed", m6nResponse{}, errors.New("connection refused"), "connection refused"},
	}
	for _, tc := range tests {
		err := w10hxGone(tc.resp, tc.err)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestW10HxRecreatedUnit(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		want          string
	}{
		{"a new container", "aa", "bb", ""},
		{"the same container", "aa", "aa", "did not change"},
		{"no id after the upgrade", "aa", "", "no container id"},
		{"no id before the upgrade", "", "bb", "no container id"},
	}
	for _, tc := range tests {
		err := w10hxRecreated(tc.before, tc.after)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestW10HxSetContainerUnit(t *testing.T) {
	id := strings.Repeat("ab", 32)
	cmd := w10hxSetContainerCmd("/var/lib/darkpanel/agent.db", id)
	for _, want := range []string{
		"sudo sqlite3 -cmd '.timeout 10000' /var/lib/darkpanel/agent.db",
		"UPDATE npm_instances SET container_id = '" + id + "' WHERE id = 1; SELECT changes();",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the command lacks %q:\n%s", want, cmd)
		}
	}
	for out, wantErr := range map[string]bool{"1\n": false, "1": false, "0\n": true, "2\n": true, "": true, "Error: database is locked\n": true} {
		if err := w10hxChanged(out); (err != nil) != wantErr {
			t.Errorf("w10hxChanged(%q) = %v, want an error: %t", out, err, wantErr)
		}
	}
}

func TestW10HxDiagnosticContextUnit(t *testing.T) {
	type key struct{}
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), key{}, "kept"))
	cancelParent()
	ctx, cancel := w10hxDiagnosticContext(parent)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Errorf("the diagnostic context is done after its parent was cancelled: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if left := time.Until(deadline); !ok || left <= 0 || left > m6nDiagnosticTimeout {
		t.Errorf("deadline %v (set %t), want one within %s from now", deadline, ok, m6nDiagnosticTimeout)
	}
	if got := ctx.Value(key{}); got != "kept" {
		t.Errorf("the context value = %v, want the parent's", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```sh
mkdir -p /root/.cache/w10a-tmp/19 && export TMPDIR=/root/.cache/w10a-tmp/19 GOTOOLCHAIN=local
timeout 300 go test -tags harness -count=1 -run 'W10Hx' ./test/harness/
```

Expected: FAIL to build, with `undefined: w10hxProbeURL` as the first error (then `w10hxGone`, `w10hxRecreated`, `w10hxSetContainerCmd`, `w10hxChanged` and `w10hxDiagnosticContext`).

- [ ] **Step 3: Implement**

1. `m5_derp_helpers_test.go`: add `w10hxProbeURL(host string) string`, which returns `https://<host>/derp/probe` built with `url.URL{Scheme: "https", Host: ..., Path: "/derp/probe"}.String()`, wrapping `host` in square brackets when `netip.ParseAddr(host)` succeeds and `Is6()` is true. `m5dProbe` builds its request URL with it, and the error text `answered without a peer certificate` names that URL string.
2. `m6_npm_test.go`:
   - `w10hxGone(resp, err)` returns `nil` only when `err == nil` and `m6nIsApp(resp)` is false. For `err != nil` it returns `harness: the request failed, so the removal is not shown: %w`. When the target still answers it returns `harness: the host still reaches the removed target`.
   - `exposeRemove`: the `eventually` closure returns `w10hxGone(m6nGet(ctx, r.f.scanner, m6nAppHost, "/", nil))`, so a failed request is retried and an outage fails the step. The last check separates the two cases: a `m6nGet` error is reported on its own (`GET https://%s/ after the removal: %v`), and only a successful reply with a status other than 200 reports `answered %d, want 200`, so `resp.Status` is never read from a zero value.
   - `w10hxRecreated(before, after)` returns an error containing `no container id` when either is empty, `did not change` when they are equal, and `nil` otherwise. `upgrade` reads `npm.instance.status/v1` into a `prev` value before it applies `npm.instance.upgrade` and, after its existing status check, reports `w10hxRecreated(prev.ContainerID, st.ContainerID)` through `t.Error`. The upgrade always stops, removes and recreates the container, so a changed id is the expected outcome.
   - `w10hxSetContainerCmd(db, id)` returns `sudo sqlite3 -cmd '.timeout 10000' <db> "UPDATE npm_instances SET container_id = '<id>' WHERE id = 1; SELECT changes();"`, and `w10hxChanged(out)` returns an error unless `strings.TrimSpace(out)` is `1`, naming the output in the message. `harnessSettings` runs the command through `m3Run` and fails with `t.Fatal` on `w10hxChanged`, so an update that matched no row stops the scenario. The `id` was validated as 64 hexadecimal characters just before.
   - `w10hxDiagnosticContext(parent)` returns `context.WithTimeout(context.WithoutCancel(parent), m6nDiagnosticTimeout)`. `logDiagnostics` takes each command's context from it instead of from `r.ctx`, so the diagnostics still run when the 40-minute test context has expired.
   - In `admin`, rename the local `fleet` (type `npm.InstancesOutput`) to `instances`, which stops it shadowing the package type `fleet`.
3. `m5dw_workflow_unit_test.go`: change `m5dwUploadRE` to ``regexp.MustCompile(`(?m)^[ \t]*path: bin/harness$`)``. Run `TestW10HxUploadRegexpUnit` before this edit, after the helpers compile, to see it fail with `m5dwUploadRE starts at … want …`, then after it to see it pass.
4. No VM scenario changes in any way not listed above.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/19 GOTOOLCHAIN=local
timeout 300 go test -tags harness -count=1 -run 'W10Hx' ./test/harness/
timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
```

Expected: `ok` for both; no VM test runs, since each needs its `-os`, `-m2` and `-m6n` flags.

- [ ] **Step 5: Run the Go checks**

```sh
export TMPDIR=/root/.cache/w10a-tmp/19 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/...
timeout 1500 go test -count=1 ./...
timeout 600 go test -tags harness -race -count=1 -run 'Unit' ./test/harness/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/19 && go clean -testcache
```

Expected: builds and vets print nothing, every test line is `ok` or `?`, `golangci-lint` prints `0 issues.` twice and `go mod tidy -diff` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add test/harness/w10hx_unit_test.go test/harness/m5_derp_helpers_test.go test/harness/m5dw_workflow_unit_test.go test/harness/m6_npm_test.go
git commit -m "test(harness): fix vacuous checks and probe URL"
```

### Task 20: Audit rows of system services

**Files:**
- Create: `internal/core/store/migrations/controller/0015_audit_service_phases.sql`
- Create: `internal/modules/cloudflare/hostnames_audit_test.go`
- Create: `internal/modules/tailnet/keysweep_audit_test.go`
- Test: `internal/core/store/migrations_test.go`
- Read only: `internal/core/store/migrations/controller/0004_audit.sql`, `0010_audit_phases.sql`, `0011_platform_surfaces.sql`, `internal/controller/audit/audit.go` (the real controller audit store, `audit.New(db, clock)`), `internal/modules/cloudflare/hostnames_service.go`, `internal/modules/tailnet/keysweep.go`

**Interfaces:**
- Consumes: Contracts "Audit phases of system services". `audit.New(db *store.DB, clock core.Clock) *audit.Store` and its `List` (`internal/controller/audit`) are used by the two new tests only.
- Produces: the migration `0015_audit_service_phases.sql`, which widens the `core_audit.phase` CHECK to the set of `0011_platform_surfaces.sql` plus `check` and `delete`. No writer's phase string changes, and no Go production file changes.

**Items:** 390

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 3 = 6
**Lenses:** silent-failures

- [ ] **Step 1: Write the failing tests**

In `internal/core/store/migrations_test.go`, add this test after `TestControllerPlatformSurfacesMigration`:

```go
func TestW10AuServicePhasesMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "w9", time.Now()), coreSetUpTo(t, SideController, 14), nil); err != nil {
		t.Fatal(err)
	}
	const at = "2026-11-06T00:00:00.000000000Z"
	insert := `INSERT INTO core_audit (id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) VALUES (?, ?, ?, ?, ?, ?, 'fix.item.add', 1, ?, ?, ?, ?, ?, ?)`
	exec := func(args ...any) error {
		_, err := db.Writer().ExecContext(ctx, insert, args...)
		return err
	}
	for _, args := range [][]any{
		{"A1", nil, "", at, "user:alice", nil, "apply", "C1", `{"name":"a"}`, "ok", nil, "127.0.0.1"},
		{"A2", 7, "E1", at, "system:recovery", "H1", "interrupt", "C2", nil, "error", "core.interrupted", nil},
		{"A3", 8, "E1", at, "user:alice", "H1", "reveal", "C2", nil, "ok", nil, "192.0.2.7"},
		{"A4", 9, "E1", at, "user:alice", "H1", "revert", "C2", nil, "override", nil, nil},
	} {
		if err := exec(args...); err != nil {
			t.Fatalf("audit row %v: %v", args[0], err)
		}
	}
	for i, phase := range []string{"check", "delete"} {
		if err := exec(fmt.Sprintf("B%d", i), nil, "", at, "system:fix.service", nil, phase, nil, nil, "ok", nil, nil); err == nil {
			t.Fatalf("phase %s was accepted before the migration", phase)
		}
	}
	const audit = `SELECT json_array(id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) FROM core_audit ORDER BY id`
	before := surfacesRows(t, db, audit)

	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "w10a", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 15 || res.Applied[0].Name != "audit_service_phases" {
		t.Fatalf("applied %+v, want 0015_audit_service_phases first", res.Applied)
	}
	if after := surfacesRows(t, db, audit); !slices.Equal(after, before) {
		t.Errorf("audit rows after the migration = %v, want %v", after, before)
	}
	var tableSQL, indexSQL string
	if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'core_audit'").
		Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
		t.Errorf("core_audit is not STRICT: %s", tableSQL)
	}
	if err := db.Read().QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'core_audit_host_epoch_seq' AND tbl_name = 'core_audit'").
		Scan(&indexSQL); err != nil {
		t.Fatalf("index core_audit_host_epoch_seq: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE") {
		t.Errorf("core_audit_host_epoch_seq is not unique: %s", indexSQL)
	}

	phases := []string{
		"apply", "confirm", "revert", "recover", "interrupt", "login", "logout", "undo", "resolve", "release",
		"revert-watchdog", "reveal", "uninstall-abort", "check", "delete",
	}
	for i, phase := range phases {
		if err := exec(fmt.Sprintf("N%02d", i), i+1, "E2", at, "system:fix.service", "H1", phase, nil, nil, "ok", nil, nil); err != nil {
			t.Errorf("phase %s refused: %v", phase, err)
		}
	}
	if err := exec("N90", 90, "E2", at, "system:fix.service", "H1", "check", nil, nil, "error", "fix.failed", nil); err != nil {
		t.Errorf("phase check with result error refused: %v", err)
	}
	if err := exec("N91", 91, "E2", at, "user:alice", "H1", "revert", nil, nil, "override", nil, nil); err != nil {
		t.Errorf("result override refused: %v", err)
	}
	refused := []struct {
		name string
		args []any
	}{
		{"unknown phase", []any{"R1", nil, "", at, "system:fix.service", nil, "bogus", nil, nil, "ok", nil, nil}},
		{"unknown result", []any{"R2", nil, "", at, "system:fix.service", nil, "check", nil, nil, "partial", nil, nil}},
		{"duplicate host, epoch and seq", []any{"R3", 7, "E1", at, "system:fix.service", "H1", "delete", nil, nil, "ok", nil, nil}},
	}
	for _, r := range refused {
		if err := exec(r.args...); err == nil {
			t.Errorf("%s: accepted", r.name)
		}
	}
}
```

Create `internal/modules/cloudflare/hostnames_audit_test.go`:

```go
package cloudflare

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
)

// TestW10AuHostnameCheckWritesThroughTheAuditStore runs the check against the
// controller's own audit store on a migrated database, which refuses a phase
// outside its CHECK list, as the in-memory audit of the other tests does not.
func TestW10AuHostnameCheckWritesThroughTheAuditStore(t *testing.T) {
	f := newHSFixture(t)
	log := audit.New(f.h.db, f.h.clock)
	f.deps.Audit = log
	f.pass()
	got, _, err := log.List(context.Background(), core.AuditQuery{Action: hostnamesService})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows = %+v, want one per hostname whose health changed", got)
	}
	for _, e := range got {
		if e.Actor != hostnamesActor || e.Phase != "check" || e.Result != "ok" {
			t.Errorf("audit row %+v, want actor %s, phase check and result ok", e, hostnamesActor)
		}
	}
}
```

Create `internal/modules/tailnet/keysweep_audit_test.go`:

```go
package tailnet

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
)

// TestW10AuKeySweepWritesThroughTheAuditStore runs a sweep against the
// controller's own audit store on a migrated database, which refuses a phase
// outside its CHECK list, as the in-memory audit of the other tests does not.
func TestW10AuKeySweepWritesThroughTheAuditStore(t *testing.T) {
	ctx := context.Background()
	f := swSetup(t)
	log := audit.New(f.h.db, f.h.clock)
	f.deps.Audit = log
	description := "darkpanel-" + swChange(1)
	id := f.key(t, description)
	f.row(t, description, "retired", "", time.Hour)
	if err := f.t.sweep(ctx, f.deps, f.h.clock.Now(), false); err != nil {
		t.Fatal(err)
	}
	if f.alive(id) {
		t.Error("the sweep left the key alive")
	}
	got, _, err := log.List(ctx, core.AuditQuery{Actor: keySweepActor})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("audit rows = %+v, want the one of the deleted key", got)
	}
	var in map[string]string
	if err := json.Unmarshal(got[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	if e := got[0]; e.Action != keySweepService || e.Phase != "delete" || e.Result != "ok" || in["keyId"] != id {
		t.Errorf("audit row %+v input %s, want phase delete, result ok and key %s", e, e.Input, id)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```sh
mkdir -p /root/.cache/w10a-tmp/20 && export TMPDIR=/root/.cache/w10a-tmp/20 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'W10Au' ./internal/core/store/ ./internal/modules/cloudflare/ ./internal/modules/tailnet/
```

Expected: FAIL in all three packages.
- `TestW10AuServicePhasesMigration`: `applied [] , want 0015_audit_service_phases first`, because no migration after 0014 exists yet.
- `TestW10AuHostnameCheckWritesThroughTheAuditStore`: `checkHostnames: … audit: insert entry: … CHECK constraint failed`.
- `TestW10AuKeySweepWritesThroughTheAuditStore`: an error from the sweep that contains `audit: insert entry` and `CHECK constraint failed`.

- [ ] **Step 3: Implement**

Create `internal/core/store/migrations/controller/0015_audit_service_phases.sql` with this content. It is a migration, so its SQL is given in full; it rebuilds the table the way `0011_platform_surfaces.sql` does:

```sql
-- The controller's system services journal the phases check (the Cloudflare
-- hostname health pass) and delete (the Tailscale key sweep) in core_audit.
-- SQLite cannot change a CHECK constraint in place, so the table is rebuilt
-- with the wider list, keeping every column, row and index. The previous
-- binary reads phase as a plain string (ADR 0005).

CREATE TABLE core_audit_new (
    id         TEXT PRIMARY KEY,
    seq        INTEGER,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    host_id    TEXT,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    phase      TEXT NOT NULL
        CHECK (phase IN ('apply', 'confirm', 'revert', 'recover', 'interrupt', 'login', 'logout',
                         'undo', 'resolve', 'release', 'revert-watchdog', 'reveal', 'uninstall-abort',
                         'check', 'delete')),
    change_id  TEXT,
    input_json TEXT,
    result     TEXT NOT NULL CHECK (result IN ('ok', 'error', 'override')),
    error_code TEXT,
    source     TEXT,
    epoch      TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO core_audit_new (id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result,
                            error_code, source, epoch)
    SELECT id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result, error_code, source,
           epoch
    FROM core_audit;

DROP TABLE core_audit;

ALTER TABLE core_audit_new RENAME TO core_audit;

CREATE UNIQUE INDEX core_audit_host_epoch_seq ON core_audit (host_id, epoch, seq);
```

Then:
1. Do not change `hostnames_service.go`, `keysweep.go`, the writers' phase strings, any earlier migration or the agent migrations: the agent has no `core_audit`.
2. The embedded set (`//go:embed migrations/*/*.sql`) picks the file up on its own. No migration list or golden names the file, so nothing else changes.
3. If an existing test fails because it counts the controller migrations or expects the latest to be `0014`, update that count or name; none was found when the plan was written.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/20 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'W10Au' ./internal/core/store/ ./internal/modules/cloudflare/ ./internal/modules/tailnet/
timeout 600 go test -count=1 ./internal/core/store/ ./internal/controller/audit/ ./internal/modules/cloudflare/... ./internal/modules/tailnet/...
```

Expected: `ok` for every package.

- [ ] **Step 5: Run the Go checks**

```sh
export TMPDIR=/root/.cache/w10a-tmp/20 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/core/store/ ./internal/controller/audit/ ./internal/modules/cloudflare/ ./internal/modules/tailnet/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/20 && go clean -testcache
```

Expected: builds and vets print nothing, every test line is `ok` or `?`, `golangci-lint` prints `0 issues.` and `go mod tidy -diff` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/core/store/migrations/controller/0015_audit_service_phases.sql internal/core/store/migrations_test.go internal/modules/cloudflare/hostnames_audit_test.go internal/modules/tailnet/keysweep_audit_test.go
git commit -m "fix(store): accept service audit phases"
```

### Task 21: Firewall reads under a host-state lock

**Files:**
- Create: `internal/modules/firewall/hostlock.go`
- Modify: `internal/modules/firewall/wire.go` (the `fw` struct and `productionFW`), `internal/modules/firewall/handler.go`, `internal/modules/firewall/firewall.go` (`resources`), `internal/modules/firewall/docker_resource.go` (`dockerResource`), `internal/modules/firewall/exposure_resource.go` (`exposureResource`), `internal/modules/firewall/snapshots.go` (`snapshotCompare`), `internal/modules/firewall/reconcile.go`, `internal/modules/firewall/uninstall.go`
- Test: Create `internal/modules/firewall/hostlock_test.go`
- Read only: `internal/core/lifecycle/engine.go` (`makePlan`, `apply.go` call the handler's `Plan`, `Snapshot`, `Apply`, `Verify` and `Revert` as separate calls; the engine holds none of this module's lock between them)

**Interfaces:**
- Consumes: nothing from another task. The test reuses the package's existing fakes: `pnHost`, `hkMutation`, `hkInput`, `dstDeps`, `newFakeDocker`, `rcHost`, `rcTask`, `rcApplied`, `dfVariants` with `dfFilterDocker`, `dfRawDocker` and `dfInterfaces`, `dunHost` and `fhJournal`. Contracts: Plan ruling W10-R9 (Assumptions).
- Produces: `fw.mu *sync.RWMutex`, the one host-state lock of a module instance (nil in a `fw` built field by field, which then takes no lock); `func (f fw) lockRead() (unlock func())`, `func (f fw) lockWrite() (unlock func())`; `func readLocked[Out any](f fw, read func(context.Context, core.Env) (Out, error)) func(context.Context, core.Env) (Out, error)`; `func (f fw) reconcileRead(ctx context.Context, env core.Env) (cfg fwstate.Config, cur fwstate.Loaded, st dockerState, skip bool, err error)`. No other task consumes them.

**Items:** 323

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** test-quality

The ruling, restated so this task is self-contained. The firewall module in the agent holds one `sync.RWMutex` per module instance. Every path that changes UFW's files, the loaded rules or the Docker block holds the write lock from its first write until both address families have reloaded. Status reads, plan preparation and the Docker and exposure resource reads hold the read lock for their whole read. Only exported entry points take the lock, and internal helpers a locked path calls take none, so no path takes the lock twice. A separate break-glass process is not covered: it runs when the agent cannot be reached.

The entry points, which Step 3 wires and `TestW10FlLockSites` pins exactly:

| Lock | Entry point | What reaches it |
|---|---|---|
| write | `mutation.Apply` | every mutating action: `firewall.state.enable`, `state.disable`, `state.repair`, `defaults.set`, `logging.set`, `rules.apply`, `rule.add`, `rule.delete`, `snapshot.restore`, `docker.enable`, `docker.disable`, `docker.repair`, `docker.set-sources`, `container.allow`, `container.deny`, `container.remove`, `containers.apply` (apply, repair and snapshot restore) |
| write | `mutation.Revert` | the engine's revert, the confirm-timeout watchdog's revert and the revert of a repair, all through `Handler.Revert` (whole restore, inverses, emergency restore and the owned-chain cleanup they run) |
| write | `module.Uninstall` | the uninstall's rules work and Docker cleanup |
| write | `fw.reconcile`, around `finishCleanups` only | the background pass's repeat of an interrupted Docker cleanup |
| read | `mutation.Plan` | plan preparation (`fw.plan`, `fw.prepare`) |
| read | `mutation.Snapshot` | the capture of the loaded state and the files |
| read | `mutation.Verify` | the comparison with the prediction |
| read | `mutation.Observe` | the observation of one effect |
| read | resources `firewall.status`, `firewall.docker`, `firewall.exposure` | through `readLocked` |
| read | action `firewall.snapshot.compare` | `snapshotCompare`, which reads the host when `to` is empty |
| read | `fw.reconcileRead` | the pass's reads of the Docker flag, the configuration, the loaded state and the Docker state |

No lock: `Manifest`, `BackgroundTasks`, `UninstallOptions`, the preflight checks, `reconcileWatch` (it only reads Docker events), the resource `firewall.snapshots` (it reads only the snapshot rows of the agent database) and every unexported helper.

Why `reconcile` must not hold a lock across its apply: `reconcileWith` ends in `tc.Apply`, which plans and applies through the engine, so `mutation.Plan` takes the read lock and `mutation.Apply` the write lock. A lock still held there is a self-deadlock. `sync.RWMutex` also blocks a second read lock while a writer waits, so a path that takes the read lock twice deadlocks as soon as a mutation queues, not only when it holds the write lock; the test covers both.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/firewall/hostlock_test.go`:

```go
package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// w10flBlocker is the runner of a fake host that can hold "ufw reload"
// after the files were written and before UFW reloaded. While it holds the
// reload, torn is true: the files describe a state the loaded rules do not
// yet match.
type w10flBlocker struct {
	core.Runner
	armed   atomic.Bool
	torn    atomic.Bool
	reached chan struct{}
	release chan struct{}
}

func newW10flBlocker(inner core.Runner) *w10flBlocker {
	return &w10flBlocker{Runner: inner, reached: make(chan struct{}), release: make(chan struct{})}
}

// Run holds the next "ufw reload" once armed, until release is closed, and
// lets everything else through.
func (b *w10flBlocker) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if cmd.Path != ufwPath || strings.Join(cmd.Args, " ") != "reload" || !b.armed.CompareAndSwap(true, false) {
		return b.Runner.Run(ctx, cmd)
	}
	b.torn.Store(true)
	b.reached <- struct{}{}
	<-b.release
	res, err := b.Runner.Run(ctx, cmd)
	b.torn.Store(false)
	return res, err
}

// w10flQueue queues a writer on the host-state lock from inside a path that
// holds it. Armed, the first loaded-state read of a path starts a writer
// and returns once the writer waits. A path that then takes the read lock a
// second time blocks behind that writer, which waits for the path's first
// read lock: a deadlock a lone reader hides. A path that holds the write
// lock only makes the writer wait for its end.
type w10flQueue struct {
	mu    *sync.RWMutex
	armed atomic.Bool
	wg    sync.WaitGroup
}

func (q *w10flQueue) hook(t *testing.T) {
	if !q.armed.CompareAndSwap(true, false) {
		return
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.mu.Lock()
		q.mu.Unlock() //nolint:staticcheck // SA2001: the writer only has to queue and pass
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if !q.mu.TryRLock() {
			return
		}
		q.mu.RUnlock()
	}
	t.Error("the queued writer never waited for the lock")
}

// step runs fn with the queue armed and fails when it takes over 10 s.
func (q *w10flQueue) step(t *testing.T, name string, fn func() error) {
	t.Helper()
	q.armed.Store(true)
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s deadlocked: the path takes the host-state lock twice", name)
	}
	q.wg.Wait()
}

// w10flTapReads makes every loaded-state read of f call on first.
func w10flTapReads(f *fw, on func()) {
	read := f.d.readLoaded
	f.d.readLoaded = func(ctx context.Context, env core.Env) (fwstate.Loaded, error) {
		on()
		return read(ctx, env)
	}
}

// w10flRig is a fake host with a UFW whose reload can be held, a firewall
// with its own host-state lock over it, and a Docker daemon. compare
// reports a difference while a reload is held, as the loaded rules lag the
// files then.
type w10flRig struct {
	h        *fakeHost
	f        fw
	block    *w10flBlocker
	watching atomic.Bool
	reads    chan struct{}
}

func newW10flRig(t *testing.T) *w10flRig {
	t.Helper()
	h, f, _ := pnHost(t)
	h.ufw["logging high"] = func(s *fhState) int { s.LogLevel = "high"; return 0 }
	r := &w10flRig{h: h, block: newW10flBlocker(h), reads: make(chan struct{}, 64)}
	f.d = dstDeps(h, newFakeDocker())
	f.d.compare = func(_, _ fwstate.Loaded, _ func(fwstate.Family, string, string) bool) []fwstate.ChainDiff {
		if !r.block.torn.Load() {
			return nil
		}
		return []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyA: []string{"-j ACCEPT"}}}
	}
	f.mu = new(sync.RWMutex)
	w10flTapReads(&f, func() {
		if r.watching.Load() {
			select {
			case r.reads <- struct{}{}:
			default:
			}
		}
	})
	r.f = f
	return r
}

// env is the Env of change c1 whose programs go through the blocker.
func (r *w10flRig) env() core.Env {
	env := r.h.env()
	env.Exec = r.block
	return env
}

// w10flLogging is a change of UFW's logging level.
func w10flLogging(fwstate.Config, fwstate.Loaded) (change, error) {
	op, err := loggingOp("high")
	return change{ops: []ufwOp{op}}, err
}

// w10flResource finds a resource of the firewall module built over f.
func w10flResource(t *testing.T, f fw, name string) core.ResourceDef {
	t.Helper()
	for _, rd := range (module{f: f}).Manifest().Resources {
		if rd.Name == name {
			return rd
		}
	}
	t.Fatalf("the manifest has no resource %s", name)
	return core.ResourceDef{}
}

// w10flStatusRead reads firewall.status through rd and fails when the read
// saw a torn state.
func w10flStatusRead(ctx context.Context, rd core.ResourceDef, env core.Env) error {
	raw, err := rd.Read(ctx, env)
	if err != nil {
		return err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if !st.InSync || len(st.OutOfSync) > 0 || st.SyncError != "" {
		return fmt.Errorf("status read a torn state: inSync %v, out of sync %+v, sync error %q", st.InSync, st.OutOfSync, st.SyncError)
	}
	return nil
}

// TestW10FlReadsWaitForTheReload: a status read or a plan that starts while
// a mutation is between its file write and its ufw reload, whether the
// mutation is an apply or a revert, does not read until the reload is done,
// and then sees the host in sync.
func TestW10FlReadsWaitForTheReload(t *testing.T) {
	for _, mutating := range []string{"apply", "revert"} {
		for _, reader := range []string{"status", "plan"} {
			t.Run(mutating+" then "+reader, func(t *testing.T) {
				ctx := t.Context()
				r := newW10flRig(t)
				env := r.env()
				m := hkMutation(r.f, nil)
				snap, err := m.Snapshot(ctx, env, hkInput{})
				if err != nil {
					t.Fatal(err)
				}
				run := func() error { _, err := m.Apply(ctx, env, hkInput{}); return err }
				if mutating == "revert" {
					if err := run(); err != nil {
						t.Fatal(err)
					}
					run = func() error { _, err := m.Revert(ctx, env, snap); return err }
				}
				r.block.armed.Store(true)
				mutated := make(chan error, 1)
				go func() { mutated <- run() }()
				select {
				case <-r.block.reached:
				case err := <-mutated:
					t.Fatalf("the %s ended without a reload: %v", mutating, err)
				case <-time.After(10 * time.Second):
					t.Fatalf("the %s never reached its reload", mutating)
				}

				readEnv := r.h.env()
				readEnv.ChangeID = ""
				read := func() error {
					if reader == "status" {
						return w10flStatusRead(ctx, w10flResource(t, r.f, "firewall.status"), readEnv)
					}
					out, err := hkMutation(r.f, w10flLogging).Plan(ctx, readEnv, hkInput{})
					if err != nil {
						return err
					}
					if len(out.Guards) != 1 || !out.Guards[0].OK {
						return fmt.Errorf("plan read a torn state: guards %+v", out.Guards)
					}
					return nil
				}
				r.watching.Store(true)
				readDone := make(chan error, 1)
				go func() { readDone <- read() }()
				select {
				case <-r.reads:
					t.Errorf("the %s read the host between the %s's file write and its reload", reader, mutating)
				case <-time.After(300 * time.Millisecond):
				}
				close(r.block.release)
				for name, ch := range map[string]chan error{mutating: mutated, reader: readDone} {
					select {
					case err := <-ch:
						if err != nil {
							t.Errorf("%s: %v", name, err)
						}
					case <-time.After(10 * time.Second):
						t.Fatalf("%s did not finish after the reload", name)
					}
				}
			})
		}
	}
}

// TestW10FlNoPathDeadlocks: every entry point that takes the host-state
// lock runs to its end while a writer queues for the lock, so none takes
// the lock twice; a reconcile pass holds no lock when it applies a change
// through the engine.
func TestW10FlNoPathDeadlocks(t *testing.T) {
	t.Run("a mutation's calls and the reads", func(t *testing.T) {
		ctx := t.Context()
		r := newW10flRig(t)
		q := &w10flQueue{mu: r.f.mu}
		w10flTapReads(&r.f, func() { q.hook(t) })
		env := r.h.env()
		planEnv := env
		planEnv.ChangeID = ""
		m := hkMutation(r.f, nil)
		var snap []byte
		var out ChangeOutput
		q.step(t, "Plan", func() error { _, err := m.Plan(ctx, planEnv, hkInput{}); return err })
		q.step(t, "Snapshot", func() (err error) { snap, err = m.Snapshot(ctx, env, hkInput{}); return })
		q.step(t, "Apply", func() (err error) { out, err = m.Apply(ctx, env, hkInput{}); return })
		q.step(t, "Verify", func() error { return m.Verify(ctx, env, hkInput{}, out) })
		q.step(t, "Observe", func() error { _, err := m.Observe(ctx, env, r.h.journal.effects[0]); return err })
		for _, name := range []string{"firewall.status", "firewall.docker", "firewall.exposure"} {
			rd := w10flResource(t, r.f, name)
			q.step(t, name, func() error { _, err := rd.Read(ctx, planEnv); return err })
		}
		for _, def := range (module{f: r.f}).Manifest().Actions {
			if def.Spec.Name != "firewall.snapshot.compare" {
				continue
			}
			raw := json.RawMessage(`{"from":"` + string(snap) + `"}`)
			q.step(t, def.Spec.Name, func() error { _, err := def.Handler.Read(ctx, planEnv, raw); return err })
		}
		q.step(t, "Revert", func() error { _, err := m.Revert(ctx, env, snap); return err })
	})

	t.Run("reconcile", func(t *testing.T) {
		h, _, f := rcHost(t, true)
		v := &dfVariants{t: t, filter: map[string]string{"nft": dfFilterDocker}, raw: map[string]string{"nft": dfRawDocker}}
		h.onReadVariant = func(fam fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
			return v.read(t.Context(), core.Env{}, fam, variant, tables)
		}
		h.onInterfaces = func() ([]netstat.Interface, error) { return dfInterfaces, nil }
		f.mu = new(sync.RWMutex)
		q := &w10flQueue{mu: f.mu}
		w10flTapReads(&f, func() { q.hook(t) })
		var calls []rcApplied
		tc := rcTask(h, &calls, nil)
		apply := tc.Apply
		var held atomic.Bool
		tc.Apply = func(ctx context.Context, action string, input any) (core.ChangeView, error) {
			q.wg.Wait()
			if !f.mu.TryLock() {
				held.Store(true)
			} else {
				f.mu.Unlock()
			}
			return apply(ctx, action, input)
		}
		q.step(t, "reconcile", func() error { return f.reconcile(t.Context(), tc) })
		if len(calls) != 1 {
			t.Fatalf("the pass applied %v, want one change so that the lock is checked at the apply", calls)
		}
		if held.Load() {
			t.Error("the pass holds the host-state lock while it applies a change")
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		_, m, env, _ := dunHost(t)
		m.f.mu = new(sync.RWMutex)
		q := &w10flQueue{mu: m.f.mu}
		w10flTapReads(&m.f, func() { q.hook(t) })
		q.step(t, "Uninstall", func() error { return m.Uninstall(t.Context(), env, false, map[string]string{"rules": "keep"}) })
	})
}

// w10flSites lists, by enclosing function, where the package's own files
// call lockRead and lockWrite.
func w10flSites(t *testing.T) (reads, writes []string) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil {
				name = w10flRecvName(fd.Recv.List[0].Type) + "." + name
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "lockRead":
						reads = append(reads, name)
					case "lockWrite":
						writes = append(writes, name)
					}
				}
				return true
			})
		}
	}
	slices.Sort(reads)
	slices.Sort(writes)
	return reads, writes
}

func w10flRecvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return w10flRecvName(x.X)
	case *ast.IndexExpr:
		return w10flRecvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return fmt.Sprintf("%T", e)
}

// TestW10FlLockSites: the host-state lock is taken at the exported entry
// points and nowhere else, so that no helper a locked path calls takes it
// again. The write lock covers the paths that change UFW's files, the
// loaded rules or the Docker block; the read lock covers every path that
// reads them.
func TestW10FlLockSites(t *testing.T) {
	reads, writes := w10flSites(t)
	wantWrites := []string{"fw.reconcile", "module.Uninstall", "mutation.Apply", "mutation.Revert"}
	wantReads := []string{"fw.reconcileRead", "fw.snapshotCompare", "mutation.Observe", "mutation.Plan", "mutation.Snapshot", "mutation.Verify", "readLocked"}
	if !slices.Equal(writes, wantWrites) {
		t.Errorf("the write lock is taken in %q, want exactly %q", writes, wantWrites)
	}
	if !slices.Equal(reads, wantReads) {
		t.Errorf("the read lock is taken in %q, want exactly %q", reads, wantReads)
	}
}

// TestW10FlOneLockPerModule: every module instance has its own lock, and a
// firewall built without Module has none.
func TestW10FlOneLockPerModule(t *testing.T) {
	a, b := Module().(module), Module().(module)
	if a.f.mu == nil || b.f.mu == nil || a.f.mu == b.f.mu {
		t.Errorf("module locks %p and %p, want two distinct locks", a.f.mu, b.f.mu)
	}
	if productionFW().mu == nil {
		t.Error("productionFW has no lock")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/21 && export TMPDIR=/root/.cache/w10a-tmp/21 GOTOOLCHAIN=local
timeout 600 go test -race -count=1 -run 'TestW10Fl' ./internal/modules/firewall/
```

Expected: a build failure whose first error is `hostlock_test.go:135:4: f.mu undefined (type fw has no field or method mu)`. Quote it verbatim in the report. After Step 3 adds only the field, `TestW10FlReadsWaitForTheReload` fails on `the status read the host between the apply's file write and its reload` and `the plan read the host between ...`; keep that as the second RED if you stage the work that way.

- [ ] **Step 3: Implement**

1. `wire.go`: add the field `mu *sync.RWMutex` to `fw` with a doc comment saying why it exists (the host-state lock of ruling W10-R9; every copy of `fw` inside one module instance shares the pointer; a `fw` built without it takes no lock). `productionFW` sets `mu: new(sync.RWMutex)`. `Module()` already calls `productionFW()` once per instance, so two instances never share a lock. Add the `sync` import.
2. `hostlock.go` (new): the three helpers named under Interfaces.
   - `lockRead` and `lockWrite` return a no-op function when `f.mu` is nil; otherwise they take `RLock` or `Lock` and return `RUnlock` or `Unlock`. Callers write `defer f.lockRead()()`.
   - `readLocked` returns a function that holds the read lock for the whole call of `read` and returns its result unchanged.
3. `handler.go`, the first statement of each method of `mutation[In]`: `defer m.f.lockRead()()` in `Plan`, `Snapshot`, `Verify` and `Observe`; `defer m.f.lockWrite()()` in `Apply` and `Revert`. `Apply` therefore holds the write lock from `attach` to `recordPostHashes` and `finish`, which includes the last `ufw reload`. Do not add a lock to `fw.plan`, `fw.prepare`, `fw.run`, `revert`, `restore` or any other helper they call: `revert` and `restore` build their own `fw{d: d}`, which has no lock, so a lock there would also be silently absent.
4. `firewall.go`: in `resources`, register `readLocked(f, f.status)` for `firewall.status`; leave `f.snapshots` as it is. `docker_resource.go` and `exposure_resource.go`: `dockerResource` and `exposureResource` pass `readLocked(f, f.dockerStatus)` and `readLocked(f, f.exposureView)` to `core.DefineResource`. The unexported `status`, `dockerStatus` and `exposureView` stay lock-free, so the existing tests that call them directly are unchanged.
5. `snapshots.go`: `snapshotCompare` passes `core.DefineRead` a `readFunc` closure that takes `defer f.lockRead()()` and then calls `f.compareSnapshots`; `compareSnapshots` itself takes no lock.
6. `uninstall.go`: the first statement of `module.Uninstall` is `defer m.f.lockWrite()()`.
7. `reconcile.go`:
   - `reconcile` takes the write lock around the cleanup only: `unlock := f.lockWrite()`, `err := f.finishCleanups(ctx, env)`, `unlock()` (an explicit call, not a defer), keeping the existing error text `firewall: reconcile: finish the owned-chain cleanups: %w`.
   - Move the code from `readDockerConfig` through `f.dockerState` into `reconcileRead`, which starts with `defer f.lockRead()()` and returns the configuration, the loaded state and the Docker state. Where the old code ended the pass with `return nil` (the integration is off, UFW is disabled, DOCKER-USER holds a foreign rule) the new method returns `skip = true` and no error; the foreign-rule warning log stays in the method. Every error keeps its `firewall: reconcile: %w` text.
   - `reconcile` then returns early when `skip` is set, and otherwise calls `f.reconcileWith(ctx, tc, cfg, cur, st)` with no lock held.
8. Doc comments only where the why is not obvious (the field and `reconcileRead`); no comment names a plan, task or register row.

- [ ] **Step 4: Run test to verify it passes**

```sh
timeout 600 go test -race -count=1 -v -run 'TestW10Fl' ./internal/modules/firewall/
```

Expected: `ok`, every `TestW10Fl…` test and its subtests `PASS` (`TestW10FlReadsWaitForTheReload` takes about 2 s: each of its four cases waits 300 ms for the read that must not happen). A hang, or `deadlocked: the path takes the host-state lock twice`, means a helper takes the lock too; `TestW10FlLockSites` names the function.

Then the full constraint set, with real output recorded:

```sh
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 1500 go test -race -count=1 ./internal/modules/firewall/
timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/21 && go clean -testcache
```

Expected: every line `ok` or `?`; `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/hostlock.go internal/modules/firewall/hostlock_test.go internal/modules/firewall/wire.go \
  internal/modules/firewall/handler.go internal/modules/firewall/firewall.go internal/modules/firewall/docker_resource.go \
  internal/modules/firewall/exposure_resource.go internal/modules/firewall/snapshots.go internal/modules/firewall/reconcile.go \
  internal/modules/firewall/uninstall.go
git commit -m "fix(firewall): lock host-state reads and writes"
```

### Task 22: Origin check normalises the public host

**Files:**
- Modify: `internal/controller/listen/runtime.go` (`validListenerSpec`, imports), `internal/controller/listen/runtime_store.go` (`insertListener`, `scanListener`, a new helper, imports)
- Test: `internal/controller/listen/runtime_store_test.go`, `internal/controller/listen/runtime_test.go`
- Read only: `internal/controller/api/server.go` (`proxiedOrigin` compares `"https://"+p.PublicHost` exactly; it is the one place `PublicHost` is compared), `internal/controller/api/runtime.go` (copies `info.Spec.PublicHost` into `core.Proxied.PublicHost`)

**Interfaces:**
- Consumes: nothing from another task.
- Produces: `normalPublicHost(host string) string` in package `listen`, the one form of a public host: lower case, without trailing dots. `Runtime.Open`, `insertListener`, `getListener`, `listListeners` and the handler built by `Runtime.Start` all carry that form, so `api.proxiedOrigin` compares it with the browser's `Origin`. No other task consumes it.

**Items:** 169

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

What the tree already does, so the task adds only what is missing: `validListenerSpec` lower-cases the host and strips one trailing dot before `Open` records it (`TestRuntimeOpenNormalisesThePublicHost`). Two gaps remain. `insertListener` and `scanListener` use the host as given, so a row that holds another form (written by anything but `Open`) reaches `Start` and the API's exact `Origin` comparison unnormalised and refuses every same-origin unsafe request. And only one trailing dot is stripped, so `panel.example.com..` is recorded with a dot. The API file is frozen and stays as it is.

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/listen/runtime_store_test.go`:

```go
// TestW10OgStoreKeepsOnePublicHostForm: a public host is recorded and read
// in one form, lower case without a trailing dot, whatever form the caller
// gives it and whatever form a row already has.
func TestW10OgStoreKeepsOnePublicHostForm(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"Panel.Example.COM.", "PANEL.example.com", "panel.example.com.", "panel.example.com.."} {
		t.Run(host, func(t *testing.T) {
			db := openControllerDB(t)
			spec := rsSpec
			spec.PublicHost = host
			rsInsert(t, db, "L1", spec, testStart)
			var stored string
			if err := db.Read().QueryRowContext(ctx, `SELECT public_host FROM core_listeners WHERE id = 'L1'`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != "panel.example.com" {
				t.Errorf("insertListener stored %q, want panel.example.com", stored)
			}
			err := db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE core_listeners SET public_host = ? WHERE id = 'L1'`, host)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := getListener(ctx, db, "L1")
			if err != nil || got.Spec.PublicHost != "panel.example.com" {
				t.Errorf("getListener of a row holding %q = %+v, %v; want the public host panel.example.com", host, got.Spec, err)
			}
			open, err := listListeners(ctx, db, core.ListenerOpen)
			if err != nil || len(open) != 1 || open[0].Spec.PublicHost != "panel.example.com" {
				t.Errorf("listListeners of a row holding %q = %+v, %v; want the public host panel.example.com", host, open, err)
			}
		})
	}
}
```

Append to `internal/controller/listen/runtime_test.go`, and add `"database/sql"` to its import block after `"crypto/tls"`:

```go
// TestW10OgStartBindsRowsWithTheNormalHost: Start builds the handler of an
// open row with the public host in the one form the API's Origin check
// compares exactly, also when the row holds another form.
func TestW10OgStartBindsRowsWithTheNormalHost(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	rsInsert(t, f.db, "L1", rtSpec, testStart)
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE core_listeners SET public_host = ? WHERE id = 'L1'`, "Panel.Example.COM.")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.start(t)
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	if len(built) != 1 || built[0].Spec.PublicHost != "panel.example.com" {
		t.Errorf("handler built for %+v, want one listener with the public host panel.example.com", built)
	}
}
```

Extend `TestRuntimeOpenValidates` (same file): add this row to its table after the `"no public host"` row, so a host of dots only is refused as no host at all:

```go
		{"a public host of dots only", func(s *core.ListenerSpec) { s.PublicHost = ".." }},
```

Extend `TestRuntimeOpenNormalisesThePublicHost` (same file): add a fourth spelling to the host list of its `for` loop, so the result is `panel.example.com` for `"Panel.Example.COM.."` too:

```go
	for _, host := range []string{"Panel.Example.COM.", "PANEL.example.com", "panel.example.com.", "Panel.Example.COM.."} {
```

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/22 && export TMPDIR=/root/.cache/w10a-tmp/22 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Og|TestRuntimeOpenValidates|TestRuntimeOpenNormalisesThePublicHost' ./internal/controller/listen/
```

Expected: `FAIL`: `TestW10OgStoreKeepsOnePublicHostForm` (all four subtests: `insertListener stored "Panel.Example.COM.", want panel.example.com`), `TestW10OgStartBindsRowsWithTheNormalHost` (`handler built for [...PublicHost:Panel.Example.COM....], want one listener with the public host panel.example.com`), `TestRuntimeOpenValidates` (`a public host of dots only: Open = <nil>, want request.invalid_input`) and `TestRuntimeOpenNormalisesThePublicHost` (`want the public host panel.example.com`).

- [ ] **Step 3: Implement**

1. `runtime_store.go`: add `normalPublicHost(host string) string` with a doc comment saying it is the form the API's `Origin` check compares exactly: lower case with every trailing dot removed. `insertListener` stores `normalPublicHost(spec.PublicHost)`. `scanListener` sets `info.Spec.PublicHost = normalPublicHost(info.Spec.PublicHost)` after its checks, so `getListener` and `listListeners` (and through them `Start`, `Get` and the retry of pending binds) never return another form. Add the `strings` import.
2. `runtime.go`: in `validListenerSpec` replace the `strings.TrimSuffix(strings.ToLower(spec.PublicHost), ".")` line with `normalPublicHost(spec.PublicHost)`. The existing check `spec.PublicHost == ""` then also refuses a host of dots only. Remove the `strings` import if nothing else in the file uses it (the build says so). The function's doc comment is already right.
3. Do not edit `internal/controller/api`.

- [ ] **Step 4: Run test to verify it passes**

```sh
timeout 600 go test -race -count=1 ./internal/controller/listen/
```

Expected: `ok`. Then the full constraint set, with real output recorded:

```sh
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/22 && go clean -testcache
```

Expected: every line `ok` or `?`; `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/runtime.go internal/controller/listen/runtime_store.go \
  internal/controller/listen/runtime_test.go internal/controller/listen/runtime_store_test.go
git commit -m "fix(listen): normalise the stored public host"
```

### Task 23: DERP service user ownership

**Files:**
- Modify: `internal/modules/derp/server_install.go` (`Task`, `await`, `readCert`, `removeUser`), `internal/modules/derp/server_uninstall.go` (`uninstallFacts`, its documents, `facts`, `Plan`, `Apply`, imports)
- Test: `internal/modules/derp/server_install_test.go`, `internal/modules/derp/server_uninstall_test.go`

**Interfaces:**
- Consumes: nothing from another task. The tests reuse `inNewSetup`, `inDrive`, `inCoded`, `inInput`, `inUseradd`, `inIP`, `newDPHost`, `dpRun`, `unInstalled`, `unPlan`, `unApply`, `unEffects`, `unSame`, `unMaps`, `unGone`, `unInactive` and `installWantsLink`.
- Produces: `installHandler.readCert` gains a fifth result, the parse error (`(der []byte, notAfter time.Time, ok bool, parseErr, err error)`); `uninstallFacts.unitEnabled`; `unitDoc(active, enabled bool) core.StateDoc` and `uninstallRemoval.removed() core.StateDoc` in `server_uninstall.go`. The unit's effect document of `derp.server.uninstall` becomes `{"active":<bool>,"enabled":<bool>}`. No other task consumes them.

**Items:** 304, 307, 314

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

What each item means in the code:
- Row 307. `Task` records the ownership of `darkpanel-derp` and then runs `useradd`. A failed `useradd` leaves the record, and a user someone else creates later is then the module's: the uninstall's facts say `user` and its `userdel` deletes it. The record must follow a successful `useradd`, and an undo must delete only a user the module recorded. The undo path matters because a `useradd` that fails with exit 9 (the user appeared between the precheck and the command) leaves the effect intended, `Observe` then finds the user present, the engine settles the effect as made and compensates it with `Inverse`.
- Row 304. `await` treats a certificate that does not parse as not yet there, which is right while derper is writing it, but the timeout then says only `derper did not create its certificate`. The last parse error must be in the message.
- Row 314, the `server_uninstall.go` items only. (a) The unit's effect of `derp.server.uninstall` has the same `Before` and `After` (`{"active":false}`) for a loaded unit that is not running, although the removal still disables it. Enablement is the state that differs, and `Observe` of the install already reads it from the link `installWantsLink`. (b) `Apply` returns a failed read of the facts as a plain error, although nothing changed yet; `Plan` marks the same failure definite.

- [ ] **Step 1: Write the failing test**

Edit 1, `TestInTaskInstalls` in `server_install_test.go`: the first entry of `wantCalls` now sees no record when `useradd` runs:

```go
				{inUseradd + " (recorded as owned: false)", "derp.user:?darkpanel-derp intended"},
```

Edit 2, `TestInTaskFailedUseraddLeavesItsEffectIntended` in the same file: append before the closing brace of the function:

```go
	if owned, err := platform.NewOwnership(s.h.db).Owns(context.Background(), ID, server.ServiceUser); err != nil || owned {
		t.Errorf("the user is recorded as the module's after a failed useradd: %v (error %v)", owned, err)
	}
```

New tests, appended to `server_install_test.go`:

```go
// TestW10DuTaskReportsAnUnparsableCertificate: a certificate that does not parse is waited out, as derper may be
// writing it, and when the wait runs out the error names why it does not parse.
func TestW10DuTaskReportsAnUnparsableCertificate(t *testing.T) {
	crtPath := server.CertDir + "/" + inIP + ".crt"
	t.Run("a certificate that never parses", func(t *testing.T) {
		s := inNewSetup(t)
		s.active()
		s.h.reads[crtPath] = []byte("not a certificate")
		var err error
		inDrive(t, s.h, func() {
			_, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
		})
		coded := inCoded(t, err, errcode.DERPVerifyFailed)
		for _, want := range []string{"certificate", "no CERTIFICATE block in the PEM data"} {
			if !strings.Contains(coded.Detail, want) {
				t.Errorf("detail %q, want it to contain %q", coded.Detail, want)
			}
		}
	})
	t.Run("a half-written certificate that parses later", func(t *testing.T) {
		s := inNewSetup(t)
		s.active()
		der, _ := s.certFile(t)
		reads := 0
		readFile := s.d.d.readFile
		s.d.d.readFile = func(name string) ([]byte, error) {
			if name == crtPath {
				if reads++; reads < 4 {
					return []byte("-----BEGIN CERTIFICATE-----\nMIIB"), nil
				}
			}
			return readFile(name)
		}
		var out json.RawMessage
		var err error
		inDrive(t, s.h, func() {
			out, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
		})
		var got ServerInstalled
		if err != nil || json.Unmarshal(out, &got) != nil || got.CertName != server.Pin(der) {
			t.Errorf("output %s (error %v), want the pin of the certificate that parsed in the end", out, err)
		}
	})
}

// TestW10DuInverseLeavesAUserItDidNotRecord: the undo of a user effect deletes the user only when the module
// recorded it, so a user someone else created while the install ran is not deleted.
func TestW10DuInverseLeavesAUserItDidNotRecord(t *testing.T) {
	s := inNewSetup(t)
	s.h.users[server.ServiceUser] = [2]int{900, 900}
	effect := core.Effect{Kind: core.EffectCreate, Resource: "derp.user:" + server.ServiceUser}
	if _, err := s.d.serverInstall().Handler.Inverse(context.Background(), s.env, effect); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.h.users[server.ServiceUser]; !ok || len(s.h.ran) != 0 {
		t.Errorf("user kept: %v, commands run %v, want the user kept and no userdel", ok, s.h.ran)
	}
}
```

Edit 3, `server_uninstall_test.go`: add `"errors"`, `"fmt"` and `"slices"` to its import block (alphabetical, among `"encoding/json"`, `"reflect"`, `"strings"`).

Edit 4, `unInstalled` in the same file: a finished install leaves the unit enabled. Insert this line before `h.users[server.ServiceUser] = [2]int{990, 990}`:

```go
	h.reads[installWantsLink] = []byte{} // the fake systemd makes no enablement link
```

Edit 5, `unDoc` in the same file: replace the function and its comment with:

```go
// unDoc is the state document of resource: the unit's holds its active and enabled flags, every other one its
// presence.
func unDoc(resource string, on bool) string {
	if resource == unUnitRes {
		return fmt.Sprintf(`{"active":%t,"enabled":%t}`, on, on)
	}
	return fmt.Sprintf(`{"present":%t}`, on)
}
```

Edit 6, `TestUnObserve` in the same file: after the `unApply` call and before `check(false)` the fake systemd has removed the link `systemctl disable` removes:

```go
	delete(h.reads, installWantsLink) // systemctl disable removed the enablement link
```

New tests, appended to `server_uninstall_test.go`:

```go
// TestW10DuUninstallLeavesAUserItDidNotCreate: an install whose useradd failed records no ownership, so the user
// someone else creates afterwards is not the module's to delete.
func TestW10DuUninstallLeavesAUserItDidNotCreate(t *testing.T) {
	ctx := context.Background()
	s := inNewSetup(t)
	s.h.script(inUseradd, dpRun{res: core.CmdResult{ExitCode: 9, Stderr: []byte("useradd: cannot lock /etc/passwd")}})
	if _, err := s.d.serverInstall().Handler.Task(ctx, s.env, inInput("ip", inIP, nil)); err == nil {
		t.Fatal("the install succeeded although useradd failed")
	}
	s.h.users[server.ServiceUser] = [2]int{900, 900}
	unMaps(s.h)
	s.h.units.States[server.UnitName] = unGone
	if out := unPlan(t, s.h); len(out.Diff) != 0 {
		t.Errorf("plan %+v, want nothing to remove", out.Diff)
	}
	out, err := unApply(t, s.h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.h.users[server.ServiceUser]; !ok || len(out.Removed) != 0 ||
		slices.ContainsFunc(s.h.ran, func(v []string) bool { return v[0] == installUserdel }) {
		t.Errorf("user kept: %v, removed %v, commands run %q, want the user kept and no userdel", ok, out.Removed, s.h.ran)
	}
}

// w10duFailingUnits is a systemd that cannot be reached.
type w10duFailingUnits struct{ core.Units }

func (w10duFailingUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{}, errors.New("w10du: systemd is unreachable")
}

// TestW10DuUninstallApplyReadFailureIsDefinite: a failed read of the facts changed nothing, so Apply reports it
// as definite, as Plan does.
func TestW10DuUninstallApplyReadFailureIsDefinite(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	env := h.env("c1")
	env.Units = w10duFailingUnits{env.Units}
	_, err := h.dp().serverUninstall().Handler.Apply(context.Background(), env, json.RawMessage(`{}`))
	if err == nil || !core.IsDefinite(err) {
		t.Errorf("Apply = %v, want a definite error", err)
	}
	if effects := unEffects(t, h); len(effects) != 0 {
		t.Errorf("effects %+v, want none journaled", effects)
	}
}

// TestW10DuUninstallUnitEffect: the unit's effect changes the unit's document also when the unit is loaded but not
// running, and a unit that is neither running nor enabled has nothing to remove.
func TestW10DuUninstallUnitEffect(t *testing.T) {
	t.Run("a loaded unit that is not running", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		if _, err := unApply(t, h, nil); err != nil {
			t.Fatal(err)
		}
		e := unEffects(t, h)[0]
		if e.Resource != unUnitRes || e.After == nil || !unSame(t, e.Before.State, `{"active":false,"enabled":true}`) ||
			!unSame(t, e.After.State, `{"active":false,"enabled":false}`) {
			t.Errorf("effect %s: before %s, after %+v, want enabled before and not after, neither running", e.Resource, e.Before.State, e.After)
		}
	})
	t.Run("a loaded unit that is neither running nor enabled", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		delete(h.reads, installWantsLink)
		if out := unPlan(t, h); !reflect.DeepEqual(out.Diff, unFullDiff[1:]) {
			t.Errorf("diff\n got %+v\nwant %+v", out.Diff, unFullDiff[1:])
		}
		if _, err := unApply(t, h, nil); err != nil {
			t.Fatal(err)
		}
		if want := []string{"reload"}; !reflect.DeepEqual(h.units.Calls, want) {
			t.Errorf("unit calls %q, want %q", h.units.Calls, want)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/23 && export TMPDIR=/root/.cache/w10a-tmp/23 GOTOOLCHAIN=local
timeout 900 go test -count=1 -run 'TestW10Du|TestInTaskInstalls|TestInTaskFailedUseradd|TestUn' ./internal/modules/derp/
```

Expected: `FAIL` for `TestInTaskInstalls` (both subtests: the useradd call reads `recorded as owned: true`), `TestInTaskFailedUseraddLeavesItsEffectIntended`, `TestW10DuTaskReportsAnUnparsableCertificate/a_certificate_that_never_parses`, `TestW10DuInverseLeavesAUserItDidNotRecord`, `TestUnApplyRemovesEverything`, `TestUnObserve`, `TestW10DuUninstallLeavesAUserItDidNotCreate`, `TestW10DuUninstallApplyReadFailureIsDefinite` and both subtests of `TestW10DuUninstallUnitEffect`. `TestW10DuTaskReportsAnUnparsableCertificate/a_half-written_certificate_that_parses_later` passes already: it guards the wait the fix must keep.

- [ ] **Step 3: Implement**

1. `server_install.go`, `Task`, the closure that creates the user: run `useradd` first (same vector, `installRun`), return its error as it is, and only then record `core.OwnedEntry{Path: server.ServiceUser, Module: ID, Kind: core.OwnedUser, CreatedAt: env.Clock.Now()}` with `platform.NewOwnership(env.DB).Record`, returning its error. Add a comment of the why: the record is the only proof the module created the user, so recording before `useradd` would make a user created later by someone else the module's. The effect, its recovery identity and the order of the other effects do not change.
2. `server_install.go`, `removeUser` (the install's `Inverse` for the user): after `lookupUser`, read `platform.NewOwnership(env.DB).Owns(ctx, ID, server.ServiceUser)`; run `userdel` only when the user exists and the module recorded it; `Forget` in every case, as now. Update its doc comment. A crash between a successful `useradd` and the record therefore leaves a user nobody deletes, which the next install's precheck refuses as foreign: the safe side.
3. `server_install.go`, `readCert` and `await`: `readCert` returns the parse error of a certificate that does not parse as its fourth result (nil when the file is absent or parses, and for a read error, which stays the fifth result). `await` keeps the parse error of its latest read in a variable (set to nil when a later read finds the file absent) and, when the deadline passes with the unit active, appends `; the file it wrote does not parse: <error>` to `derper did not create its certificate within <certWait>`. The unit-not-active message and every other path are unchanged.
4. `server_uninstall.go`, enablement:
   - `uninstallFacts` gains `unitEnabled bool`: derper's unit is loaded and `h.d.d.readFile(installWantsLink)` finds its link, by the rule of `installHandler.exists` (any error but `fs.ErrNotExist` counts as present). `facts` sets it only when the unit is loaded. Add the `errors` and `io/fs` imports.
   - `exists(uninstallUnit)` becomes `f.unitActive || f.unitEnabled`: a loaded unit that is neither running nor enabled has nothing to stop or disable.
   - `unitDoc(active, enabled bool)` builds `{"active":<bool>,"enabled":<bool>}`; `uninstallFacts.doc` uses it for the unit with `unitActive` and `unitEnabled`; `uninstallRemoval.doc(v bool)` now always builds `{"present":<bool>}`; new `uninstallRemoval.removed()` returns `unitDoc(false, false)` for the unit and `doc(false)` for the others, and `Apply` uses it as the effect's `After` and as the document it records made.
   - `Plan`'s state hash also lists `enabled=<bool>`. The plan text of the unit (`stop and disable` / `disable`) is unchanged.
5. `server_uninstall.go`, `Apply`: return `core.Definite(err)` for a failed `h.facts` read before anything changed. Later errors are unchanged.
6. Do not change `server_uninstall.go`'s `removeUser`: it is reached only when the ownership record exists.

- [ ] **Step 4: Run test to verify it passes**

```sh
timeout 900 go test -count=1 -run 'TestW10Du|TestIn|TestUn' ./internal/modules/derp/
```

Expected: `ok`. Then the full constraint set, with real output recorded (the derp package takes about four minutes):

```sh
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 1500 go test -race -count=1 ./internal/modules/derp/
timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10a-tmp/23 && go clean -testcache
```

Expected: every line `ok` or `?`; `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/server_install.go internal/modules/derp/server_uninstall.go \
  internal/modules/derp/server_install_test.go internal/modules/derp/server_uninstall_test.go
git commit -m "fix(derp): record the service user after useradd"
```

### Task 24: DERP artifact and derper build checks

**Files:**
- Modify: `internal/modules/derp/artifact/manifest.go`, `release/releasetool/derper.go`, `internal/modules/derp/deps.go` (the import and the two uses of `artifact.Source` and `artifact.Fetch`)
- Create (moved with `git mv`): `internal/modules/derp/artifact/download/fetch.go` (from `artifact/fetch.go`) and `internal/modules/derp/artifact/download/fetch_test.go` (from `artifact/fetch_test.go`); `artifact/fetch.go` and `artifact/fetch_test.go` no longer exist
- Test: `internal/modules/derp/artifact/manifest_test.go`, `internal/modules/derp/artifact/download/fetch_test.go`, `release/releasetool/releasetool_test.go`

**Interfaces:**
- Consumes: nothing from another task. Existing: `update.ParseVersion`, `update.DownloadAsset`, `update.Download`, `artifact.ParseManifest`, `artifact.Manifest`, `artifact.BinaryName`.
- Produces: package `internal/modules/derp/artifact/download` with `Source` and `Fetch` exactly as `artifact.Source` and `artifact.Fetch` were (same fields, same signature, same errors apart from the one prefix fix below); package `artifact` without `Source` and `Fetch` and without any import of `internal/update`; `derperBuildEnv(environ []string, arch string) []string` in `release/releasetool`. No other task consumes them.

**Items:** 308, 316, 317

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

This task does not edit `internal/update/`: row 316's last item (`DownloadAsset` repeating `Download`'s checksum steps) is duplication with no defect behind it, deferred to post-v1 by W10-R2.

What each item means in the code:
- Row 316. `ParseManifest` decodes into a map, so a member named twice (and an arch named twice inside `artifacts`) keeps the last one silently. `validate` checks each arch's presence and hash in one loop, so a manifest with a bad `amd64` hash and no `arm64` is refused for the hash; the brief lists the missing arch first. `Fetch` wraps a `ParseManifest` error as `artifact: release X: artifact: ...`, a doubled prefix.
- Row 317. `fileSHA256` refuses an empty file, so one empty file in a release directory fails the whole `checksums.txt` rewrite although `sha256sum` hashes it; the empty-build refusal belongs in `runDerper`. `releasetool` imports `artifact`, which imports `internal/update` (for `ParseVersion` in `validate` and for the download in `Fetch`), and `internal/update` links `internal/core`, the store, the engine and the platform into every release tool run.
- Row 308. `buildDerper` runs its `go` commands with the runner's whole environment, so a runner's `GOFLAGS`, `GOPROXY`, `GOSUMDB=off`, `GONOSUMDB`, `GOENV` or `GOWORK` decides how the derper source is fetched and verified. This task pins the environment. It does not add a `go.sum` file: one exists only after a release build has resolved `tailscale.com` at `DerperVersion` with network access; until then the checksum database (`GOSUMDB`) verifies every module the build fetches.

- [ ] **Step 1: Write the failing test**

Edit `internal/modules/derp/artifact/manifest_test.go`:

1. Add `"go/parser"` and `"go/token"` to its import block, before `"os"`.
2. `TestMfParseManifestRefuses`: add these rows after the row `"a hash that is not a string"`:

```go
		{"a repeated member", mfDoc(mfDarkpanel, mfDerper, `"derperVersion":"v1.105.0"`, mfArtifacts), `"derperVersion" twice`},
		{"a repeated architecture", mfDoc(mfDarkpanel, mfDerper, arts(amd+`,`+arm+`,"amd64":"`+mfHashARM+`"`)), `"amd64" twice`},
		{"a repeated architecture spelled with escapes", mfDoc(mfDarkpanel, mfDerper, arts(amd+`,`+arm+`,"amd\u0036\u0034":"`+mfHashARM+`"`)), `"amd64" twice`},
		{"a bad hash and a missing architecture", mfDoc(mfDarkpanel, mfDerper, arts(`"amd64":"`+mfHashAMD[:63]+`"`)), `lacks the architecture "arm64"`},
```

3. `TestMfEncodeValidatesTheManifest`: add this row after the row `"nil artifacts"`:

```go
		{"a bad hash and a missing arch", func(m *Manifest) { delete(m.Artifacts, "arm64"); m.Artifacts["amd64"] = "xyz" }, `lacks the architecture "arm64"`},
```

4. Append:

```go
// TestW10DaPackageImportsNoUpdater: the package the release tool imports for derper's names and manifest does not
// import the updater, which would link the agent's runtime into every release tool run.
func TestW10DaPackageImportsNoUpdater(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(path, "github.com/darkraise/darkpanel/internal/update") {
				t.Errorf("%s imports %s", e.Name(), path)
			}
		}
	}
}
```

Move the Fetch tests: `git mv internal/modules/derp/artifact/fetch_test.go internal/modules/derp/artifact/download/fetch_test.go` (the directory is new). In the moved file change the package clause to `package download`, add `"strconv"` (between `"slices"` and `"strings"`) and `"github.com/darkraise/darkpanel/internal/modules/derp/artifact"` (between the `errcode` and `update` imports) to the imports, and qualify every use of the exported names of `artifact`: `Manifest{` literals, `ManifestName`, `BinaryName`, `DerperVersion` and `Arches` become `artifact.Manifest{`, `artifact.ManifestName` and so on. Struct field names inside a literal (`DarkpanelVersion:`, `DerperVersion:`, `Artifacts:`) stay unqualified; the compiler names anything missed. Then append:

```go
// TestW10DaFetchNamesThePackageOnce: a manifest that does not parse is reported with the package name once, and
// names the release.
func TestW10DaFetchNamesThePackageOnce(t *testing.T) {
	signer := feNewSigner(t)
	srv := newFeServer(t)
	fePublish(t, srv, signer, feDarkpanel, artifact.DerperVersion)
	srv.put("v"+feDarkpanel+"/"+artifact.ManifestName, []byte(`{"surprise":1}`))
	srv.sign(signer, feDarkpanel, "stable")
	_, _, err := Fetch(context.Background(), feSource(srv, signer.key), feDarkpanel, artifact.DerperVersion, "amd64")
	if err == nil {
		t.Fatal("Fetch accepted a manifest with an unknown member")
	}
	if got := strings.Count(err.Error(), "artifact:"); got != 1 || !strings.HasPrefix(err.Error(), "artifact:") ||
		!strings.Contains(err.Error(), feDarkpanel) || !strings.Contains(err.Error(), "surprise") {
		t.Errorf("error %q, want one leading \"artifact:\", the release %s and the unknown member", err, feDarkpanel)
	}
}

// TestW10DaManifestVersionGrammarMatchesTheUpdater: derper.json accepts a darkpanelVersion exactly when the
// updater accepts it as a version, although the manifest no longer imports the updater.
func TestW10DaManifestVersionGrammarMatchesTheUpdater(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, v := range []string{
		"1.4.0", "0.0.0", "1.5.0-beta.1", "1.5.0-rc-1", "01.4.0", "1.4", "v1.4.0", "1.4.0+build", "1.4.0-",
		"1.4.0-beta..1", "dev", "", "18446744073709551616.0.0", "18446744073709551615.0.0",
	} {
		doc := `{"darkpanelVersion":` + strconv.Quote(v) + `,"derperVersion":"v1.104.1","artifacts":{"amd64":"` + hash + `","arm64":"` + hash + `"}}`
		_, upErr := update.ParseVersion(v)
		_, mfErr := artifact.ParseManifest([]byte(doc))
		if (upErr == nil) != (mfErr == nil) {
			t.Errorf("version %q: the updater says %v, the manifest says %v", v, upErr, mfErr)
		}
	}
}
```

Edit `release/releasetool/releasetool_test.go`: append (`reflect` and `strings` are already imported there):

```go
// TestW10DaChecksumsCoverAnEmptyFile: the rewrite of checksums.txt lists a file with no content by its hash, as
// sha256sum does, instead of failing on it.
func TestW10DaChecksumsCoverAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"empty.txt": "", "full.txt": "data", "full.txt.minisig": "signature"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil { //nolint:gosec // G306: test files
			t.Fatal(err)
		}
	}
	if err := rewriteChecksums(dir); err != nil {
		t.Fatalf("rewriteChecksums: %v", err)
	}
	want := drSum(nil) + "  empty.txt\n" + drSum([]byte("data")) + "  full.txt\n"
	if got := string(drRead(t, filepath.Join(dir, "checksums.txt"))); got != want {
		t.Errorf("checksums.txt =\n%s\nwant\n%s", got, want)
	}
}

// TestW10DaRunDerperRefusesABuildThatWritesNothing: a builder that leaves an empty file or no file is an error
// naming the architecture, and no manifest is written.
func TestW10DaRunDerperRefusesABuildThatWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write bool
		want  string
	}{
		{"an empty file", true, "empty"},
		{"no file", false, "no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			build := func(_ context.Context, _, _, dst string) error {
				if tc.write {
					return os.WriteFile(dst, nil, 0o755) //nolint:gosec // G306: a test binary
				}
				return nil
			}
			err := runDerper(context.Background(), []string{"1.4.0", out}, &bytes.Buffer{}, build)
			if err == nil || !strings.Contains(err.Error(), "amd64") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDerper = %v, want an error naming amd64 and %q", err, tc.want)
			}
			drMissing(t, filepath.Join(out, artifact.ManifestName))
		})
	}
}

// TestW10DaDerperBuildEnv: the go commands that build derper get a fixed environment, whatever the runner's:
// the checksum database and the public module proxy, no Go configuration file or workspace, no flags, and none
// of the runner's other variables.
func TestW10DaDerperBuildEnv(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/home/runner", "GOFLAGS=-mod=vendor", "GOPROXY=off", "GOSUMDB=off", "GONOSUMDB=*",
		"GOINSECURE=*", "GOPRIVATE=*", "GONOPROXY=*", "GOENV=/tmp/evil", "GOWORK=/tmp/go.work", "GOOS=windows",
		"CGO_ENABLED=1", "GITHUB_TOKEN=placeholder",
	}
	got := map[string]string{}
	for _, kv := range derperBuildEnv(environ, "arm64") {
		name, value, _ := strings.Cut(kv, "=")
		if _, twice := got[name]; twice {
			t.Errorf("%s is set twice", name)
		}
		got[name] = value
	}
	want := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/runner", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "arm64",
		"GOFLAGS": "", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", "GOENV": "off", "GOWORK": "off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environment %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/24 && export TMPDIR=/root/.cache/w10a-tmp/24 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestMf|TestW10Da' ./internal/modules/derp/artifact/
timeout 600 go test -count=1 ./internal/modules/derp/artifact/download/
timeout 600 go test -count=1 -run 'TestW10Da' ./release/releasetool/
```

Expected:
- `artifact`: `FAIL` for `TestMfParseManifestRefuses` (the four new rows), `TestMfEncodeValidatesTheManifest/a_bad_hash_and_a_missing_arch` and `TestW10DaPackageImportsNoUpdater` (`manifest.go imports github.com/darkraise/darkpanel/internal/update` and the same for `fetch.go`).
- `download`: a build failure, `undefined: Fetch` and `undefined: Source`, because only the moved test file is in the directory.
- `releasetool`: a build failure, `undefined: derperBuildEnv`.

- [ ] **Step 3: Implement**

1. `artifact/manifest.go`:
   - Add `checkNoRepeats(b []byte) error`, called first in `ParseManifest`. It walks `b` with `json.Decoder.Token`: when the document is an object, a member name seen twice in it is the error `artifact: derper.json has the member "<name>" twice`, and a name seen twice inside the value of `artifacts` (when that is an object) is `artifact: derper.json artifacts has the architecture "<arch>" twice`. Names are compared as decoded, so `"amd\u0036\u0034"` equals `"amd64"`. A document that is not an object, or that the walk cannot read, returns nil and is left to the existing decoding, whose errors stay as they are. Every error starts with `artifact:` (`FuzzParseManifest` checks that).
   - `validate`: after the unknown-arch loop, one loop over `Arches` refuses the first missing arch (`artifacts lacks the architecture "<arch>"`), and only then a second loop refuses the first hash that is not 64 lower-case hex digits. A missing arch is thus always named before a bad hash.
   - Remove the import of `internal/update`. Validate `darkpanelVersion` with a package-level `semverPattern` copied verbatim from `versionPattern` in `internal/update/version.go` and by requiring that the three numbers also parse with `strconv.ParseUint(n, 10, 64)`, which is all `update.ParseVersion` does; the error text stays `darkpanelVersion "<v>" is not a semantic version without a leading v`. `TestW10DaManifestVersionGrammarMatchesTheUpdater` keeps the two in step.
2. Move `fetch.go` with `git mv internal/modules/derp/artifact/fetch.go internal/modules/derp/artifact/download/fetch.go`. Change its package clause to `download` and give it a package doc comment (it reads a derper build from a Darkpanel release); import `internal/modules/derp/artifact` and qualify `Arches`, `ParseManifest`, `ManifestName` and `BinaryName`; leave `Source`, `Fetch` and the limits as they are. Fix the doubled prefix: the manifest parse error is returned as `fmt.Errorf("%w (release %s)", err, dpVersion)`, so it starts with `artifact:` once and names the release.
3. `internal/modules/derp/deps.go`: import `internal/modules/derp/artifact/download` in place of `artifact` if `artifact` is no longer used there (the compiler says), use `download.Source` and `download.Fetch`, and update the comment of the `fetch` field.
4. `release/releasetool/derper.go`:
   - `fileSHA256` no longer refuses an empty file (it hashes the bytes it reads); fix its doc comment.
   - `runDerper`, after each `build` call and before `fileSHA256`: `os.Stat` the destination (a missing file keeps the stat error), and a size of 0 is `derper <arch>: the build wrote an empty <dst>`. Put the same `//nolint:gosec // G703:` line above the stat that the neighbouring `os.Stat` calls carry.
   - Add `derperBuildPassThrough` (`PATH`, `HOME`, `TMPDIR`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOROOT`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `http_proxy`, `https_proxy`, `no_proxy`, `SSL_CERT_FILE`, `SSL_CERT_DIR`) and `derperBuildEnv(environ, arch)`: the variables of `environ` in that list, in order, followed by `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=<arch>`, `GOFLAGS=` (empty), `GOPROXY=https://proxy.golang.org`, `GOSUMDB=sum.golang.org`, `GOENV=off` and `GOWORK=off`.
   - `buildDerper` sets `cmd.Env` from `derperBuildEnv(os.Environ(), arch)` instead of `append(os.Environ(), ...)`.

- [ ] **Step 4: Run test to verify it passes**

```sh
timeout 600 go test -count=1 ./internal/modules/derp/artifact/... ./release/...
! go list -deps ./release/releasetool | grep -q 'darkpanel/internal/update'
timeout 900 go test -count=1 ./internal/modules/derp/
```

Expected: `ok` for each package; the `go list` check prints nothing and exits 0 (the release tool no longer links the updater). Then the full constraint set, with real output recorded:

```sh
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 1500 go test -race -count=1 ./internal/modules/derp/artifact/... ./release/...
timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
timeout 120 bash scripts/fuzz.test.sh
rm -rf /root/.cache/w10a-tmp/24 && go clean -testcache
```

Expected: every line `ok` or `?`; `0 issues.`; `go mod tidy -diff` prints nothing; `scripts/fuzz.test.sh` exits 0 (`FuzzParseManifest` stays in `artifact`).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/artifact internal/modules/derp/deps.go \
  release/releasetool/derper.go release/releasetool/releasetool_test.go
git commit -m "fix(derp): harden artifact and derper checks"
```

### Task 25: Tailscale latency probe URL checks

**Files:**
- Modify: `internal/modules/tailscale/latency.go` (`latencyProbeURLs`, `latencyHostValid`)
- Test: `internal/modules/tailscale/latency_test.go`
- Read only: `docs/dforge/notes/v1-w4-final-review-followups.md`, section "Security finding parked during execution" (the fix it describes)

**Interfaces:**
- Consumes: nothing from another task.
- Produces: nothing another task consumes. `latencyProbeURLs(r *tslocal.DERPRegion) []string` keeps its signature and its output for every host it accepts today.

**Items:** 301, 318

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

The parked finding says the DERP map's `HostName` and `DERPPort` reach the probe URL unchecked. Most of its fix is already in `latency.go` and pinned by rows of `TestLtProbeURLs`: nodes whose host holds a slash, question mark, hash, at sign, backslash, white space, control character or a colon outside an IPv6 literal are skipped, the URL is built with `url.URL`, and a port outside 0 to 65535 is skipped. What is left is row 318: `latencyHostValid` only has a deny-list, so a host with brackets (`a[1].example.net`), a percent sign (`a%2fb.example.net`) or non-ASCII characters (`é.example.net`) still becomes a probe URL, and `latencyProbeURLs` parses an IP host twice (once in `latencyHostValid`, once for the IPv6 bracket decision). This task replaces the deny-list with an allow-list and parses once. The tailnet's DERP map is attacker-influenced input, so an allow-list is the safe form: a host that is not an IP literal and not made of host-name characters is never probed.

- [ ] **Step 1: Extend the test `internal/modules/tailscale/latency_test.go::TestLtProbeURLs`**

Add these three rows at the end of the `tests` table of `TestLtProbeURLs` (after the row "an IPv6 literal with a zone is skipped"):

```go
		{"brackets, percent signs and non-ASCII characters are skipped", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a[1].example.net"},
			{Name: "1b", HostName: "a].example.net"},
			{Name: "1c", HostName: "a%2fb.example.net"},
			{Name: "1d", HostName: "a%41.example.net"},
			{Name: "1e", HostName: "é.example.net"},
			{Name: "1f", HostName: "例え.example.net"},
			{Name: "1g", HostName: "ok.example.net"},
		}, []string{ltURL("ok.example.net")}},
		{"letters, digits, hyphens and dots are kept as they are, case included", []*tslocal.DERPNode{
			{Name: "1a", HostName: "derp-1.Example.NET"},
			{Name: "1b", HostName: "xn--e1afmkfd.example.net"},
			{Name: "1c", HostName: "derp1"},
		}, []string{ltURL("derp-1.Example.NET"), ltURL("xn--e1afmkfd.example.net"), ltURL("derp1")}},
		{"an empty host name is skipped", []*tslocal.DERPNode{
			{Name: "1a", HostName: ""},
			{Name: "1b", HostName: "ok.example.net"},
		}, []string{ltURL("ok.example.net")}},
```

Then add this new test at the end of the file. It follows the read end to end, so it pins that an unsafe host never reaches the HTTPS probe, which is what the parked finding is about:

```go
// TestW10LpUnsafeHostIsNeverProbed: a DERP node whose host name could change the meaning of the probe URL gets no
// HTTPS probe at all, and its region reports that it has no DERP node.
func TestW10LpUnsafeHostIsNeverProbed(t *testing.T) {
	h := newTSHost(t)
	var nodes []*tslocal.DERPNode
	for _, host := range []string{
		"a.example.net/admin", "a.example.net:8080", "a.example.net?x=1", "user@a.example.net",
		"a%2fb.example.net", "a[1].example.net", "é.example.net",
	} {
		nodes = append(nodes, &tslocal.DERPNode{Name: host, HostName: host})
	}
	h.local.DERP = ltMap(ltRegion(1, "x", "Unsafe", nodes...))
	f := ltNew()

	got, _ := ltRead(t, h, f)

	if len(got.Regions) != 1 || got.Regions[0].HTTPS != (Measurement{Reason: "the region has no DERP node"}) {
		t.Errorf("regions %+v, want one region whose HTTPS measurement says it has no DERP node", got.Regions)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for target, n := range f.calls {
		if strings.HasPrefix(target, "https://") {
			t.Errorf("%d HTTPS probe(s) of %s, want none", n, target)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/25 && export TMPDIR=/root/.cache/w10a-tmp/25 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestLtProbeURLs|TestW10LpUnsafeHostIsNeverProbed' ./internal/modules/tailscale/`
Expected: FAIL. `TestLtProbeURLs` reports the row "brackets, percent signs and non-ASCII characters are skipped" with a `urls [...]` list longer than `[https://ok.example.net/derp/latency-check]`, and `TestW10LpUnsafeHostIsNeverProbed` reports HTTPS probes of URLs built from those hosts (for example the host with the percent sign). The other two new rows already pass. Quote the output verbatim in the report.

- [ ] **Step 3: Implement**

In `internal/modules/tailscale/latency.go`:

1. Replace `latencyHostValid` with one helper that classifies a node's host name once and returns the host part of the URL, for example `latencyURLHost(name string, port int) (host string, ok bool)`. Remove `latencyHostValid`, and remove the `unicode` and `strings` imports if nothing else in the file uses them.
2. The helper parses `name` with `netip.ParseAddr` exactly once.
   - A parsed address with a zone is refused (`ok` false), as today.
   - A parsed IPv4 address is the host as it is. A parsed IPv6 address is the host in brackets.
   - A name that is not an IP address is accepted only when it is not empty and every byte is an ASCII letter, an ASCII digit, `-` or `.`. This allow-list replaces the character deny-list, so brackets, percent signs, non-ASCII characters, slashes, question marks, hash signs, at signs, backslashes, white space, control characters and colons are all refused by the one rule. Case is preserved.
3. The port rule is unchanged: a port below 0 or above 65535 skips the node, 0 and 443 leave the port out of the URL, and any other port is appended after the host with `net.JoinHostPort` semantics (an IPv6 host stays in brackets: `https://[2001:db8::2]:8443/derp/latency-check`).
4. `latencyProbeURLs` keeps skipping nil nodes and `STUNOnly` nodes, keeps node order, and still builds the URL with `url.URL{Scheme: "https", Host: host, Path: latencyCheckPath}.String()`. It calls the helper once per node.
5. Update the doc comments of `latencyProbeURLs` and the helper: the DERP map comes from the tailnet's policy, so only an IP literal without a zone or a host name of letters, digits, hyphens and dots is probed.

Every row that `TestLtProbeURLs` holds today must stay green unchanged.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

Run:
```sh
mkdir -p /root/.cache/w10a-tmp/25 && export TMPDIR=/root/.cache/w10a-tmp/25 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestLtProbeURLs|TestW10LpUnsafeHostIsNeverProbed' -v ./internal/modules/tailscale/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```
Expected: the two tests PASS; `go vet` and `go mod tidy -diff` print nothing; every line of `go test` is `ok` or `?`; golangci-lint prints `0 issues.`. Then `rm -rf /root/.cache/w10a-tmp/25 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/latency.go internal/modules/tailscale/latency_test.go
git commit -m "fix(tailscale): restrict DERP probe host names"
```

### Task 26: NPM client limits and public-check denials

**Files:**
- Modify: `internal/modules/npm/npmapi/client.go` (`do`, `apiError`, the const block)
- Modify: `internal/modules/npm/controller_checks.go` (`ctlPortOpen`, `ctlDockerMajor`)
- Test: `internal/modules/npm/npmapi/client_test.go`
- Test: `internal/modules/npm/controller_checks_test.go`

**Interfaces:**
- Consumes: nothing from another task.
- Produces: nothing another task consumes. `npmapi` gains the unexported constant `answerBodyLimit` (4 MiB) and keeps `errorBodyLimit` (64 KiB); the `Client` methods keep their signatures. `ctlPortOpen` and `ctlDockerMajor` keep theirs.

**Items:** 331, 356

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

Row 331: a successful NPM answer is read without a size limit (`json.NewDecoder(res.Body).Decode`, and `io.Copy(io.Discard, ...)` when no output is wanted), so a compromised NPM can exhaust the agent's memory. A refusal body over 64 KiB is left partly unread and a non-2xx response is closed undrained, so the connection is not reused. Row 356, the parts of this task: `ctlPortOpen` ignores `DockerBinding.Denied`, so the public check counts a binding that denies sources as open to every source; and `ctlDockerMajor` treats a Docker version string with no dot (a bare major such as `29`) as not recognised.

Decisions this task makes:
- The limit for a successful answer is 4 MiB, the size of the agent API's own body limit, so a larger answer could not reach the controller anyway.
- After reading an answer the client drains at most `errorBodyLimit` more bytes of the body before it closes it. A body longer than that is abandoned on purpose, and the connection is closed rather than kept.
- A binding is open to every source only when `Allowed` holds `any` and `Denied` is empty.
- A Docker version is a major made only of digits, followed by a dot and anything, or nothing at all.

- [ ] **Step 1: Write the failing tests**

In `internal/modules/npm/npmapi/client_test.go`, add `bytes`, `errors`, `fmt` and `strings` to the import block and append:

```go
// w10ncBody is a response body that counts the bytes read from it and records how many were left when it was closed.
type w10ncBody struct {
	r      *bytes.Reader
	read   int
	unread int
	closed bool
}

func (b *w10ncBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *w10ncBody) Close() error {
	b.unread, b.closed = b.r.Len(), true
	return nil
}

// w10ncTransport answers every request with one status and body.
type w10ncTransport struct {
	status int
	body   *w10ncBody
}

func (t w10ncTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{}, Body: t.body, Request: r}, nil
}

// TestW10NcBodiesAreDrainedWithinALimit: a body the client does not decode is read to its end when it is short enough,
// so the connection can be reused, and is never read further than a fixed bound, so a hostile NPM cannot make the
// client read without end.
func TestW10NcBodiesAreDrainedWithinALimit(t *testing.T) {
	refusal := func(size int) []byte {
		doc := `{"error":{"message":"boom"}}`
		return []byte(doc + strings.Repeat(" ", size-len(doc)))
	}
	tests := []struct {
		name    string
		status  int
		body    []byte
		drained bool // the whole body must have been read when the client closed it
		maxRead int
	}{
		{"a refusal over the read limit is drained", 500, refusal(100 << 10), true, 2 * errorBodyLimit},
		{"a huge refusal is read only to a bound", 500, refusal(1 << 20), false, 2 * errorBodyLimit},
		{"an answer nobody decodes is drained", 200, bytes.Repeat([]byte(" "), 50<<10), true, errorBodyLimit},
		{"a huge answer nobody decodes is read only to a bound", 200, bytes.Repeat([]byte(" "), 1<<20), false, errorBodyLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &w10ncBody{r: bytes.NewReader(tt.body)}
			c := New("http://npm.invalid", &http.Client{Transport: w10ncTransport{status: tt.status, body: body}})
			err := c.DeleteProxyHost(t.Context(), 3)
			if tt.status == 200 {
				if err != nil {
					t.Fatalf("DeleteProxyHost: %v", err)
				}
			} else {
				var ae *APIError
				if !errors.As(err, &ae) || ae.Status != tt.status || ae.Message != "boom" {
					t.Fatalf("error %#v, want an APIError %d with the message boom", err, tt.status)
				}
			}
			if !body.closed {
				t.Error("the body was not closed")
			}
			if tt.drained && body.unread != 0 {
				t.Errorf("%d bytes were left unread at close, want the body read to its end", body.unread)
			}
			if body.read > tt.maxRead {
				t.Errorf("%d bytes were read, want at most %d", body.read, tt.maxRead)
			}
		})
	}
}

// TestW10NcAnswerOverTheLimitIsRefused: an answer of exactly answerBodyLimit bytes decodes, and one byte more fails
// with an error that names the limit.
func TestW10NcAnswerOverTheLimitIsRefused(t *testing.T) {
	const skeleton = `{"status":""}`
	doc := func(size int) string { return `{"status":"` + strings.Repeat("a", size-len(skeleton)) + `"}` }

	t.Run("an answer of exactly the limit decodes", func(t *testing.T) {
		s := apStart(t, 200, doc(answerBodyLimit))
		h, err := s.client().Health(t.Context())
		if err != nil || len(h.Status) != answerBodyLimit-len(skeleton) {
			t.Fatalf("Health = %d status bytes, %v; want %d and no error", len(h.Status), err, answerBodyLimit-len(skeleton))
		}
	})
	t.Run("an answer one byte over the limit is refused", func(t *testing.T) {
		s := apStart(t, 200, doc(answerBodyLimit+1))
		_, err := s.client().Health(t.Context())
		want := fmt.Sprintf("exceeds %d bytes", answerBodyLimit)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Health error %v, want one that says the answer %s", err, want)
		}
	})
}
```

In `internal/modules/npm/controller_checks_test.go`, extend `TestCkPublicCheck`. Inside the test, next to the `hostAddr` helper, add:

```go
	denying := func(b firewall.DockerBinding, sources ...string) firewall.DockerBinding {
		b.Denied = sources
		return b
	}
```

and add these rows at the end of its `tests` table:

```go
		{"port 80 denied to a range while allowed from every source", deployed("no"),
			withBindings(denying(ckBinding("darkpanel-npm", 80, "tcp", "any"), "203.0.113.0/24"), ckBinding("darkpanel-npm", 443, "tcp", "any")),
			PublicResult{}, true, 1},
		{"port 443 denied from every source", deployed("no"),
			withBindings(ckBinding("darkpanel-npm", 80, "tcp", "any"), denying(ckBinding("darkpanel-npm", 443, "tcp", "any"), "any")),
			PublicResult{}, true, 1},
		{"a denial on another container's binding does not matter", deployed("no"),
			withBindings(append(ckPorts(), denying(ckBinding("web", 80, "tcp", "any"), "10.0.0.0/8"))...),
			PublicResult{Public: true, Via: "ports"}, false, 1},
```

Extend `TestCkInstanceCheck` with these rows at the end of its table (the struct is `{name, docker, integration, want, detail}`):

```go
		{"a bare major version is recognised", func(d *firewall.DockerStatus) { d.Version = "29" }, true, "", ""},
		{"a bare major below 28 is older than 28.0", func(d *firewall.DockerStatus) { d.Version, d.Supported = "27", false }, false, errcode.ModuleUnavailable, "older than 28.0"},
		{"a major with a suffix instead of a dot is not recognised", func(d *firewall.DockerStatus) { d.Version = "28-ce" }, false, errcode.ModuleUnavailable, "not recognised"},
		{"an empty major is not recognised", func(d *firewall.DockerStatus) { d.Version = ".5" }, false, errcode.ModuleUnavailable, "not recognised"},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/26 && export TMPDIR=/root/.cache/w10a-tmp/26 GOTOOLCHAIN=local && timeout 300 go test -count=1 ./internal/modules/npm/npmapi/ ; timeout 600 go test -count=1 -run 'TestCkPublicCheck$|TestCkInstanceCheck$' ./internal/modules/npm/`
Expected: the `npmapi` package fails to build with `undefined: answerBodyLimit`. In `internal/modules/npm`, `TestCkPublicCheck` fails on the two "denied" rows (the answer is `{Public:true Via:ports}` where a refusal is wanted) and `TestCkInstanceCheck` fails on "a bare major version is recognised" (a definite `module.unavailable` refusal where an answer is wanted) and on "a bare major below 28" (the detail says "not recognised", not "older than 28.0"). The other new rows pass already. Quote the output verbatim.

- [ ] **Step 3: Implement**

In `internal/modules/npm/npmapi/client.go`:

1. Add `answerBodyLimit` to the const block next to `errorBodyLimit`, with a doc comment: the most bytes of a successful answer the client reads, equal to the agent API's body limit because a larger answer could not be returned to the controller. Its value is 4 MiB (`4 << 20`).
2. In `do`, replace the deferred `res.Body.Close()` with a deferred function that first discards at most `errorBodyLimit` further bytes (`io.Copy(io.Discard, io.LimitReader(res.Body, errorBodyLimit))`, ignoring its result) and then closes the body. This one place covers every path: a refusal whose body was read only to `errorBodyLimit`, an answer nobody decodes, and an answer decoded from a buffer.
3. For a 2xx answer with `out == nil`, return `nil` at once; the deferred drain handles the body, so the unbounded `io.Copy(io.Discard, res.Body)` goes away.
4. For a 2xx answer with `out != nil`, read `io.ReadAll(io.LimitReader(res.Body, answerBodyLimit+1))` instead of streaming into `json.NewDecoder`.
   - A read error is returned as `npmapi: read <path> answer: <err>`.
   - More than `answerBodyLimit` bytes is the error `npmapi: <path> answer exceeds <answerBodyLimit> bytes` (the number in decimal, for example `exceeds 4194304 bytes`).
   - Otherwise `json.Unmarshal` the bytes into `out`; a failure keeps today's text `npmapi: decode <path> answer: <err>`.
5. `apiError` stays as it is: it reads at most `errorBodyLimit` bytes and takes only `error.message`.

In `internal/modules/npm/controller_checks.go`:

1. `ctlPortOpen`: a binding counts only when `Denied` is empty in addition to today's conditions (container name, host port, `tcp`, host address unset or unspecified, `Allowed` holds `any`). Update its doc comment to say it allows the port from every source and denies none.
2. `ctlDockerMajor`: when the string has no dot, the digits are the whole string. The major is recognised when those digits are non-empty and only `0`-`9`; `latest`, an empty string, `28-ce` and `.5` stay unrecognised. Update its doc comment (the digits before the first dot, or the whole string when it has none). The refusal texts do not change.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

Run:
```sh
mkdir -p /root/.cache/w10a-tmp/26 && export TMPDIR=/root/.cache/w10a-tmp/26 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestW10Nc' -v ./internal/modules/npm/npmapi/
timeout 600 go test -count=1 -run 'TestCkPublicCheck$|TestCkInstanceCheck$' -v ./internal/modules/npm/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/ ./internal/modules/npm/npmapi/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```
Expected: the named tests PASS; `go vet` and `go mod tidy -diff` print nothing; every line of `go test` is `ok` or `?`; golangci-lint prints `0 issues.`. Then `rm -rf /root/.cache/w10a-tmp/26 && go clean -testcache`.

- [ ] **Step 5: Commit**

Two commits, one per concern:

```bash
git add internal/modules/npm/npmapi/client.go internal/modules/npm/npmapi/client_test.go
git commit -m "fix(npm): bound and drain NPM API answers"
git add internal/modules/npm/controller_checks.go internal/modules/npm/controller_checks_test.go
git commit -m "fix(npm): honour denied sources in public check"
```

### Task 27: NPM instance list reports unreadable hosts

**Files:**
- Modify: `internal/modules/npm/wire.go` (`InstancesOutput`)
- Modify: `internal/modules/npm/instance_list.go` (`ctlInstanceListReader.Read`)
- Test: `internal/modules/npm/instance_list_test.go`
- Test: `internal/modules/npm/wire_test.go` (`TestSdWireJSON`)
- Regenerate: `schemas/npm.instance.list/v1.json` (by `make schemas`), `docs/api/**` (by `make api-docs`)

**Interfaces:**
- Consumes: nothing from another task.
- Produces: Contracts "NPM unreadable hosts", Go side: `InstancesOutput.Unreadable []string` with JSON name `unreadable`, the sorted ids of the hosts whose status read failed, `[]` and never `null` when there are none; a host listed there is absent from `Instances`. Task 32 reads it on the web side.

**Items:** 333, 334, 356

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security, silent-failures

Row 333: `npm.instance.list` leaves a host whose status read fails out of the answer, and the every-page banner renders nothing for it, so the admin-exposure warning is silent for an unreadable host. The wire change below lets the banner (Task 32) say so. Row 334: the reader logs one Warn per unreachable host on every banner refetch (every 60 s), which floods the controller log for an offline host; now that the answer carries the information, the line moves to debug level. Row 356, the part of this task: the fan-out keeps starting host reads after the context is cancelled.

This task is the only one of the four that regenerates `schemas/npm.*` and `docs/api/**`.

- [ ] **Step 1: Write the failing tests**

In `internal/modules/npm/instance_list_test.go`, add `bytes`, `fmt`, `log/slog` and `strings` to the import block and append:

```go
// TestW10NlUnreadableHostsAreListed: a host whose status read fails, whose agent answers something that is not a
// status, or that has no agent is named in Unreadable, in id order, and is not among the instances.
func TestW10NlUnreadableHostsAreListed(t *testing.T) {
	h := newTHHost(t)
	for _, id := range []string{"h5", "h1", "h3", "h2", "h4"} {
		ckAddHost(t, h, id, "host "+id)
	}
	h.agents = map[string]*coretest.FakeAgent{
		"h1": (&ckAgent{status: ckExposed()}).fake(),
		"h2": (&ckAgent{statusErr: errors.New("the agent does not answer")}).fake(),
		"h3": (&ckAgent{status: InstanceStatus{}}).fake(),
		"h4": {ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`[`), nil
		}},
	}
	got, _, err := ckList(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	wantInstances := []HostInstance{
		{HostID: "h1", HostName: "host h1", Status: ckExposed()},
		{HostID: "h3", HostName: "host h3", Status: InstanceStatus{}},
	}
	if !reflect.DeepEqual(got.Instances, wantInstances) {
		t.Errorf("instances %+v, want %+v", got.Instances, wantInstances)
	}
	if want := []string{"h2", "h4", "h5"}; !reflect.DeepEqual(got.Unreadable, want) {
		t.Errorf("unreadable %v, want %v: the failing agent, the agent that answers garbage and the host without an agent", got.Unreadable, want)
	}
}

// TestW10NlUnreadableIsNeverNull: with no unreadable host, the answer carries an empty array, whether there is no
// host at all or every host reads.
func TestW10NlUnreadableIsNeverNull(t *testing.T) {
	empty := newTHHost(t)
	readable := newTHHost(t)
	ckAddHost(t, readable, "h1", "alpha")
	readable.agents = map[string]*coretest.FakeAgent{"h1": (&ckAgent{status: ckExposed()}).fake()}
	for name, h := range map[string]*thHost{"no host": empty, "every host readable": readable} {
		_, raw, err := ckList(context.Background(), h)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || string(fields["unreadable"]) != "[]" {
			t.Errorf("%s: output %s (%v), want an unreadable member of []", name, raw, err)
		}
	}
}

// TestW10NlUnreadableHostLogsAtDebug: a host that cannot be read is in the answer, so reading it again every minute
// writes nothing at info level; the debug line names the host.
func TestW10NlUnreadableHostLogsAtDebug(t *testing.T) {
	h := newTHHost(t)
	ckAddHost(t, h, "h1", "alpha")
	h.agents = map[string]*coretest.FakeAgent{"h1": (&ckAgent{statusErr: errors.New("the agent does not answer")}).fake()}
	for _, tc := range []struct {
		level    slog.Level
		wantLine bool
	}{{slog.LevelInfo, false}, {slog.LevelDebug, true}} {
		var buf bytes.Buffer
		env := h.ctlEnv("")
		env.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: tc.level}))
		if _, err := h.nm().instanceList().Handler.Read(context.Background(), env, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if logged := buf.String(); tc.wantLine != strings.Contains(logged, "host=h1") || (!tc.wantLine && logged != "") {
			t.Errorf("at level %v the log is %q, want a line naming host h1: %v", tc.level, logged, tc.wantLine)
		}
	}
}

// TestW10NlCancelledListStartsNoMoreReads: once the context is cancelled, no further host read is started. Every read
// waits for the cancellation, which the ctlListParallel-th read causes, so all the slots are busy when it happens.
func TestW10NlCancelledListStartsNoMoreReads(t *testing.T) {
	h := newTHHost(t)
	h.agents = map[string]*coretest.FakeAgent{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	for i := range ctlListParallel + 4 {
		id := fmt.Sprintf("h%02d", i)
		ckAddHost(t, h, id, "host "+id)
		h.agents[id] = &coretest.FakeAgent{ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			if reads.Add(1) == ctlListParallel {
				cancel()
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	if _, _, err := ckList(ctx, h); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled", err)
	}
	if n := reads.Load(); n != ctlListParallel {
		t.Errorf("%d host reads started, want %d: none after the cancellation", n, ctlListParallel)
	}
}
```

In `internal/modules/npm/wire_test.go`, extend `TestSdWireJSON`: replace its single row `"instances output"` by these two rows:

```go
		{"instances output", InstancesOutput{
			Instances:  []HostInstance{{HostID: "h1", HostName: "alpha", Status: InstanceStatus{PinnedImage: "i2", Public: "no"}}},
			Unreadable: []string{"h2"},
		},
			`{"instances":[{"hostId":"h1","hostName":"alpha","status":{"deployed":false,"containerId":"","image":"","pinnedImage":"i2","running":false,` +
				`"apiReady":false,"public":"no","adminEmail":"","adminExposure":null,"upgradeAvailable":false}}],"unreadable":["h2"]}`},
		{"instances output without unreadable hosts", InstancesOutput{Instances: []HostInstance{}, Unreadable: []string{}}, `{"instances":[],"unreadable":[]}`},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/27 && export TMPDIR=/root/.cache/w10a-tmp/27 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run 'TestW10Nl|TestSdWireJSON|TestCkInstanceList' ./internal/modules/npm/`
Expected: build failure naming `Unreadable` (`unknown field Unreadable in struct literal of type InstancesOutput`, `got.Unreadable undefined`). Quote it verbatim. Once `wire.go` has the field and before `instance_list.go` changes, `TestW10NlCancelledListStartsNoMoreReads` fails with `12 host reads started, want 8`, and the other `TestW10Nl` tests fail on the missing ids and the Warn line.

- [ ] **Step 3: Implement**

1. `internal/modules/npm/wire.go`: add the field `Unreadable []string` with JSON name `unreadable` to `InstancesOutput`, after `Instances`, exactly as Contracts "NPM unreadable hosts" states it. Its doc comment says it holds the ids of the hosts whose instance status could not be read, which are not in `Instances`.
2. `internal/modules/npm/instance_list.go`, `ctlInstanceListReader.Read`:
   - After `wg.Wait()` and the cancellation check, build the answer with `Instances: []HostInstance{}` and `Unreadable: []string{}`. Walk `hosts` in order: a host whose status was read goes to `Instances`; any other host's `ID` is appended to `Unreadable`. `core_hosts` is read `ORDER BY id`, so `Unreadable` is sorted without a further sort. A host with no agent, a failed read and an answer that does not decode all count as unreadable, because `ctlInstanceStatus` returns an error for each.
   - The log line for a host that could not be read becomes `env.Log.Debug` (same message and attributes, still skipped when the context is cancelled).
   - Stop the fan-out on cancellation.
     - At the top of each loop iteration, return from the loop when `ctx.Err() != nil`.
     - Take the semaphore slot with a `select` on the slot and `ctx.Done()`.
     - Check `ctx.Err()` once more after the slot is taken, and when it is set release the slot and leave the loop. Without this second check a free slot and a cancelled context are both ready and `select` may choose the slot.
     - The existing `ctx.Err()` check after `wg.Wait()` still fails the whole list with the context's error.
   - Update the doc comment of `Read`: hosts whose status could not be read are named in `Unreadable`, in id order, and a cancelled request fails the list.

- [ ] **Step 4: Regenerate the schema snapshot and the API documentation**

Run:
```sh
mkdir -p /root/.cache/w10a-tmp/27 && export TMPDIR=/root/.cache/w10a-tmp/27 GOTOOLCHAIN=local
timeout 600 make schemas
timeout 600 make api-docs
git status --short
```
Expected: `git status` lists, besides the Go and test files of this task, only `schemas/npm.instance.list/v1.json`, `docs/api/npm.md` and `docs/api/openapi.json` (and any other file under `docs/api/` that quotes the schema of `npm.instance.list`). The snapshot's `InstancesOutput` gains a required `unreadable` property of `type: array` with string items; no other snapshot changes. If another `schemas/**` file changes, stop and report it.

- [ ] **Step 5: Run the tests to verify they pass, then the task's checks**

Run:
```sh
mkdir -p /root/.cache/w10a-tmp/27 && export TMPDIR=/root/.cache/w10a-tmp/27 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Nl|TestSdWire|TestCkInstanceList|TestRegSnapshotsAreCommitted' -v ./internal/modules/npm/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```
Expected: the named tests PASS; `go test ./...` includes `internal/modules` (`TestSnapshotsCurrent`) and `internal/apidocs` (`TestAdAPIDocsCurrent`) and every line is `ok` or `?`; `go vet` and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`. Then `rm -rf /root/.cache/w10a-tmp/27 && go clean -testcache`.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm/wire.go internal/modules/npm/instance_list.go internal/modules/npm/instance_list_test.go internal/modules/npm/wire_test.go schemas/npm.instance.list docs/api
git commit -m "fix(npm): list unreadable hosts, stop on cancel"
```

### Task 28: NPM removal deletes upgrade archives

**Files:**
- Modify: `internal/modules/npm/instance_remove.go` (`target`, `Plan`, `Apply`, `remove`, `Verify`, `Observe`)
- Test: `internal/modules/npm/instance_remove_test.go`

**Interfaces:**
- Consumes: from the module, `backupDir`, `ownedPaths`, `deps.readDir`, `env.Files.RemoveManaged`, `insertUpgrade`, `setUpgradePhase`, `npmEffect`, `nm.run` and `nm.observeEngine`, none of which this task changes.
- Produces: nothing another task consumes. `npm.instance.remove` journals one more effect per upgrade archive on the host: kind delete, resource `npm.archive:<file name>`, before-state `{"file":"<file name>"}`, after-state the null state.

**Items:** 332, 347

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** silent-failures

Row 332: `npm.instance.remove` leaves the upgrade archives (volume archives that hold Let's Encrypt private keys and NPM's database) in `/var/lib/darkpanel/agent/npm-backups` and the `npm_upgrades` rows on the host after the instance is gone. The removal now deletes them: each archive file is a journaled delete effect, and the `npm_upgrades` rows go with the other bookkeeping in the final transaction. The plan states the deletion. Row 347: a failed before-state read in `remove` is returned without `core.Definite` (the effect is not yet intended and nothing of it has changed, so the refusal is definite); the three nearly identical removal blocks of `Apply` become one loop; and the reserved refusal is tested with one exposure only.

Decisions this task makes:
- The archives are deleted after the container, the volumes and the network, because until the data is gone the archives are the only way back. A removal that fails earlier keeps them, and applying again continues.
- The archives of the instance are every regular file in `backupDir` whose name matches the `Pattern` of `ownedPaths()[0]`. The directory belongs to the module alone. A missing directory means none.
- The plan diff stays as it is when there are no archives and no `npm_upgrades` rows.

- [ ] **Step 1: Write the failing tests**

In `internal/modules/npm/instance_remove_test.go`, append (the file's existing imports, `context`, `database/sql`, `encoding/json`, `errors`, `slices`, `strings`, `testing`, `core`, `errcode` and `dockerapi`, are enough):

```go
// w10nrSeedUpgrade records a finished upgrade of change id with one archive of each volume on the host, and returns
// the archive names in order.
func w10nrSeedUpgrade(t *testing.T, h *thHost, id string) []string {
	t.Helper()
	ctx := context.Background()
	h.addChange(id, "npm.instance.upgrade")
	err := insertUpgrade(ctx, h.db, upgradeRow{ChangeID: id, FromImage: "jc21/nginx-proxy-manager:2.12.0", ToImage: Image, UpdatedAt: h.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{id + "-data.tar", id + "-letsencrypt.tar"}
	refs := []archiveRef{{Volume: dataVolume, File: names[0], SHA256: "x"}, {Volume: certVolume, File: names[1], SHA256: "x"}}
	for _, name := range names {
		if err := h.WriteManaged(ctx, ID, backupDir, name, []byte("archive "+name), 0o600, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := setUpgradePhase(ctx, h.db, id, "done", "", refs, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	return names
}

// w10nrApply applies npm.instance.remove as change.
func w10nrApply(t *testing.T, h *thHost, change string) error {
	t.Helper()
	h.addChange(change, "npm.instance.remove")
	_, err := h.nm().instanceRemove().Handler.Apply(context.Background(), h.env(change), json.RawMessage(`{}`))
	return err
}

// w10nrArchivesLeft lists the files the host still holds in the backup directory.
func w10nrArchivesLeft(h *thHost) []string {
	var left []string
	for name := range h.files {
		if rest, ok := strings.CutPrefix(name, backupDir+"/"); ok {
			left = append(left, rest)
		}
	}
	slices.Sort(left)
	return left
}

// w10nrUpgradeRows counts the npm_upgrades rows.
func w10nrUpgradeRows(t *testing.T, h *thHost) int {
	t.Helper()
	var n int
	if err := h.db.Read().QueryRowContext(context.Background(), "SELECT count(*) FROM npm_upgrades").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestW10NrApplyDeletesTheUpgradeArchives: the removal deletes every archive in the backup directory, each as a
// journaled delete after the Engine objects, and the npm_upgrades rows.
func TestW10NrApplyDeletesTheUpgradeArchives(t *testing.T) {
	h, _ := rmDeployed(t)
	names := slices.Concat(w10nrSeedUpgrade(t, h, "u1"), w10nrSeedUpgrade(t, h, "u2"))
	if err := w10nrApply(t, h, "c1"); err != nil {
		t.Fatal(err)
	}
	if left := w10nrArchivesLeft(h); len(left) != 0 {
		t.Errorf("files %v are left in %s, want none", left, backupDir)
	}
	if n := w10nrUpgradeRows(t, h); n != 0 {
		t.Errorf("npm_upgrades holds %d rows, want 0", n)
	}
	effects := h.journal.effects()
	if len(effects) != 4+len(names) {
		t.Fatalf("%d effects, want the four Engine objects and %d archives: %+v", len(effects), len(names), effects)
	}
	for i, name := range names {
		e := effects[4+i]
		if e.Kind != core.EffectDelete || e.Resource != "npm.archive:"+name || e.State != core.EffectMade {
			t.Errorf("effect %d is %+v, want a made delete of npm.archive:%s", 4+i, e, name)
		}
		if want := `{"file":"` + name + `"}`; string(e.Before.State) != want {
			t.Errorf("effect %d before-state %s, want %s", 4+i, e.Before.State, want)
		}
		if e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("effect %d post %+v, want the absent state null", 4+i, e.Post)
		}
	}
}

// TestW10NrPlanStatesTheArchiveDeletion: the plan names every archive and the records that go with the instance,
// and a plan made before an archive appeared is stale.
func TestW10NrPlanStatesTheArchiveDeletion(t *testing.T) {
	ctx := context.Background()
	h, inst := rmDeployed(t)
	def := h.nm().instanceRemove()
	plan := func() core.PlanOutcome {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := plan()
	names := w10nrSeedUpgrade(t, h, "u1")
	after := plan()
	want := slices.Concat(rmContainerDiff(inst, true), rmVolumeDiff, []core.DiffLine{
		{Op: "-", Text: "network darkpanel-npm"},
		{Op: "-", Text: "upgrade archive " + names[0]},
		{Op: "-", Text: "upgrade archive " + names[1]},
		{Op: " ", Text: "the upgrade archives hold copies of the instance's data and certificates, private keys included, and are deleted with it"},
		{Op: "-", Text: "the records of the instance's past upgrades"},
		{Op: "-", Text: "the stored admin password and the attachment claims of this instance"},
	})
	if !slices.Equal(after.Diff, want) {
		t.Errorf("diff %v, want %v", after.Diff, want)
	}
	if after.StateHash == before.StateHash {
		t.Error("the hash did not change when archives and upgrade records appeared")
	}
}

// TestW10NrVerifyFindsLeftoverArchivesAndRecords: after a removal, an archive in the backup directory or an
// npm_upgrades row is something the removal left.
func TestW10NrVerifyFindsLeftoverArchivesAndRecords(t *testing.T) {
	ctx := context.Background()
	h, _ := rmDeployed(t)
	def := h.nm().instanceRemove()
	if err := w10nrApply(t, h, "c1"); err != nil {
		t.Fatal(err)
	}
	verify := func() error {
		return def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	if err := verify(); err != nil {
		t.Fatalf("verify after the removal: %v", err)
	}
	if err := h.WriteManaged(ctx, ID, backupDir, "u9-data.tar", []byte("x"), 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err == nil || !strings.Contains(err.Error(), "the upgrade archive u9-data.tar") {
		t.Errorf("verify with an archive left: %v, want an error naming the upgrade archive u9-data.tar", err)
	}
	if err := h.RemoveManaged(ctx, ID, backupDir, "u9-data.tar"); err != nil {
		t.Fatal(err)
	}
	h.addChange("u8", "npm.instance.upgrade")
	if err := insertUpgrade(ctx, h.db, upgradeRow{ChangeID: "u8", FromImage: "a", ToImage: "b", UpdatedAt: h.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err == nil || !strings.Contains(err.Error(), "the upgrade records") {
		t.Errorf("verify with an upgrade row left: %v, want an error naming the upgrade records", err)
	}
}

// TestW10NrObserveReadsAnArchive: an archive's effect reads as its file while the file exists and as the null state
// once it is gone.
func TestW10NrObserveReadsAnArchive(t *testing.T) {
	ctx := context.Background()
	h, _ := rmDeployed(t)
	def := h.nm().instanceRemove()
	names := w10nrSeedUpgrade(t, h, "u1")
	e := core.Effect{Kind: core.EffectDelete, Resource: "npm.archive:" + names[0], State: core.EffectIntended}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if want := `{"file":"u1-data.tar"}`; err != nil || string(obs.Current.State) != want {
		t.Errorf("observation %s (%v), want %s while the file exists", obs.Current.State, err, want)
	}
	if err := h.RemoveManaged(ctx, ID, backupDir, names[0]); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation %s (%v), want null once the file is gone", obs.Current.State, err)
	}
}

// TestW10NrFailedBeforeStateReadIsDefinite: when the Engine cannot be read for the before-state of the first removal,
// nothing was changed, so the refusal is definite and no effect is journaled.
func TestW10NrFailedBeforeStateReadIsDefinite(t *testing.T) {
	h, _ := rmDeployed(t)
	// The first InspectContainer is the check of the target; the second is the before-state read of the removal.
	h.docker.Fail("InspectContainer", nil)
	h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
	err := w10nrApply(t, h, "c1")
	if err == nil || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite refusal", err)
	}
	if slices.Contains(h.docker.calls(), "RemoveContainer") {
		t.Error("the container was removed after its before-state could not be read")
	}
	if n := len(h.journal.effects()); n != 0 {
		t.Errorf("%d effects journaled, want none", n)
	}
	if n := rmCount(t, h, "npm_instances"); n != 1 {
		t.Errorf("npm_instances holds %d rows, want the instance kept", n)
	}
}

// TestW10NrReservedRefusalNamesEveryExposure: the refusal lists each exposure that reserves an object once, in name
// order, however many objects an exposure reserves. It pins the existing behaviour for more than one exposure.
func TestW10NrReservedRefusalNamesEveryExposure(t *testing.T) {
	ctx := context.Background()
	h, inst := rmDeployed(t)
	rmReserve(t, h, inst)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range []reservedRow{
			{Kind: "access_list", ObjectID: 3, Owner: "npm.admin.expose", Exposure: "npm.example.com", ChangeID: "c0"},
			{Kind: "proxy_host", ObjectID: 8, Owner: "panel.exposure.add", Exposure: "app.example.com", ChangeID: "c0"},
		} {
			if err := addReserved(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.nm().instanceRemove().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
	coded := rmRefused(t, err, errcode.NPMReserved)
	want := "the exposures app.example.com, npm.example.com reserve objects of this Nginx Proxy Manager instance; remove them first"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10a-tmp/28 && export TMPDIR=/root/.cache/w10a-tmp/28 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run 'TestW10Nr' ./internal/modules/npm/`
Expected: FAIL for `TestW10NrApplyDeletesTheUpgradeArchives` (files left in the backup directory, `npm_upgrades` holds 2 rows, 4 effects where 8 are wanted), `TestW10NrPlanStatesTheArchiveDeletion` (the diff has no archive lines and the hash did not change), `TestW10NrVerifyFindsLeftoverArchivesAndRecords` (verify passes with an archive left), `TestW10NrObserveReadsAnArchive` (`npm: "npm.archive:u1-data.tar" is not an Engine object of the module`) and `TestW10NrFailedBeforeStateReadIsDefinite` (the error is not definite). `TestW10NrReservedRefusalNamesEveryExposure` passes already: it only covers behaviour the plan keeps. Quote the output verbatim.

- [ ] **Step 3: Implement**

In `internal/modules/npm/instance_remove.go`:

1. Declare a noun constant next to the type declarations, `nounUpgradeArchive = "npm.archive"`, with a doc comment (a resource is `npm.archive:<file name>`). Do not edit `effects.go`.
2. Add the archives to `instanceRemoveTarget`: `archives []string` (the file names, sorted) and `upgrades int` (the number of `npm_upgrades` rows).
3. `target` fills them after the existing checks, and every failure is `core.Definite`, as for the other reads:
   - A helper lists the archives: `h.n.d.readDir(backupDir)`; `fs.ErrNotExist` means none; any other error is `npm: list <backupDir>: <err>`. It keeps regular files (not directories) whose name matches the `Pattern` of `ownedPaths()[0]` (compile the pattern inside the helper), sorted by name.
   - `SELECT count(*) FROM npm_upgrades` through `env.DB.Read()` gives `upgrades`.
4. `Plan`: after the line `network darkpanel-npm` and before the line `the stored admin password and the attachment claims of this instance`, add, in this order, with these exact texts:
   - one `{Op: "-", Text: "upgrade archive <name>"}` per archive, in name order;
   - when there is at least one archive, `{Op: " ", Text: "the upgrade archives hold copies of the instance's data and certificates, private keys included, and are deleted with it"}`;
   - when `upgrades > 0`, `{Op: "-", Text: "the records of the instance's past upgrades"}`.

   With no archive and no row the diff is exactly what it is today. The state hash input gains the archive names joined by `,` and the number of upgrade rows, as two more lines.
5. `Apply`: replace the three near-identical blocks (container, volumes, network) by one loop over an ordered list of steps, each a resource and the call that removes it, with the same resources, the same error texts and the same order as today (container if it exists, then `dataVolume` and `certVolume` as they exist, then the network). Append to that list, after the network, one step per archive in `t.archives`: resource `resourceOf(nounUpgradeArchive, name)`, call `env.Files.RemoveManaged(ctx, ID, backupDir, name)`, where an error that is `fs.ErrNotExist` counts as removed and any other is `npm: remove the archive <name>: <err>`. The final transaction also runs `DELETE FROM npm_upgrades`, with the error text `npm: delete the upgrade records: <err>`, next to the existing deletes.
6. `remove`: its before-state read and the observer that `run` uses to settle the effect both go through one new method, `observe(ctx, env, e)`, which answers an effect of `nounUpgradeArchive` from the archive and every other effect through `h.n.observeEngine`. An error from the before-state read is returned as `core.Definite(err)`, since nothing of this effect has happened.
7. Archive observation: from the effect's resource take the file name (`creationOf` gives it). If `readDir(backupDir)` lists a regular file of that name, the observation is `named` of the JSON `{"file":"<name>"}` (one string field `file`); otherwise `named(nullState().State)`. A missing directory is the absent state; another read error is returned as `npm: read <backupDir>: <err>`.
8. `Observe` calls `h.observe`. `Verify` also lists the archives and counts the rows, and adds `the upgrade archive <name>` for each archive and `the upgrade records` when rows remain to the `left` list, so the existing error `npm: the removal left ...` names them.
9. Update the doc comments of `target`, `Plan` and `Apply` to say the archives and the upgrade records are part of what the removal deletes.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

Run:
```sh
mkdir -p /root/.cache/w10a-tmp/28 && export TMPDIR=/root/.cache/w10a-tmp/28 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestW10Nr|TestRm' -v ./internal/modules/npm/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```
Expected: the named tests PASS, including every existing `TestRm` test unchanged (they have no archive on the host, so their diffs and four-effect lists are as before); `go vet` and `go mod tidy -diff` print nothing; every line of `go test` is `ok` or `?`; golangci-lint prints `0 issues.`. `schemas/**` and `docs/api/**` are unchanged (`git status --short` shows only the two files of this task). Then `rm -rf /root/.cache/w10a-tmp/28 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_remove.go internal/modules/npm/instance_remove_test.go
git commit -m "fix(npm): delete upgrade archives on removal"
```

### Task 29: Restore sets staged files through one descriptor

**Files:**
- Modify: `internal/controller/backup/durable.go` (the `Ops` interface and `osOps`)
- Modify: `internal/controller/backup/restore.go` (`installStaged`, `Restore`)
- Test (new): `internal/controller/backup/restore_secure_test.go`
- Test (modify): `internal/controller/backup/durable_test.go`, `internal/controller/backup/durable_crash_test.go`, `internal/controller/backup/restore_test.go`

**Interfaces:**
- Consumes: nothing from another task.
- Produces: `Ops.Secure(path string, uid, gid int, perm fs.FileMode) error` in package `backup`, which replaces `Ops.Chown` and `Ops.Chmod` (no package outside `internal/controller/backup` implements `Ops`). `backup.Restore` keeps its signature but now returns the filled `RestoreResult` together with its "finished and its database is installed, but removing earlier restores' set-aside files failed" error; Task 31's CLI and Task 34's runbook rely on that.

**Items:** 393, 397

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

Two faults in the restore path of the controller, each with a test that fails today:

1. `installStaged` checks the staged file with `os.Lstat` and then calls `Ops.Chown` (lchown) and `Ops.Chmod` (`os.Chmod`) by path. A symbolic link swapped in after the check makes `Chmod` change the mode of the file behind the link, as root. The check and the changes must use one open descriptor.
2. A restore that finished but could not remove an earlier restore's set-aside files returns an empty `RestoreResult` with that error, so its caller cannot report what was restored.

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/backup/restore_secure_test.go`. It pins `Ops.Secure` of the real operating-system implementation: it sets the mode of a regular file, and it refuses, without changing the mode of any file behind the path and without blocking, a symbolic link, a dangling link, a file with a second hard link, a named pipe and a directory.

```go
package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// w10brFile writes a regular file and gives it mode 0644, so that a call
// which sets 0600 shows in the mode.
func w10brFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // G302: a mode other than 0600 shows whether a call changed it
		t.Fatal(err)
	}
}

func w10brMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestW10BrOSOpsSecure(t *testing.T) {
	ops := OSOps()
	uid, gid := os.Getuid(), os.Getgid()

	t.Run("a regular file gets the mode", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "f")
		w10brFile(t, p)
		if err := ops.Secure(p, uid, gid, 0o600); err != nil {
			t.Fatalf("Secure = %v", err)
		}
		if got := w10brMode(t, p); got != 0o600 {
			t.Errorf("mode after Secure = %v, want 0600", got)
		}
	})

	t.Run("a missing path", func(t *testing.T) {
		err := ops.Secure(filepath.Join(t.TempDir(), "missing"), uid, gid, 0o600)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Secure of a missing path = %v, want fs.ErrNotExist", err)
		}
	})

	// A refused path must not change the mode of the file behind it, and a
	// named pipe must be refused without blocking.
	refusals := []struct {
		name  string
		plant func(t *testing.T, dir string) (path, untouched string)
	}{
		{"a symbolic link to a file", func(t *testing.T, dir string) (string, string) {
			target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
			w10brFile(t, target)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link, target
		}},
		{"a dangling symbolic link", func(t *testing.T, dir string) (string, string) {
			link := filepath.Join(dir, "link")
			if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
				t.Fatal(err)
			}
			return link, ""
		}},
		{"a file with a second hard link", func(t *testing.T, dir string) (string, string) {
			first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
			w10brFile(t, first)
			if err := os.Link(first, second); err != nil {
				t.Fatal(err)
			}
			return second, first
		}},
		{"a named pipe", func(t *testing.T, dir string) (string, string) {
			fifo := filepath.Join(dir, "pipe")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			return fifo, ""
		}},
		{"a directory", func(t *testing.T, dir string) (string, string) {
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil { //nolint:gosec // G301: a mode other than 0700 shows whether a call changed it
				t.Fatal(err)
			}
			return sub, sub
		}},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			path, untouched := tc.plant(t, t.TempDir())
			var before fs.FileMode
			if untouched != "" {
				before = w10brMode(t, untouched)
			}
			done := make(chan error, 1)
			go func() { done <- ops.Secure(path, uid, gid, 0o600) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "is not a single regular file") {
					t.Errorf("Secure = %v, want an error saying the path is not a single regular file", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Secure did not return within 10 seconds")
			}
			if untouched != "" {
				if after := w10brMode(t, untouched); after != before {
					t.Errorf("the mode behind the refused path changed from %v to %v", before, after)
				}
			}
		})
	}
}
```

Edit three existing test files. They stop compiling until `Ops.Secure` exists, which is part of the expected red. Each edit is a unified diff against the file on `main`; apply it as written.

`internal/controller/backup/durable_crash_test.go`: the crash wrapper replaces its two methods by one.

```diff
@@ -96,10 +96,6 @@
 }
 
-func (c *crashOps) Chown(path string, uid, gid int) error {
-	return c.duCall(func() error { return c.inner.Chown(path, uid, gid) })
-}
-
-func (c *crashOps) Chmod(path string, perm fs.FileMode) error {
-	return c.duCall(func() error { return c.inner.Chmod(path, perm) })
+func (c *crashOps) Secure(path string, uid, gid int, perm fs.FileMode) error {
+	return c.duCall(func() error { return c.inner.Secure(path, uid, gid, perm) })
 }
 
```

`internal/controller/backup/durable_test.go`: `TestDuOSOpsDirectoryAndMetadata` drops its `Chmod` and `Chown` assertions (`Secure` is pinned by `TestW10BrOSOpsSecure`), the script of the crash tests makes one `Secure` call where it made a `Chmod` and a `Chown` call, so it counts 9 calls, and the table of `TestDuCrashOpsEveryCall` and the dead-after-crash test follow.

```diff
@@ -131,26 +131,8 @@
 	p := filepath.Join(dir, "f")
 	duWriteFile(t, ops, p, "x")
-	if err := ops.Chmod(p, 0o640); err != nil {
-		t.Fatal(err)
-	}
-	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o640 {
-		t.Errorf("mode after Chmod = %v, %v; want 0640", st.Mode().Perm(), err)
-	}
-	if err := ops.Chmod(filepath.Join(dir, "missing"), 0o600); !errors.Is(err, fs.ErrNotExist) {
-		t.Errorf("Chmod of a missing path = %v, want fs.ErrNotExist", err)
-	}
-	if err := ops.Chown(p, os.Getuid(), os.Getgid()); err != nil {
-		t.Errorf("Chown to the owner's own ids = %v", err)
-	}
-	if err := ops.Chown(filepath.Join(dir, "missing"), os.Getuid(), os.Getgid()); !errors.Is(err, fs.ErrNotExist) {
-		t.Errorf("Chown of a missing path = %v, want fs.ErrNotExist", err)
-	}
 	dangling := filepath.Join(dir, "dangling")
 	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
 		t.Fatal(err)
 	}
-	if err := ops.Chown(dangling, os.Getuid(), os.Getgid()); err != nil {
-		t.Errorf("Chown of a dangling link = %v; it must change the link, not follow it", err)
-	}
 
 	for _, tt := range []struct {
@@ -167,6 +149,6 @@
 
 // duScript makes one call of each kind in a fixed order and stops at the
-// first error: Create, Write, Sync, Close, Rename, Exists, Chmod, Chown,
-// SyncDir, Remove.
+// first error: Create, Write, Sync, Close, Rename, Exists, Secure, SyncDir,
+// Remove.
 func duScript(ops Ops, dir string) error {
 	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
@@ -190,8 +172,5 @@
 		return err
 	}
-	if err := ops.Chmod(b, 0o640); err != nil {
-		return err
-	}
-	if err := ops.Chown(b, os.Getuid(), os.Getgid()); err != nil {
+	if err := ops.Secure(b, os.Getuid(), os.Getgid(), 0o640); err != nil {
 		return err
 	}
@@ -202,5 +181,5 @@
 }
 
-const duScriptCalls = 10
+const duScriptCalls = 9
 
 // duState lists dir as sorted "name=content:mode" words.
@@ -283,14 +262,13 @@
 	type outcome struct{ before, after string }
 	want := map[int]outcome{
-		1:  {"", "a=:-rw-------"},
-		2:  {"a=:-rw-------", "a=data:-rw-------"},
-		3:  {"a=data:-rw-------", "a=data:-rw-------"},
-		4:  {"a=data:-rw-------", "a=data:-rw-------"},
-		5:  {"a=data:-rw-------", "b=data:-rw-------"},
-		6:  {"b=data:-rw-------", "b=data:-rw-------"},
-		7:  {"b=data:-rw-------", "b=data:-rw-r-----"},
-		8:  {"b=data:-rw-r-----", "b=data:-rw-r-----"},
-		9:  {"b=data:-rw-r-----", "b=data:-rw-r-----"},
-		10: {"b=data:-rw-r-----", ""},
+		1: {"", "a=:-rw-------"},
+		2: {"a=:-rw-------", "a=data:-rw-------"},
+		3: {"a=data:-rw-------", "a=data:-rw-------"},
+		4: {"a=data:-rw-------", "a=data:-rw-------"},
+		5: {"a=data:-rw-------", "b=data:-rw-------"},
+		6: {"b=data:-rw-------", "b=data:-rw-------"},
+		7: {"b=data:-rw-------", "b=data:-rw-r-----"},
+		8: {"b=data:-rw-r-----", "b=data:-rw-r-----"},
+		9: {"b=data:-rw-r-----", ""},
 	}
 	for k := 1; k <= duScriptCalls; k++ {
@@ -350,9 +328,6 @@
 		t.Errorf("SyncDir after the crash = %v, want errCrash", err)
 	}
-	if err := c.Chmod(a, 0o644); !errors.Is(err, errCrash) {
-		t.Errorf("Chmod after the crash = %v, want errCrash", err)
-	}
-	if err := c.Chown(a, os.Getuid(), os.Getgid()); !errors.Is(err, errCrash) {
-		t.Errorf("Chown after the crash = %v, want errCrash", err)
+	if err := c.Secure(a, os.Getuid(), os.Getgid(), 0o644); !errors.Is(err, errCrash) {
+		t.Errorf("Secure after the crash = %v, want errCrash", err)
 	}
 	if ok, err := c.Exists(a); !errors.Is(err, errCrash) || ok {
```

`internal/controller/backup/restore_test.go`: the recording `Ops` of the restore test records one `secure` event per staged file instead of a `chown` and a `chmod` (it records the owner it was asked for and applies the test process's own, which a non-root test can do), and `TestRtFailedCleanupOfOldAsidesReportsAFinishedRestore` also requires the result.

```diff
@@ -309,6 +309,7 @@
 }
 
-// rtOps records the renames, chowns and chmods of a restore. Chown only
-// records: the test process cannot give a file to another user.
+// rtOps records the renames and secures of a restore. Secure records the
+// owner it was asked for but applies the test process's own: the test process
+// cannot give a file to another user.
 type rtOps struct {
 	Ops
@@ -323,12 +324,7 @@
 }
 
-func (o *rtOps) Chown(path string, uid, gid int) error {
-	o.note("chown %s %d %d", filepath.Base(path), uid, gid)
-	return nil
-}
-
-func (o *rtOps) Chmod(path string, perm fs.FileMode) error {
-	o.note("chmod %s %v", filepath.Base(path), perm)
-	return o.Ops.Chmod(path, perm)
+func (o *rtOps) Secure(path string, uid, gid int, perm fs.FileMode) error {
+	o.note("secure %s %d %d %v", filepath.Base(path), uid, gid, perm)
+	return o.Ops.Secure(path, os.Getuid(), os.Getgid(), perm)
 }
 
@@ -423,9 +419,7 @@
 	owner := fmt.Sprintf("%d %d", rtOwnerUID, rtOwnerGID)
 	wantEvents := []string{
-		"chown controller.db.restore-" + tx + " " + owner,
-		"chmod controller.db.restore-" + tx + " -rw-------",
+		"secure controller.db.restore-" + tx + " " + owner + " -rw-------",
 		"rename controller.db.restore-" + tx + " controller.db",
-		"chown controller.key.restore-" + tx + " " + owner,
-		"chmod controller.key.restore-" + tx + " -rw-------",
+		"secure controller.key.restore-" + tx + " " + owner + " -rw-------",
 		"rename controller.key.restore-" + tx + " controller.key",
 	}
@@ -638,8 +632,11 @@
 	l.host.Ops = &rtFailRemoveOps{Ops: OSOps(), path: AsideName(p.ControllerDB(), tx1)}
 
-	_, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
+	res, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
 	if err == nil || !strings.Contains(err.Error(), "restore "+tx2+" finished") || strings.Contains(err.Error(), "--recover") {
 		t.Fatalf("Restore error = %v, want one saying restore %s finished and not asking for --recover", err, tx2)
 	}
+	if res.Tx != tx2 || !res.BackupCreatedAt.Equal(bk.createdAt) {
+		t.Errorf("Restore result = %+v, want tx %s and the backup's createdAt %s: the restore finished, so its caller can report it", res, tx2, bk.createdAt)
+	}
 	if js, lerr := ListJournals(p.ControllerDir); lerr != nil || len(js) != 0 {
 		t.Errorf("journals = %v (error %v), want none: the restore finished", js, lerr)
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/29
export TMPDIR=/root/.cache/w10a-tmp/29 GOTOOLCHAIN=local
timeout 300 go vet ./internal/controller/backup/
```

Expected: FAIL to build: package `backup` reports `ops.Secure undefined (type Ops has no field or method Secure)`. Record the output verbatim.

- [ ] **Step 3: Implement**

1. `internal/controller/backup/durable.go`, interface `Ops`: remove `Chown` and `Chmod` and add `Secure(path string, uid, gid int, perm fs.FileMode) error`. Its comment says: it opens path read-only without following a symbolic link, requires the open descriptor to be a regular file with one link, and then sets owner and mode through that descriptor; anything else is refused with an error saying the path is not a single regular file. Remove `osOps.Chown` and `osOps.Chmod` and add `osOps.Secure`:
   - open with `os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)` and the same `//nolint:gosec // G304` reason `Create` carries. `O_NONBLOCK` keeps a named pipe from blocking the open until the `Stat` refuses it; say so in a comment, with the reason for the single descriptor: nothing can be swapped between the check and the change.
   - `syscall.ELOOP` from the open (a symbolic link, dangling or not) returns `backup: <path> is not a single regular file`. Any other open error returns `backup: secure <path>: <err>` wrapped with `%w`, so `errors.Is(err, fs.ErrNotExist)` holds for a missing path.
   - `Stat` the open file. Unless `Mode().IsRegular()` and `Sys()` is a `*syscall.Stat_t` with `Nlink == 1`, return the same `backup: <path> is not a single regular file` text.
   - then `f.Chown(uid, gid)` and, only if that succeeded, `f.Chmod(perm)` (both act on the descriptor), each error wrapped as `backup: secure <path>: <err>`. Close the file in a deferred call.
2. `internal/controller/backup/restore.go`:
   - `installStaged`: delete the `os.Lstat` block, its comment and the `Chown` and `Chmod` calls. Where they were, call `h.Ops.Secure(from, uid, gid, 0o600)` and return its error wrapped as `fmt.Errorf("%w; the journal %s is kept", err, journalPath)`. Keep the comment that owner and mode are set before the rename. Update the function's doc comment. `TestRcInstallRefusesALinkedStagedFile` keeps passing unchanged: its message still contains `is not a single regular file`. Remove an import only if the compiler reports it unused.
   - `Restore`: the branch that handles `errOldAside` returns `RestoreResult{Tx: tx, BackupCreatedAt: createdAt}` with the same error text. Extend `Restore`'s doc comment: a finished restore whose cleanup of earlier restores' set-aside files failed returns its result together with that error.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/29 GOTOOLCHAIN=local
timeout 900 go test -count=1 -run 'W10Br|TestDu|TestRt|TestRc' ./internal/controller/backup/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1800 go test -race -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf /root/.cache/w10a-tmp/29 && go clean -testcache
```

Expected: every `go test` line `ok` (the race run of package `backup` takes several minutes; run it as a background task), `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add internal/controller/backup/durable.go internal/controller/backup/restore.go internal/controller/backup/restore_secure_test.go internal/controller/backup/durable_test.go internal/controller/backup/durable_crash_test.go internal/controller/backup/restore_test.go
git commit -m "fix(backup): secure staged files through one fd" -m "Set the owner and mode of a staged file through one descriptor that refuses links, and return the result of a finished restore whose cleanup of earlier set-aside files failed."
```

### Task 30: Backup copy, key read and enroll-token fixes

**Files:**
- Modify: `internal/controller/backup/stream.go` (`streamServe`, `streamReadKey`)
- Modify: `internal/controller/backup/contribute.go` (`Contribute`)
- Modify: `internal/controller/backup/quarantine_routes.go` (`enrollTokenHandler`)
- Test (new): `internal/controller/backup/stream_copy_test.go`, `internal/controller/backup/quarantine_token_test.go`
- Test (modify): `internal/controller/backup/stream_test.go`

**Interfaces:**
- Consumes: nothing from another task.
- Produces: unexported `streamReadBounded` and `sweepCopies` in package `backup`. No other task consumes them.

**Items:** 395

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 3 = 7
**Lenses:** security

Three faults in the backup stream and the quarantine routes of the controller, each with a test that fails today:

1. A backup's plaintext copy of the database (`VACUUM INTO`) is created beside the database under the process umask and chmodded afterwards, and a crash leaves it behind. It must live in a private directory, and the controller must remove leftovers at startup.
2. A key file that grows between `Stat` and the read is cut off at `MaxKeyMember` bytes without an error, and the cut-off key is backed up as if it were whole.
3. `enroll-token` reads the quarantine flag outside the transaction that issues the token, so a release that commits in between lets a token out of a controller that is no longer in quarantine.

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/backup/stream_copy_test.go`. The first test reads the database directory while the stream runs (the stream asks for its salt after the copy exists and before the first response byte), the second pins the startup sweep, the third pins the bounded key read.

```go
package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// w10bcSpy is the random source of a backup stream. The stream asks for its
// salt after the temporary copy of the database exists and before the first
// response byte, so look runs while the copy is on disk. It runs once.
type w10bcSpy struct {
	once sync.Once
	look func()
}

func (s *w10bcSpy) Read(p []byte) (int, error) {
	s.once.Do(s.look)
	return rand.Read(p)
}

type w10bcSeen struct {
	name string
	dir  bool
	perm os.FileMode
}

func w10bcDeps(e *bsEnv) ext.Deps {
	return ext.Deps{
		DB: e.db, Sealer: e.sealer, Clock: e.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: e.audit, LocalHostID: "host-local",
	}
}

func w10bcLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestW10BcBackupCopyLivesInAPrivateDirectory(t *testing.T) {
	e := bsNewEnv(t)
	var (
		seen    []w10bcSeen
		lookErr error
	)
	spy := &w10bcSpy{look: func() {
		names, err := filepath.Glob(filepath.Join(e.dir, "backup-*"))
		if err != nil {
			lookErr = err
			return
		}
		for _, name := range names {
			st, err := os.Lstat(name)
			if err != nil {
				lookErr = err
				return
			}
			seen = append(seen, w10bcSeen{name: filepath.Base(name), dir: st.IsDir(), perm: st.Mode().Perm()})
		}
	}}

	rec := httptest.NewRecorder()
	e.b.streamServe(w10bcDeps(e), spy).ServeHTTP(rec, bsRequest(bsBody(t, "p", &TestParams)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if lookErr != nil {
		t.Fatal(lookErr)
	}
	if len(seen) != 1 || !seen[0].dir || seen[0].perm != 0o700 {
		t.Errorf("while the stream ran, the database directory held %+v; want one directory of mode 0700 that holds the plaintext copy, so that no other user can read it before the copy is chmodded", seen)
	}
	if left := w10bcLeftovers(t, e.dir); len(left) != 0 {
		t.Errorf("after the stream, %v is left beside the database", left)
	}
}

func TestW10BcContributeSweepsTheCopiesACrashLeft(t *testing.T) {
	e := bsNewEnv(t)
	crashed := filepath.Join(e.dir, "backup-"+ulid.Make().String()+".tmp")
	if err := os.Mkdir(crashed, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(e.dir, "backup-"+ulid.Make().String()+".db")
	notACopy := []string{
		filepath.Join(e.dir, "backup-notes.txt"),
		filepath.Join(e.dir, "backup-"+ulid.Make().String()+".db.keep"),
		filepath.Join(e.dir, "mybackup-"+ulid.Make().String()+".db"),
	}
	for _, f := range append([]string{filepath.Join(crashed, "backup.db"), legacy}, notACopy...) {
		if err := os.WriteFile(f, []byte("plaintext database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := e.b.Contribute(w10bcDeps(e)); err != nil {
		t.Fatalf("Contribute: %v", err)
	}

	for _, gone := range []string{crashed, legacy} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: error %v, want it swept at startup", filepath.Base(gone), err)
		}
	}
	for _, kept := range append(notACopy, e.db.Path()) {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s: %v, want it kept: it is not a copy of a crashed backup", filepath.Base(kept), err)
		}
	}
}

func TestW10BcKeyReadRefusesAFileThatGrew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.key")
	key := bytes.Repeat([]byte("k"), MaxKeyMember)

	got, err := streamReadBounded(bytes.NewReader(key), path)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("streamReadBounded of exactly %d bytes = %d bytes, %v; want them all", MaxKeyMember, len(got), err)
	}

	got, err = streamReadBounded(io.MultiReader(bytes.NewReader(key), strings.NewReader("k")), path)
	if err == nil || got != nil {
		t.Fatalf("streamReadBounded of %d bytes = %d bytes, %v; want no key and an error: a key that grew past the limit must not be cut off", MaxKeyMember+1, len(got), err)
	}
	if msg := err.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, "larger than") || strings.Contains(msg, "kkkk") {
		t.Errorf("error %q, want one naming the file and the limit and carrying no key bytes", msg)
	}

	got, err = streamReadBounded(iotest.ErrReader(errors.New("disk failure")), path)
	if err == nil || got != nil || !strings.Contains(err.Error(), "disk failure") {
		t.Errorf("streamReadBounded of a failing reader = %d bytes, %v; want no key and the read error", len(got), err)
	}
}
```

Create `internal/controller/backup/quarantine_token_test.go`. It releases the quarantine in a transaction that holds the database writer, starts an `enroll-token` request, waits (through the writer pool's `WaitCount`, not a sleep) until the request is past every check made outside a transaction and queued for the writer, then lets the release commit. The token must be refused.

```go
package backup

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestW10BcEnrollTokenReadsTheFlagInItsTransaction releases the quarantine in
// a transaction that commits only after the enroll-token request has passed
// its first check and is waiting for the database writer. The token must not
// be issued, because the quarantine is over when the request's transaction
// runs.
func TestW10BcEnrollTokenReadsTheFlagInItsTransaction(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	ctx := context.Background()
	hostsBefore := e.hostStates(t)

	entered, release := make(chan struct{}), make(chan struct{})
	released := make(chan error, 1)
	go func() {
		released <- e.db.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-released:
		t.Fatalf("the releasing transaction ended before it held the writer: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the releasing transaction never began")
	}

	// A request that waits for the writer is counted by the pool, so the
	// test knows the request is past every check made outside a transaction.
	waiting := e.db.Writer().Stats().WaitCount
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answered <- qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for e.db.Writer().Stats().WaitCount <= waiting {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the enroll-token request never waited for the database writer")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-released; err != nil {
		t.Fatalf("release the quarantine: %v", err)
	}

	var rec *httptest.ResponseRecorder
	select {
	case rec = <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("the enroll-token request never answered")
	}
	qlWantProblem(t, rec, http.StatusUnprocessableEntity, errcode.InvalidInput,
		"the controller is not in restore quarantine; use darkpanel host token")
	if n := len(e.issuer.issued()); n != 0 {
		t.Errorf("the issuer was called %d times after the quarantine ended", n)
	}
	if got := e.hostStates(t); got != hostsBefore {
		t.Errorf("host states changed to %s, were %s", got, hostsBefore)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none for a refusal", rows)
	}
}
```

Edit one existing test file, as a unified diff against the file on `main`; apply it as written.

`internal/controller/backup/stream_test.go`: the helper that lists temporary copies looks for `backup-*`, so it sees the private directory too and the existing "no copy left" assertions stay meaningful.

```diff
@@ -85,5 +85,5 @@
 func (e *bsEnv) copies(t *testing.T) []string {
 	t.Helper()
-	names, err := filepath.Glob(filepath.Join(e.dir, "backup-*.db"))
+	names, err := filepath.Glob(filepath.Join(e.dir, "backup-*"))
 	if err != nil {
 		t.Fatal(err)
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/30
export TMPDIR=/root/.cache/w10a-tmp/30 GOTOOLCHAIN=local
timeout 300 go vet ./internal/controller/backup/ && timeout 900 go test -count=1 -run 'W10Br' ./internal/controller/backup/
```

Expected: FAIL: `go vet` reports `undefined: streamReadBounded` (and `undefined: sweepCopies` where the tests call it); once those exist as stubs the `TestW10Bc` tests fail on their assertions. Record the output verbatim.

- [ ] **Step 3: Implement**

1. `internal/controller/backup/stream.go`:
   - Add the constant `copyDirSuffix = ".tmp"`. In `streamServe`, replace the temporary file by a directory: create `backup-<ULID>.tmp` beside `deps.DB.Path()` with `os.Mkdir(dir, 0o700)` (a failure is answered as a problem, `backup: create the temporary directory <dir>: <err>`), remove it with `os.RemoveAll` in the deferred cleanup that logs a failure as today, and make `VACUUM INTO` write `<dir>/backup.db`. Keep the `os.Chmod(path, 0o600)`. A comment says why: SQLite creates the copy under the process umask, so the directory is what keeps other users from reading it before the chmod. Imports that become unused go.
   - `streamReadKey` ends with `return streamReadBounded(f, path)`. New `streamReadBounded(r io.Reader, path string) ([]byte, error)` reads at most `MaxKeyMember+1` bytes. A read error returns no key and `backup: read the controller key file <path>: <err>`. More than `MaxKeyMember` bytes returns no key (the bytes read are cleared) and `backup: the controller key file <path> is larger than <MaxKeyMember> bytes`, the text the `Stat` check uses. No error carries key bytes. A comment says why the extra byte is read: a file that grew after `Stat` must be refused, not cut off.
   - New `sweepCopies(dir string, log *slog.Logger)`: list `dir` and `os.RemoveAll` every entry whose name is `backup-`, then a 26-character upper-case ULID (alphabet `0-9A-HJKMNP-TV-Z`), then `.tmp` or `.db` (earlier versions named the copy `backup-<ULID>.db`). Every other name stays. A failure to list or to remove is logged at error level with the `backup:` prefix and does not stop anything. The doc comment says a crash leaves the copy behind because the deferred removal does not run.
2. `internal/controller/backup/contribute.go`, `Contribute`: call `sweepCopies(filepath.Dir(deps.DB.Path()), deps.Log)` first, only when `deps.DB != nil` (`routes_test.go` calls `Contribute` with an empty `ext.Deps`). Extend the doc comment: it also removes the copies a crashed backup left; it still starts no service.
3. `internal/controller/backup/quarantine_routes.go`, `enrollTokenHandler`: keep the early `quarantine.Active` check. Make the first statement of the `deps.DB.Write` closure a read through `tx` of whether `core_meta` has the row `quarantine.MetaKey` (the query `releaseHandler` uses). When it is absent, return the same `errcode.InvalidInput` error with the same text as the early check. A comment says why: a release can commit between the early check and the write. Do not edit package `quarantine`.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/30 GOTOOLCHAIN=local
timeout 900 go test -count=1 -run 'W10Br|TestBs|TestQl|TestCk' ./internal/controller/backup/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1800 go test -race -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf /root/.cache/w10a-tmp/30 && go clean -testcache
```

Expected: every `go test` line `ok` (run the race run of package `backup` as a background task), `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add internal/controller/backup/stream.go internal/controller/backup/contribute.go internal/controller/backup/quarantine_routes.go internal/controller/backup/stream_copy_test.go internal/controller/backup/quarantine_token_test.go internal/controller/backup/stream_test.go
git commit -m "fix(backup): guard the backup copy and key read" -m "Keep the plaintext copy of a backup in a private directory that startup sweeps, refuse a key file that grew past its limit, and read the quarantine flag in the transaction that issues an enroll token."
```

### Task 31: Backup CLI passphrase, answer type and restore summary

**Files:**
- Modify: `internal/cli/controller_backup.go` (`runBackup`, `restore`)
- Test (new): `internal/cli/controller_backup_fix_test.go`

**Interfaces:**
- Consumes: Task 29's `backup.Restore`, which returns the filled `RestoreResult` together with the error of a finished restore whose cleanup failed.
- Produces: unexported `encodeBackupRequest` and `reportRestore` in package `cli`. No other task consumes them.

**Items:** 397

**Depends on:** Task 29

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

The CLI turns the passphrase into a Go string (`string(pass)`) for `json.Marshal`, which cannot be zeroed; it saves any 2xx answer to `--out` whatever its `Content-Type`; and a restore that finished but could not remove an earlier restore's set-aside files prints no summary, because `Restore` returns an empty result with that error.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/controller_backup_fix_test.go`. `encodeBackupRequest` builds the request body from the passphrase bytes, `reportRestore` is the end of `restore`, and the middle test goes through the whole `backup` command against a fake controller on a Unix socket under a temporary directory.

```go
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/backup"
)

func TestW10BrBackupRequestBody(t *testing.T) {
	cases := []struct {
		name   string
		pass   []byte
		params *backup.Params
	}{
		{"plain", []byte("correct horse battery"), nil},
		{"quotes and backslashes", []byte(`a"b\c`), nil},
		{"control characters", []byte("tab\there\nnew\r\x00\x1f\x7f"), nil},
		{"markup characters and non-ASCII text", []byte("<&>ü漢字😀 "), nil},
		{"argon2 parameters", []byte("x"), &backup.Params{T: 1, M: 65536, P: 2}},
		{"the longest passphrase", bytes.Repeat([]byte("p"), maxPassphrase), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass := bytes.Clone(tc.pass)
			body, err := encodeBackupRequest(pass, tc.params)
			if err != nil {
				t.Fatalf("encodeBackupRequest: %v", err)
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			var got backup.BackupRequest
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("the body is not a backup request: %v", err)
			}
			if dec.InputOffset() != int64(len(bytes.TrimSpace(body))) {
				t.Errorf("the body holds more than one JSON value")
			}
			if got.Passphrase != string(tc.pass) {
				t.Errorf("the decoded passphrase has %d bytes and differs from the one given (%d bytes)", len(got.Passphrase), len(tc.pass))
			}
			if (got.Argon2 == nil) != (tc.params == nil) || got.Argon2 != nil && *got.Argon2 != *tc.params {
				t.Errorf("argon2 = %+v, want %+v", got.Argon2, tc.params)
			}
		})
	}

	t.Run("a passphrase that is not UTF-8 is refused", func(t *testing.T) {
		body, err := encodeBackupRequest([]byte{'a', 0xff, 0xfe}, nil)
		if err == nil || body != nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("encodeBackupRequest = %q, %v; want no body and an error naming UTF-8: JSON would replace the bytes and the restore could never type the same passphrase", body, err)
		}
	})
}

func TestW10BrBackupRefusesAnAnswerThatIsNotABackupStream(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 5000)
	for _, tc := range []struct {
		name, contentType string
		ok                bool
	}{
		{"an HTML page", "text/html; charset=utf-8", false},
		{"JSON", "application/json", false},
		{"no content type", "", false},
		{"a look-alike type", "application/octet-streamer", false},
		{"octet-stream", "application/octet-stream", true},
		{"octet-stream with a parameter", "application/octet-stream; charset=binary", true},
		{"octet-stream in capitals", "Application/Octet-Stream", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock, _ := ckBackupServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType == "" {
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			})
			dir := t.TempDir()
			out := filepath.Join(dir, "b.dpbk")
			stdout, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, "backup", "--out", out, "--socket", sock)
			if tc.ok {
				if got, err := os.ReadFile(out); code != 0 || err != nil || !bytes.Equal(got, body) { //nolint:gosec // G304: a path under the test's own temporary directory
					t.Errorf("exit %d, file of %d bytes (error %v), stderr %q; want the stream saved", code, len(got), err, stderr)
				}
				return
			}
			if code != ExitError || stdout != "" || !strings.Contains(stderr, "not application/octet-stream") {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d, no stdout and an error saying the answer is not application/octet-stream", code, stdout, stderr, ExitError)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Errorf("the directory holds %d entries (error %v), want none: nothing may be saved from such an answer", len(entries), err)
			}
		})
	}
}

func TestW10BrReportRestore(t *testing.T) {
	done := backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	cleanup := errors.New("backup: restore 01JTESTTX finished and its database is installed, but removing earlier restores' set-aside files failed")
	for _, tc := range []struct {
		name    string
		res     backup.RestoreResult
		err     error
		wantOut string
	}{
		{"a finished restore", done, nil, restoreSummary(done)},
		{"a finished restore whose cleanup failed", done, cleanup, restoreSummary(done)},
		{"a restore that did not finish", backup.RestoreResult{}, errors.New("backup: wrong passphrase or damaged backup file"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := reportRestore(&out, tc.res, tc.err)
			if !errors.Is(err, tc.err) {
				t.Errorf("reportRestore returned %v, want %v: a failure is never hidden", err, tc.err)
			}
			if out.String() != tc.wantOut {
				t.Errorf("reportRestore printed %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/31
export TMPDIR=/root/.cache/w10a-tmp/31 GOTOOLCHAIN=local
timeout 300 go vet ./internal/cli/
```

Expected: FAIL to build: package `cli` reports `undefined: encodeBackupRequest` and `undefined: reportRestore`. Record the output verbatim.

- [ ] **Step 3: Implement**

1. `internal/cli/controller_backup.go`:
   - New `encodeBackupRequest(pass []byte, params *backup.Params) ([]byte, error)`. It returns `usagef("the passphrase must be valid UTF-8")` and no body for bytes that are not UTF-8 (`utf8.Valid`): JSON would replace them with U+FFFD, and `restore` could never be given the same passphrase. Otherwise it appends `{"passphrase":"`, then the passphrase bytes one by one (a `"` or `\` after a backslash, a byte below 0x20 as `\u00XX`, every other byte as it is, so multi-byte UTF-8 passes through), then `"`; when `params` is not nil it appends `,"argon2":` and `json.Marshal(params)`; then `}`. No Go string ever holds the passphrase. A comment says so.
   - `runBackup`: build `body` with `encodeBackupRequest(pass, params)`, then `clear(pass)` as now. `backup.BackupRequest` stays in use by the server and the tests; the CLI no longer fills it.
   - After the status check and before `receiveBackup`, parse `resp.Header.Get("Content-Type")` with `mime.ParseMediaType`. A parse error or a media type other than `application/octet-stream` returns `POST /api/v1/controller/backup: the answer is "<content type>", not application/octet-stream; no backup was written` (build it from `http.MethodPost` and `backupRoutePath` like the neighbouring errors); nothing is written to `--out`.
   - New `reportRestore(w io.Writer, res backup.RestoreResult, err error) error`: when `res.Tx != ""` it writes `restoreSummary(res)`, and it returns `err` unchanged. `restore` ends with `return reportRestore(cmd.OutOrStdout(), res, errors.Join(err, closeHost()))`. So a restore that finished but could not remove old set-aside files prints the summary and still exits non-zero with the error.
   - `readSecret` keeps `[]byte(s)`: `Options.ReadSecret` returns a string and lives outside this task.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/31 GOTOOLCHAIN=local
timeout 900 go test -count=1 -run 'W10Br|TestCb' ./internal/cli/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1800 go test -race -count=1 ./internal/cli/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf /root/.cache/w10a-tmp/31 && go clean -testcache
```

Expected: every `go test` line `ok`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add internal/cli/controller_backup.go internal/cli/controller_backup_fix_test.go
git commit -m "fix(cli): harden controller backup and restore" -m "Build the backup request from the passphrase bytes, refuse an answer that is not an octet stream, and print the restore summary when only the cleanup failed."
```

### Task 32: NPM banner warns on unreadable hosts

**Files:**
- Modify: `web/src/modules/npm/banner.tsx` (`AdminExposureBanner`)
- Modify: `web/src/modules/npm/types.ts` (`InstancesOutput`, `isInstancesOutput`; this task is its only editor)
- Modify: `web/src/modules/npm/fixtures.ts` (`npInstances`)
- Modify: `web/e2e/fixtures.ts` (the `npm.instance.list` read)
- Test (rewrite): `web/src/modules/npm/banner.test.tsx`
- Test (modify): `web/src/modules/npm/types.test.ts`, `web/src/modules/npm/fixtures.test.ts`, `web/src/shell.test.tsx`

**Interfaces:**
- Consumes: Contracts "NPM unreadable hosts" (Task 27 adds `Unreadable []string \`json:"unreadable"\`` to the Go `InstancesOutput`; this task is the web side). The host list read `listHosts` and `HOSTS_QUERY_KEY` of `web/src/api/client.ts` give the display names.
- Produces: `InstancesOutput.unreadable: string[]` in `web/src/modules/npm/types.ts`, required by `isInstancesOutput`; the banner texts of the Contracts entry. Nothing another task consumes.

**Items:** 333

**Depends on:** Task 27

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 1 = 5

The every-page banner warns about an exposed NPM admin interface. Today a host whose status read fails is silently left out of the list and a failed list read renders nothing, so the warning is silent exactly when it cannot be checked. Task 27 makes `npm.instance.list` name the hosts it could not read; this task shows them.

Behaviour (Contracts "NPM unreadable hosts"):
- `unreadable` non-empty: besides the existing exposure alerts, a warning alert reads `Could not check whether the NPM admin UI is exposed on: <names>.`, the names being the hosts' display names (from the host list) joined by `, `, in the order of `unreadable`. A host the host list does not name (or while that list is unavailable) shows its id.
- The list read fails, or its answer is not a valid instance list (a missing `unreadable` counts as invalid): one warning alert reads `Could not check whether the NPM admin UI is exposed.`
- Pending: nothing. The existing exposure alerts are unchanged.

- [ ] **Step 1: Write the failing tests**

Replace `web/src/modules/npm/banner.test.tsx` with this file. Its first test and the cache test are the existing ones with the answer shaped as the controller now sends it; the failure and unreadable cases are new, and two old cases ("renders nothing when the fleet read fails" and "... something that is not an instance list") now expect the warning.

```tsx
import { useState, type ReactElement } from "react"
import { fireEvent, screen, waitFor } from "@testing-library/react"
import type { QueryClient } from "@tanstack/react-query"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AdminExposureBanner } from "./banner"
import { npInstance } from "./fixtures"
import { npmFleetKey, type HostInstance } from "./types"

const BN_LIST = "POST /api/v1/actions/npm.instance.list/v1/read"
const BN_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const BN_WEB1 = "The NPM admin interface of web-1 is exposed at https://npm.example.com."
const BN_UNCHECKED = "Could not check whether the NPM admin UI is exposed."

function bnExposed(hostId: string, hostName: string, hostname: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true, adminExposure: { hostname, proxyHostId: 4, certificateId: 5 } }) }
}

function bnPlain(hostId: string, hostName: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true }) }
}

function bnFleet(instances: HostInstance[], unreadable: string[] = []) {
  return { body: { output: { instances, unreadable } } }
}

function bnHost(id: string, name: string): Host {
  return { id, name, isLocal: false, online: true, modules: [], ingestion: null }
}

function bnHosts(...hosts: Host[]) {
  return { body: { output: { hosts } } }
}

function bnRender(response: StubHandler, ui: ReactElement = <AdminExposureBanner />, extra: Record<string, StubHandler> = {}) {
  const calls = stubFetch({ [BN_LIST]: response, ...extra })
  const result = renderWithProviders(ui)
  return { calls, ...result }
}

function bnReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.method === "POST" && c.path === "/api/v1/actions/npm.instance.list/v1/read").length
}

async function bnSettled(client: QueryClient) {
  await waitFor(() => expect(["success", "error"]).toContain(client.getQueryState(npmFleetKey)?.status))
}

function BnToggle() {
  const [shown, setShown] = useState(true)
  return (
    <>
      <button onClick={() => setShown((s) => !s)}>Toggle</button>
      {shown && <AdminExposureBanner />}
    </>
  )
}

test("shows one warning for each host whose admin interface is exposed, linking to its NPM page", async () => {
  bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com"), bnPlain("h2", "web-2"), bnExposed("h3", "web-3", "admin.example.org")]))
  expect(await screen.findByText(BN_WEB1)).toBeTruthy()
  expect(screen.getByText("The NPM admin interface of web-3 is exposed at https://admin.example.org.")).toBeTruthy()
  expect(screen.queryByText(/web-2/)).toBeNull()
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-1" }).getAttribute("href")).toBe("/hosts/h1/npm")
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-3" }).getAttribute("href")).toBe("/hosts/h3/npm")
})

test("renders nothing when no host exposes its admin interface and every host could be read", async () => {
  const { client, calls } = bnRender(bnFleet([bnPlain("h1", "web-1")]))
  await bnSettled(client)
  expect(screen.queryByText(/NPM admin interface/)).toBeNull()
  expect(screen.queryByText(/Could not check/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
  expect(calls.some((c) => c.path === "/api/v1/actions/core.host.list/v1/read")).toBe(false)
})

test("warns about the hosts whose status could not be read, by display name, beside the exposure warnings", async () => {
  bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")], ["h2", "h3"]), <AdminExposureBanner />, {
    [BN_HOSTS]: bnHosts(bnHost("h1", "web-1"), bnHost("h2", "web-2"), bnHost("h3", "web-3")),
  })
  expect(await screen.findByText("Could not check whether the NPM admin UI is exposed on: web-2, web-3.")).toBeTruthy()
  expect(screen.getByText(BN_WEB1)).toBeTruthy()
  expect(screen.getAllByText(/NPM admin/)).toHaveLength(2)
})

test("names an unreadable host by its id while the host list is not available", async () => {
  bnRender(bnFleet([], ["h2"]), <AdminExposureBanner />, {
    [BN_HOSTS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } },
  })
  expect(await screen.findByText("Could not check whether the NPM admin UI is exposed on: h2.")).toBeTruthy()
})

test("warns that the check failed when the fleet read fails", async () => {
  const { client } = bnRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
  expect(screen.queryByText(/is exposed at/)).toBeNull()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("warns that the check failed when the controller answers with something that is not an instance list", async () => {
  const { client } = bnRender({ body: { output: { instances: "none", unreadable: [] } } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
})

test("warns that the check failed when the answer lacks the unreadable hosts", async () => {
  bnRender({ body: { output: { instances: [bnExposed("h1", "web-1", "npm.example.com")] } } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(screen.queryByText(BN_WEB1)).toBeNull()
})

test("the fleet read is kept for 60 seconds before a new mount reads it again", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const { calls } = bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")]), <BnToggle />)
    expect(await screen.findByText(BN_WEB1)).toBeTruthy()
    const toggle = () => fireEvent.click(screen.getByRole("button", { name: "Toggle" }))
    toggle()
    expect(screen.queryByText(BN_WEB1)).toBeNull()
    await vi.advanceTimersByTimeAsync(30_000)
    toggle()
    expect(await screen.findByText(BN_WEB1)).toBeTruthy()
    expect(bnReads(calls)).toBe(1)
    toggle()
    await vi.advanceTimersByTimeAsync(31_000)
    toggle()
    await waitFor(() => expect(bnReads(calls)).toBe(2))
  } finally {
    vi.useRealTimers()
  }
})

test("the banner has no accessibility violations", async () => {
  const { container } = bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")], ["h2"]), <AdminExposureBanner />, {
    [BN_HOSTS]: bnHosts(bnHost("h2", "web-2")),
  })
  await screen.findByText(BN_WEB1)
  await screen.findByText("Could not check whether the NPM admin UI is exposed on: web-2.")
  expect(await axe(container)).toHaveNoViolations()
})
```

Edit three existing test files with these unified diffs against `main`. `types.test.ts` pins that the guard requires `unreadable` (an array of strings), `fixtures.test.ts` pins the default fixture, and `shell.test.tsx` gives its typed fixture the new member.

`web/src/modules/npm/types.test.ts`:

```diff
@@ -68,5 +68,6 @@
 test("isInstancesOutput accepts the controller's JSON and an empty fleet", () => {
   expect(isInstancesOutput(JSON.parse(JSON.stringify(npInstances())))).toBe(true)
-  expect(isInstancesOutput({ instances: [] })).toBe(true)
+  expect(isInstancesOutput({ instances: [], unreadable: [] })).toBe(true)
+  expect(isInstancesOutput({ instances: [], unreadable: ["h1", "h2"] })).toBe(true)
 })
 
@@ -76,9 +77,12 @@
     ["null", null],
     ["an empty object", {}],
-    ["instances that are null", { instances: null }],
-    ["an instance that is a string", { instances: ["h1"] }],
-    ["an instance without a hostId", { instances: [{ ...host, hostId: undefined }] }],
-    ["a hostName that is a number", { instances: [{ ...host, hostName: 1 }] }],
-    ["a status that is not a status", { instances: [{ ...host, status: { deployed: true } }] }],
+    ["instances that are null", { instances: null, unreadable: [] }],
+    ["an instance that is a string", { instances: ["h1"], unreadable: [] }],
+    ["an instance without a hostId", { instances: [{ ...host, hostId: undefined }], unreadable: [] }],
+    ["a hostName that is a number", { instances: [{ ...host, hostName: 1 }], unreadable: [] }],
+    ["a status that is not a status", { instances: [{ ...host, status: { deployed: true } }], unreadable: [] }],
+    ["a missing unreadable", { instances: [] }],
+    ["an unreadable that is null", { instances: [], unreadable: null }],
+    ["an unreadable id that is a number", { instances: [], unreadable: [1] }],
   ]
   for (const [name, value] of rows) expect(isInstancesOutput(value), name).toBe(false)
@@ -91,5 +95,5 @@
   const exposure: AdminExposure = { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 }
   const host: HostInstance = { hostId: "h1", hostName: "edge-1", status: npInstance({ adminExposure: exposure }) }
-  const fleet: InstancesOutput = { instances: [host] }
+  const fleet: InstancesOutput = { instances: [host], unreadable: ["h2"] }
   const hosts: ProxyHostList = {
     items: [{
```

`web/src/modules/npm/fixtures.test.ts`:

```diff
@@ -69,5 +69,6 @@
 test("npInstances lists one host with the default instance and takes overrides", () => {
   expect(isInstancesOutput(npInstances())).toBe(true)
-  expect(npInstances()).toEqual({ instances: [{ hostId: "01JD0000000000000000000011", hostName: "edge-1", status: npInstance() }] })
-  expect(npInstances({ instances: [] })).toEqual({ instances: [] })
+  expect(npInstances()).toEqual({ instances: [{ hostId: "01JD0000000000000000000011", hostName: "edge-1", status: npInstance() }], unreadable: [] })
+  expect(npInstances({ instances: [] })).toEqual({ instances: [], unreadable: [] })
+  expect(npInstances({ unreadable: ["h2"] }).unreadable).toEqual(["h2"])
 })
```

`web/src/shell.test.tsx`:

```diff
@@ -198,4 +198,5 @@
     { hostId: "h2", hostName: "web-2", status: npInstance({ deployed: true }) },
   ],
+  unreadable: [],
 }
 
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10a-tmp/32
export TMPDIR=/root/.cache/w10a-tmp/32 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/npm/banner.test.tsx src/modules/npm/types.test.ts src/modules/npm/fixtures.test.ts src/shell.test.tsx
```

Expected: FAIL. The banner tests for the unreadable hosts and for the failed read cannot find `Could not check whether the NPM admin UI is exposed…`; `types.test.ts` fails on the rows "a missing unreadable", "an unreadable that is null" and "an unreadable id that is a number" (the guard accepts them) and `fixtures.test.ts` on the missing `unreadable: []`. Record the output verbatim.

- [ ] **Step 3: Implement**

1. `web/src/modules/npm/types.ts`: `InstancesOutput` gains `unreadable: string[]`. `isInstancesOutput` also requires `unreadable` to be an array whose every member is a string (so a controller that does not send it fails the guard).
2. `web/src/modules/npm/fixtures.ts`: `npInstances` returns `unreadable: []` before applying its overrides.
3. `web/e2e/fixtures.ts`: the `npm.instance.list` read returns `{ instances: [], unreadable: [] }`, so the Playwright run answers with the shape the guard accepts.
4. `web/src/modules/npm/banner.tsx`, `AdminExposureBanner`:
   - Keep the fleet query (`npmFleetKey`, `readInstances`, `staleTime: 60_000`).
   - Add a second query over `HOSTS_QUERY_KEY` and `listHosts` (imported from `@/api/client`, the same key the shell uses), `enabled` only when the fleet read succeeded with a non-empty `unreadable`, so no host list is read when nothing is unreadable. Call both hooks before any early return.
   - A failed fleet query (the request fails, or `readInstances` throws because `isInstancesOutput` refuses the answer) renders one warning alert with the text `Could not check whether the NPM admin UI is exposed.` A pending fleet query renders nothing.
   - Otherwise render the exposure alerts exactly as today, followed, when `unreadable` is non-empty, by one warning alert whose text is `Could not check whether the NPM admin UI is exposed on: <names>.`. Each name is the `name` of the host in the host list with that id when that query succeeded, else the id.
   - Both new alerts are `variant="warning"`, `role="status"` and `className="mb-4"`, like the existing alert (the failure test also asserts there is no `alert` role). Put the text in an `AlertDescription`, with the `AlertTitle` `Admin exposure not checked`, in a small component of the same file used by both cases.
   - Rewrite the comment above the component: it no longer stays silent on a failed read.
   - Nothing else changes: the Go side is Task 27, and no other file is edited.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10a-tmp/32 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/npm src/shell.test.tsx
timeout 1200 make lint-web test-web
rm -rf /root/.cache/w10a-tmp/32 && go clean -testcache
```

Expected: the vitest run passes, `make lint-web test-web` ends with eslint at zero warnings, `tsc --noEmit` clean and the whole suite passing. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add web/src/modules/npm/banner.tsx web/src/modules/npm/types.ts web/src/modules/npm/fixtures.ts web/e2e/fixtures.ts web/src/modules/npm/banner.test.tsx web/src/modules/npm/types.test.ts web/src/modules/npm/fixtures.test.ts web/src/shell.test.tsx
git commit -m "fix(web): warn when NPM hosts cannot be checked"
```

### Task 33: Threat model

**Files:**
- Create: `docs/security/threat-model.md`
- Test (new): `internal/docs/threat_model_test.go`
- Read for style: `docs/security/parsers.md`; for the facts, the files named in each area below, the spec's sections 1 to 10 (`docs/dforge/specs/2026-10-21-v1-completion-design.md`) and `docs/adr/0003-agent-protocol.md`, `0005-storage-and-secrets.md`, `0006-api-and-cli.md`, `0009-host-execution-safety.md`, `0010-web-certificate-authority.md`

**Interfaces:**
- Consumes: Contracts "Threat model outline" (the headings and the three rules below it). From the existing `internal/docs` test files: `dcRead`, `dcRoot`, `dcMissing` and `dcLinks` (`helpers_test.go`) and `dnFenceTags`, `dnHTMLRE` and `dnInlineRE` (`modules_b_test.go`). The mitigations describe what Tasks 10 and 20 to 32 merged.
- Produces: `docs/security/threat-model.md`, which the two-reviewer security review of plan `w10b` reads as the base of its brief.

**Depends on:** Task 10, Task 20, Task 21, Task 22, Task 23, Task 24, Task 25, Task 26, Task 27, Task 28, Task 29, Task 30, Task 31, Task 32

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

The page is the written threat model of the spec's hardening section: assets, actors, trust boundaries and, per area, threats, mitigations and residual risk. It describes the product as it is on `main` after Tasks 10 and 20 to 32; it never promises future work and never names a plan, wave, task or register row. The existing `docs/user` style tests do not read `docs/security`, so the test below applies the style rules to this page itself.

- [ ] **Step 1: Write the failing test**

Create `internal/docs/threat_model_test.go`. It checks the exact outline, that every section holds bullets, that every mitigation names an existing repository path in backticks, that each area states a set of facts and names the paths of the fixes of this release, that `Known residual risks` covers every open question without naming a register row, and the documentation style.

````go
package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const w10tmPage = "docs/security/threat-model.md"

// w10tmOutline is every heading of level one to three, in order.
var w10tmOutline = []string{
	"# Threat model",
	"## Scope",
	"## Assets",
	"## Actors",
	"## Trust boundaries",
	"### Browser and controller",
	"### Proxy and controller",
	"### Controller and agent",
	"### Agent and host",
	"### Controller and cloud APIs",
	"## Areas",
	"### Authentication",
	"### Agent protocol",
	"### Command execution",
	"### Compose execution",
	"### Secret handling and delivery",
	"### Firewall",
	"### Cloud credentials",
	"### Exposure",
	"### DERP host",
	"## Known residual risks",
}

var w10tmParts = []string{"Threats", "Mitigations", "Residual risk"}

// w10tmMinBullets is the fewest bullets a section of the page holds.
var w10tmMinBullets = map[string]int{
	"Scope": 2, "Assets": 6, "Actors": 5,
	"Browser and controller": 2, "Proxy and controller": 2, "Controller and agent": 2,
	"Agent and host": 2, "Controller and cloud APIs": 2,
}

// w10tmFacts are the terms an area states somewhere in its three parts,
// verbatim.
var w10tmFacts = map[string][]string{
	"Authentication":               {"TOTP", "CSRF", "argon2id", "SameSite", "rate limit", "scope"},
	"Agent protocol":               {"client certificate", "enrollment token", "revoked", "SAN", "TLS 1.3"},
	"Command execution":            {"allowlist", "argument vector", "openat2", "root"},
	"Compose execution":            {"provider", "COMPOSE_EXPERIMENTAL_GIT_REMOTE", "--no-build", "operator-authored"},
	"Secret handling and delivery": {"XChaCha20-Poly1305", "reveal", "audit", "backup", "passphrase"},
	"Firewall":                     {"lockout", "revert", "snapshot", "break-glass", "lock"},
	"Cloud credentials":            {"sealed", "write-only", "uncertain", "token"},
	"Exposure":                     {"X-Darkpanel-Client", "revoked", "port 81", "banner", "unreadable"},
	"DERP host":                    {"-verify-clients", "darkpanel-derp", "tailscaled", "pinned", "signature"},
}

// w10tmMitigationPaths are the repository paths an area's mitigations name.
var w10tmMitigationPaths = map[string][]string{
	"Authentication":               {"internal/controller/auth", "internal/controller/api", "internal/controller/audit", "internal/core/store/migrations/controller"},
	"Agent protocol":               {"internal/transport", "internal/controller/fleet", "internal/agent/daemon"},
	"Command execution":            {"internal/platform"},
	"Compose execution":            {"internal/modules/docker/compose"},
	"Secret handling and delivery": {"internal/core/secret", "internal/controller/backup"},
	"Firewall":                     {"internal/modules/firewall"},
	"Cloud credentials":            {"internal/controller/cloud"},
	"Exposure":                     {"internal/controller/listen", "internal/modules/npm/npmapi", "web/src/modules/npm/banner.tsx"},
	"DERP host":                    {"internal/modules/derp/artifact", "release/releasetool"},
}

// w10tmOpen are the terms the open security questions use; each appears in
// a bullet of Known residual risks.
var w10tmOpen = []string{"Origin", "RFC 1918", "forward host", "port 81", "port 3000", "digest", "openpgp"}

var (
	w10tmHeadingRE  = regexp.MustCompile(`^(#{1,6}) +(\S.*?)\s*$`)
	w10tmSpanRE     = regexp.MustCompile("`([^`]+)`")
	w10tmRepoPathRE = regexp.MustCompile(`^(internal|cmd|docs|release|scripts|test|web|schemas|\.github)/\S*$`)
	w10tmInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]+|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9]+|coming soon|will be added)\b`)
	w10tmRowRE      = regexp.MustCompile(`(?i)\b(register|rows? [0-9]+)\b`)
)

// w10tmDoc is the page split by heading: Lines holds each section's lines
// under its heading, keyed by the section name; an area's parts are keyed
// "<area>/<part>".
type w10tmDoc struct {
	headings []string // levels one to three, as "## Scope"
	parts    map[string][]string
	lines    map[string][]string
	level4   map[string][]string // the level-four headings under each level-three heading
	bad      []string            // headings of level five or six
}

func w10tmParse(text string) w10tmDoc {
	d := w10tmDoc{parts: map[string][]string{}, lines: map[string][]string{}, level4: map[string][]string{}}
	fenced := false
	var current, area, part string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		m := w10tmHeadingRE.FindStringSubmatch(line)
		if m == nil || fenced {
			switch {
			case part != "":
				d.parts[area+"/"+part] = append(d.parts[area+"/"+part], line)
			case current != "":
				d.lines[current] = append(d.lines[current], line)
			}
			continue
		}
		level, name := len(m[1]), m[2]
		switch {
		case level <= 3:
			d.headings = append(d.headings, m[1]+" "+name)
			current, part = name, ""
			if level == 3 {
				area = name
			}
		case level == 4:
			d.level4[area] = append(d.level4[area], name)
			part = name
		default:
			d.bad = append(d.bad, line)
		}
	}
	return d
}

// w10tmBullets returns the bullets among lines: a line that starts with "- "
// and the non-blank lines that follow it up to the next bullet.
func w10tmBullets(lines []string) []string {
	var out []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "- "):
			out = append(out, line)
		case len(out) > 0 && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#"):
			out[len(out)-1] += " " + strings.TrimSpace(line)
		}
	}
	return out
}

func TestW10TmHasItsOutline(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	if !slices.Equal(d.headings, w10tmOutline) {
		t.Errorf("%s has the headings\n%s\nwant\n%s", w10tmPage, strings.Join(d.headings, "\n"), strings.Join(w10tmOutline, "\n"))
	}
	if len(d.bad) > 0 {
		t.Errorf("%s has headings below level four: %q", w10tmPage, d.bad)
	}
	for _, area := range w10tmAreas() {
		if !slices.Equal(d.level4[area], w10tmParts) {
			t.Errorf("area %q has the subheadings %q, want exactly %q", area, d.level4[area], w10tmParts)
		}
	}
	for section, subs := range d.level4 {
		if !slices.Contains(w10tmAreas(), section) && len(subs) > 0 {
			t.Errorf("%q has the subheadings %q; only an area has any", section, subs)
		}
	}
}

// w10tmAreas are the headings between "## Areas" and "## Known residual risks".
func w10tmAreas() []string {
	from, to := slices.Index(w10tmOutline, "## Areas"), slices.Index(w10tmOutline, "## Known residual risks")
	var areas []string
	for _, h := range w10tmOutline[from+1 : to] {
		areas = append(areas, strings.TrimPrefix(h, "### "))
	}
	return areas
}

func TestW10TmSectionsHoldBullets(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for section, least := range w10tmMinBullets {
		if got := len(w10tmBullets(d.lines[section])); got < least {
			t.Errorf("section %q has %d bullets, want at least %d", section, got, least)
		}
	}
	for _, area := range w10tmAreas() {
		for _, part := range w10tmParts {
			if len(w10tmBullets(d.parts[area+"/"+part])) == 0 {
				t.Errorf("area %q has no bullet under %q", area, part)
			}
		}
	}
}

func TestW10TmMitigationsNameExistingRepositoryPaths(t *testing.T) {
	root := dcRoot(t)
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, area := range w10tmAreas() {
		for _, bullet := range w10tmBullets(d.parts[area+"/Mitigations"]) {
			found := 0
			for _, m := range w10tmSpanRE.FindAllStringSubmatch(bullet, -1) {
				if !w10tmRepoPathRE.MatchString(m[1]) {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(m[1], "/")))); err != nil {
					t.Errorf("area %q: the mitigation names `%s`, which does not exist: %v", area, m[1], err)
					continue
				}
				found++
			}
			if found == 0 {
				t.Errorf("area %q: a mitigation names no existing repository path in backticks: %.120s", area, bullet)
			}
		}
	}
}

func TestW10TmAreasStateTheirFacts(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, area := range w10tmAreas() {
		var all []string
		for _, part := range w10tmParts {
			all = append(all, d.parts[area+"/"+part]...)
		}
		for _, name := range dcMissing(strings.Join(all, "\n"), w10tmFacts[area]) {
			t.Errorf("area %q does not mention %q", area, name)
		}
		mitigations := strings.Join(d.parts[area+"/Mitigations"], "\n")
		for _, name := range dcMissing(mitigations, w10tmMitigationPaths[area]) {
			t.Errorf("the mitigations of area %q do not name `%s`", area, name)
		}
	}
}

func TestW10TmKnownResidualRisks(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	bullets := w10tmBullets(d.lines["Known residual risks"])
	if len(bullets) < len(w10tmOpen) {
		t.Errorf("Known residual risks has %d bullets, want at least %d: one for each open security question", len(bullets), len(w10tmOpen))
	}
	all := strings.Join(bullets, "\n")
	for _, name := range dcMissing(all, w10tmOpen) {
		t.Errorf("Known residual risks does not mention %q", name)
	}
	for _, re := range []*regexp.Regexp{w10tmInternalRE, w10tmRowRE} {
		if m := re.FindString(all); m != "" {
			t.Errorf("Known residual risks names %q; it never names a register row, plan, wave or task", m)
		}
	}
}

func TestW10TmFollowsTheDocumentationStyle(t *testing.T) {
	text := dcRead(t, w10tmPage)
	if !strings.HasPrefix(text, "# Threat model\n") {
		t.Errorf("%s must start with the title", w10tmPage)
	}
	open := false
	for i, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "```"); ok {
			switch {
			case open && rest != "":
				t.Errorf("%s:%d: a closing fence carries %q", w10tmPage, i+1, rest)
			case !open && !slices.Contains(dnFenceTags, rest):
				t.Errorf("%s:%d: a code block tagged %q, want one of %v", w10tmPage, i+1, rest, dnFenceTags)
			}
			open = !open
			continue
		}
		if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
			t.Errorf("%s:%d: HTML in %q", w10tmPage, i+1, line)
		}
	}
	if open {
		t.Errorf("%s ends inside a code block", w10tmPage)
	}
	if m := w10tmInternalRE.FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row, or promises future work", w10tmPage, m)
	}
	dcLinks(t, w10tmPage)
}
````

- [ ] **Step 2: Run the test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/33
export TMPDIR=/root/.cache/w10a-tmp/33 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'W10Tm' ./internal/docs/
```

Expected: FAIL. Every test stops with `docs/security/threat-model.md: open …/docs/security/threat-model.md: no such file or directory`, the failure of `dcRead`. Record the output verbatim.

- [ ] **Step 3: Write the document**

Write `docs/security/threat-model.md` in the documentation style of `docs/security/parsers.md`: English, plain Markdown, ATX headings, sentence-case headings, fenced blocks tagged `sh`, `json` or `text` (the page needs none), no HTML, present tense. It has exactly the headings of Contracts "Threat model outline"; each of the nine areas has exactly `#### Threats`, `#### Mitigations` and `#### Residual risk`, each with at least one bullet. The title is followed by a short paragraph that says what the page is for: the base of a security review and of the decision which risk to accept.

Before you write a fact, open the file that implements it and check it; where a file or behaviour differs from the description below, the repository wins and you cite the real path. The test needs these things and you add more of your own:

- Every bullet under a `#### Mitigations` heading holds one mitigation and ends with the repository paths that implement it, each in backticks (`internal/controller/auth`, a file or a package directory, no glob and no trailing dots). Every path exists. Host paths such as `/etc/darkpanel/controller.key` may appear too, but they do not count.
- Each area names, in its three parts together, the terms of the right-hand column of the table below, verbatim (the test compares case-sensitively), and its mitigations name the listed paths.

| Area | Terms the area must state | Paths its mitigations must name |
|---|---|---|
| Authentication | `TOTP`, `CSRF`, `argon2id`, `SameSite`, `rate limit`, `scope` | `internal/controller/auth`, `internal/controller/api`, `internal/controller/audit`, `internal/core/store/migrations/controller` |
| Agent protocol | `client certificate`, `enrollment token`, `revoked`, `SAN`, `TLS 1.3` | `internal/transport`, `internal/controller/fleet`, `internal/agent/daemon` |
| Command execution | `allowlist`, `argument vector`, `openat2`, `root` | `internal/platform` |
| Compose execution | `provider`, `COMPOSE_EXPERIMENTAL_GIT_REMOTE`, `--no-build`, `operator-authored` | `internal/modules/docker/compose` |
| Secret handling and delivery | `XChaCha20-Poly1305`, `reveal`, `audit`, `backup`, `passphrase` | `internal/core/secret`, `internal/controller/backup` |
| Firewall | `lockout`, `revert`, `snapshot`, `break-glass`, `lock` | `internal/modules/firewall` |
| Cloud credentials | `sealed`, `write-only`, `uncertain`, `token` | `internal/controller/cloud` |
| Exposure | `X-Darkpanel-Client`, `revoked`, `port 81`, `banner`, `unreadable` | `internal/controller/listen`, `internal/modules/npm/npmapi`, `web/src/modules/npm/banner.tsx` |
| DERP host | `-verify-clients`, `darkpanel-derp`, `tailscaled`, `pinned`, `signature` | `internal/modules/derp/artifact`, `release/releasetool` |

**Scope** (at least 2 bullets). What the model covers: the controller, the agents it manages, the modules shipped in v1 (firewall, Docker, Tailscale, tailnet, DERP, Nginx Proxy Manager, Cloudflare, panel exposure), the CLI, the web UI, backup and restore, and the update path. What it does not cover: a compromised root account on a host (the agent runs as root, so a root attacker already holds the host), physical access, the security of Tailscale, Cloudflare, Let's Encrypt and Docker themselves, and a malicious administrator (an admin can do everything an admin may do; the model limits what a lower scope, a stolen credential or a mistake can do).

**Assets** (at least 6 bullets, one asset each, with where it lives). The controller database `controller.db` (hosts, audit log, sealed secrets, the sealed private keys of both certificate authorities, quarantine state); the key files `controller.key` and `agent.key`, which seal every secret column; the agent's identity (its key and certificate in the agent's TLS directory); sessions, API tokens, password hashes and TOTP secrets; cloud credentials (Tailscale API key and auth keys, the Cloudflare API token and tunnel tokens); the Nginx Proxy Manager admin password, its Let's Encrypt private keys and its database; container environment values; backup files and their passphrases; the reachability of every host (what the firewall allows); the trusted release keys in the binary and the signed release and derper artifacts; the DERP node key. Say for each what a loss of confidentiality, integrity or availability costs, in one clause.

**Actors** (at least 5 bullets). An unauthenticated network attacker; an authenticated user at each scope (`read`, `operate`, `admin`); a holder of a stolen session cookie or API token; hostile web content in an admin's browser (cross-site requests, script injection); the operator themselves, whose mistake can lock them out; an unprivileged local user on a managed host; a container workload on a managed Docker host, including one on a network joined through `npm.network.attach`; a compromised agent host; a compromised controller; and a compromised third party (Nginx Proxy Manager's API, a cloud API, an upstream release). State what each can reach.

**Trust boundaries** (each `###` at least 2 bullets: what crosses it and what authenticates and limits the crossing).

- Browser and controller. HTTPS listeners on loopback and the Tailscale interface by default (plain HTTP only on 127.0.0.1); certificate from `tailscale cert` or the internal web root (ECDSA P-256); cookie sessions (Secure on HTTPS, HttpOnly, SameSite=Strict) plus a CSRF token, scoped expiring API tokens, one middleware that denies every route not on the public list whatever the method, a strict Content-Security-Policy and the other headers of `internal/controller/api/middleware.go`.
- Proxy and controller. The runtime listeners of a panel exposure: HTTPS only, a private bridge address, one allowed peer address (the proxy), one client-address header (`X-Darkpanel-Client` for Nginx Proxy Manager, `Cf-Connecting-IP` for a tunnel) with every other forwarded header ignored, a refused request whose forwarded protocol is not https, an Origin that must match the public host, and durable revocation.
- Controller and agent. The agent dials out and opens no port; mutual TLS 1.3 over a yamux session; ALPN `darkpanel-agent/1` requires a client certificate and `darkpanel-enroll/1` serves only enrollment; the controller's separate Ed25519 authority; controller and agent identities as URI SANs.
- Agent and host. The agent is root: allowlisted commands with fixed environments, files opened with `openat2` under root-owned directories, systemd over D-Bus, the Docker Engine API and the Tailscale local API; the agent socket is root-only and checked with `SO_PEERCRED`.
- Controller and cloud APIs. Outbound HTTPS to the Tailscale and Cloudflare APIs with sealed credentials, 15-second timeouts, retries only for reads and deletes, and an explicit procedure for a write whose outcome is uncertain. Let's Encrypt is reached only through Nginx Proxy Manager on the agent's host.

**Areas.** Describe each area from the facts below. Threats are the ways it can be attacked or fail; mitigations are what exists now; the residual risk is what stays after them, stated plainly and with who must accept it. Add facts of your own from the code, but none that you did not verify.

- Authentication.
  - Threats: password guessing and credential stuffing; a stolen password; a replayed or brute-forced TOTP code; session theft; cross-site request forgery against a cookie session; a route that forgets its check; a lower scope reaching an admin action; an attacker erasing their traces.
  - Mitigations: users with argon2id password hashes and a bounded number of hashes at a time; TOTP (RFC 6238) required to sign in, with a code never accepted twice (`internal/controller/auth`); a login rate limit by user name and by source address with a lock, so a flood of new keys cannot clear a running lock; sessions with an idle and an absolute lifetime and only their hash stored; API tokens with a `read`, `operate` or `admin` scope, an expiry and only their hash stored; one authentication middleware on every listener that applies to every method and denies unless the route is public, SameSite=Strict HttpOnly cookies and a CSRF token, and the Origin check of runtime listeners (`internal/controller/api`); break-glass user commands only on the controller socket, root-only through `SO_PEERCRED`; an audit entry for every action (`internal/controller/audit`), including the rows the system services (the Cloudflare hostname driver and the tailnet key sweep) write, which the audit table accepts since its phase check lists the phases those services use (`internal/core/store/migrations/controller`).
  - Residual risk: the rate limiter's counters live in memory, so a restart clears them; TOTP is SHA-1 because authenticator apps implement it; the web listeners accept TLS 1.2 for browsers (the agent transport requires 1.3); an admin whose session and CSRF token are both stolen acts as that admin until the session ends. Do not list the Origin question here; it belongs in `Known residual risks`.
- Agent protocol.
  - Threats: an agent impersonating the controller; a stolen agent key or certificate; enrollment token theft, guessing or reuse; a CSR that claims another host's identity; a stale or revoked agent staying connected; a host offline for a long time losing its identity; downgrade to an unsupported protocol version.
  - Mitigations: separate authorities and certificates for the two roles, with the controller SAN pinned by every agent and a host SAN required of every agent; TLS 1.3 with a client certificate and ALPN `darkpanel-agent/1`; an enrollment listener on `darkpanel-enroll/1` that serves only enrollment; enrollment tokens of 128 random bits stored hashed, valid for one hour, compared in constant time, consumed atomically and rate limited, bound to one host id, with the CSR's claimed identity ignored (`internal/controller/fleet`); certificates of one year renewed over the live session, revocation by serial checked at every handshake and closing the live session (`internal/transport`, `internal/agent/daemon`); an agent listener that binds loopback and the Tailscale interface by default, never all interfaces; a frozen hello and update path so an agent outside the version window can still be updated; a restored controller issuing enrollment tokens only in quarantine and for existing hosts (`internal/controller/backup`).
  - Residual risk: a stolen agent key acts as that host until it is revoked; a compromised controller can send any declared action to every enrolled agent (its power is limited by the declared schemas and the allowlist on the agent, not removed); a host offline longer than a certificate's life must enroll again.
- Command execution.
  - Threats: argument or option injection, a binary found through `PATH`, an inherited environment, a symlink planted by an unprivileged process to redirect a root write, a marked block corrupted by hand, a parser fed hostile tool output.
  - Mitigations: the exec provider runs only allowlisted absolute binary paths with a fixed minimal environment, argument vectors only and `--` before values that could be read as options; input schemas constrain every value that reaches a program; the file provider writes only declared paths and marked blocks, opens the parent with `openat2` and `RESOLVE_NO_SYMLINKS`, requires a root-owned parent that others cannot write, writes through a temporary file and a rename, and refuses duplicated, nested or unterminated markers; APIs are preferred to tools (Docker Engine API, Tailscale local API, systemd over D-Bus); an ownership manifest drives uninstall; only `internal/platform` runs programs or writes host files; every parser of tool output and untrusted input is listed in `docs/security/parsers.md` with a fuzz target that `scripts/fuzz.sh` runs.
  - Residual risk: the agent is root, so a flaw in an allowlisted tool is a root flaw; the file provider defends against an unprivileged process, not against root; package scripts run as root when a module installs or pins a package.
- Compose execution.
  - Threats: a Compose file that runs a host program, a remote `include` or `extends` reaching Git or an OCI registry, a build that starts a host plugin, a project that touches Darkpanel's own objects, hostile Compose output.
  - Mitigations: Compose files are operator-authored and trusted like any root-run configuration, Darkpanel never writes them, running them is an `admin` action and the plan says it runs the project's file as root; validation requires absolute paths owned by root or the directory's owner with no symlink; a service with a `provider` key and any key outside the documented set is refused; both remote loaders are switched off with `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false` and the OCI equivalent, `up` always runs with `--no-build`, and the environment is only the Darkpanel-owned `DOCKER_CONFIG` and `HOME`; a project named `darkpanel*` or touching an object labelled `darkpanel.module` is refused with `docker.owned`; the resolved configuration is read as strict JSON and fuzzed (`internal/modules/docker/compose`, `internal/modules/docker/compose_check.go`).
  - Residual risk: a Compose file can still mount host paths and start privileged containers, which is root-equivalent by design; the key allowlist follows the Compose version installed on the host; private registry pulls through Compose are unsupported.
- Secret handling and delivery.
  - Threats: a secret in a log, a plan, a journal, an audit row, an error, a process list or shell history; the key file leaking; a secret shown to a user who should not see it; a stolen or tampered backup; a plaintext copy left on disk; a secret surviving in a removed component.
  - Mitigations: secret columns sealed with XChaCha20-Poly1305 under a per-role key file (mode 0600) with the table, column and row id as associated data, and the `secret` tag driving sealing and redaction (`internal/core/secret`); the CLI takes secrets only from a prompt or standard input, never a flag; a reveal action returns one stored secret once, needs `admin`, writes its audit entry before it answers, sets `Cache-Control: no-store`, and is never journaled, cached, relayed through server-sent events or stored in a change, and has no `--local` form (`internal/core/lifecycle/reveal.go`, `internal/controller/api/reveal.go`); container logs and the Nginx Proxy Manager container's logs are filtered for secrets (`internal/modules/docker/logsecrets.go`); a backup is one archive framed and sealed with a key derived from the operator's passphrase by argon2id, authenticated to its end so truncation is detected, read from the terminal or a root-owned 0600 file (`internal/controller/backup`); a restore stages with mode 0600 and gives owner and mode to each staged file through one descriptor that refuses links, and a backup keeps its plaintext copy of the database in a private directory that the controller sweeps at startup and reads the key file with a bound, so a key that grew is refused; the CLI builds the backup request from the passphrase bytes; removing an Nginx Proxy Manager instance deletes its upgrade archives, which hold Let's Encrypt private keys and the NPM database (`internal/modules/npm`).
  - Residual risk: the key files are readable by the service user and root, so anyone who reads them reads every sealed value; a passphrase typed at a terminal passes through a Go string in the terminal reader before it is copied; the strength of a backup passphrase is the operator's; losing `controller.key` without a backup loses the sealed data.
- Firewall.
  - Threats: a change that cuts the operator's SSH session, the panel's path or the agent's link; a change that cannot be reverted; a torn read of a half-applied state; Docker publishing a port around UFW; a rule the operator did not mean to open; the guard itself being wrong.
  - Mitigations: a lockout guard predicts the effect of a change on the protected flows and refuses it unless explicitly overridden (`internal/modules/firewall/predict`, `internal/modules/firewall/eval`); a change stays pending until confirmed and the host reverts it by itself after 60 seconds, so the revert happens even when the client disconnects; snapshots before every change and a break-glass rollback and restore through the agent socket that work without the controller (`internal/cli`, `internal/modules/firewall`); the module holds one read-write lock per instance, taken for writing by every path that changes UFW's files, the loaded rules or the Docker block and for reading by every status read and plan preparation, so a plan or a status read never sees a state between a file write and the reload (`internal/modules/firewall`); the Docker integration is a marked block with its own implementation that pins a container's allowance to its published binding (`internal/modules/firewall/dockerfw`); the exposure view computes reachability per port and the harness checks it against an external scanner; the parsers of UFW files and ruleset output are fuzzed.
  - Residual risk: a break-glass process outside the agent is not covered by the lock, because it runs when the agent cannot be reached; an overridden guard can still cut the operator off, and the revert timer is the only net; Docker network modes the integration cannot protect are reported as unprotected; manual rules are shown and never removed.
- Cloud credentials.
  - Threats: a Tailscale or Cloudflare credential leaking; a write sent twice or lost after it committed; credentials deleted while cloud objects still depend on them; an over-privileged token; a leaked auth key.
  - Mitigations: credentials stored sealed in each module's own table and write-only through the API except through secret delivery; one HTTP client with 15-second timeouts that retries only reads and deletes; every write has an uncertain-outcome procedure, the effect becomes unresolved and the change `needs_repair`, and nothing settles it automatically or re-sends it blindly (`internal/controller/cloud`); a check of pending cleanup before any path deletes a credentials row; every auth key Darkpanel creates is recorded first and swept by description; connect probes read capabilities and reports write capabilities as unverified until a write succeeds; the cloud fakes test every fault (`internal/controller/cloud`, `internal/modules/cloudflare`, `internal/modules/tailnet`).
  - Residual risk: Cloudflare's configuration `PUT` can overwrite a concurrent write that nothing detects; an account token holds all the permissions Darkpanel asked for, so its theft is as bad as the module's reach; a Tailscale key whose create timed out may exist until the sweep removes it.
- Exposure.
  - Threats: the panel reachable by the wrong party through a proxy; spoofed client-address or protocol headers; cross-site requests through an exposure; Nginx Proxy Manager's admin UI exposed by mistake or unchecked; a proxy host that points at the panel or at NPM itself; a compromised NPM exhausting an agent; a revoked exposure coming back.
  - Mitigations: runtime listeners accept only HTTPS from one allowed peer, read exactly one client header set by a proxy Darkpanel configured and ignore `X-Forwarded-For` and `X-Real-IP`, refuse a request whose forwarded protocol is not https, and refuse an unsafe request whose Origin is not the exposure's public host, which is compared in lower case without a trailing dot (`internal/controller/listen`, `internal/controller/api`); login over an exposure requires TOTP, every page shows a banner, and revocation is a durable `revoked` state that is checked on every request and stream and that nothing reverses, with `sudo darkpanel panel listener revoke` as the break-glass (`internal/modules/panel`); NPM's admin port 81 is published on 127.0.0.1 only, and its HTTPS exposure is an opt-in action with a warning banner on every page that also warns, since the banner reads the list again, about each host whose status could not be read (`unreadable`) and about a failed read (`web/src/modules/npm/banner.tsx`, `internal/modules/npm`); proxy hosts to NPM's own port 81 and to the panel gateway are refused as reserved (`internal/modules/npm/proxy`); the agent's NPM client limits the size of every answer and drains refusals (`internal/modules/npm/npmapi`); the public check honours the sources a Docker binding denies; each runtime listener keeps its own TLS session tickets, so a session resumed on one listener is not accepted by another.
  - Residual risk: the open questions about Origin, forward hosts and NPM's ports are listed in `Known residual risks`; a container on a network joined through the NPM attach action shares that network with NPM; Let's Encrypt's duplicate-certificate limit can refuse a repeated add.
- DERP host.
  - Threats: a tampered derper binary; a downgrade to an older artifact; a derper that relays for strangers; a version mismatch that makes client verification fail open or closed wrongly; a service account that Darkpanel did not create being removed; the probe used against internal targets.
  - Mitigations: derper is built by the release workflow from upstream at a pinned version with a pinned module environment, listed in a manifest of per-architecture SHA-256 digests that is verified through the release's signature (`internal/modules/derp/artifact`, `release/releasetool`); the manifest parser refuses duplicate members; the agent installs it as a systemd service under the system user `darkpanel-derp` with `NoNewPrivileges=yes` and only `CAP_NET_BIND_SERVICE`, and records that it created the user only after `useradd` succeeded, so uninstall never removes an account somebody else made (`internal/modules/derp`); derper runs with `-verify-clients`, which checks every client's node key against the host's own `tailscaled` and refuses the client when `tailscaled` fails; the host's `tailscaled` is pinned and held at the derper version (`internal/modules/tailscale`), and a pin that fails rolls the package back even when the forward step's context has been cancelled (check how `internal/modules/tailscale/package_pin.go` runs the rollback before you say this); the node key file is mode 0600; the probe uses a throwaway node key; the latency probe URL is built only from host names made of letters, digits, hyphens and dots.
  - Residual risk: holding `tailscaled` at derper's version delays its security updates on that host; the probe's target ranges and the error text of a failed probe are an open question listed in `Known residual risks`; derper itself answers unauthenticated STUN and HTTPS probes from the internet.

**Known residual risks.** A short paragraph says these are the security questions that remain open after the fixes of this release and that none is described as accepted. Then one bullet per question, seven in all, each stating the question, what is exposed and the choices; none names a register row, a plan or a task. Use these words verbatim, because the test looks for them:
- `Origin`: a request without an Origin header is accepted for a cookie session on an unsafe method through a runtime listener, so only the CSRF token guards it; whether to require Origin for sessions is undecided.
- `RFC 1918`: the DERP node probe accepts RFC 1918 and CGNAT targets and returns raw TLS error text that can name internal certificate names; whether to refuse those ranges or to record the decision is undecided.
- `forward host`: the reserved-target check of a proxy host resolves no names, so a forward host that resolves to loopback, to NPM's admin port or to the panel gateway passes; whether to resolve it as NPM does is undecided.
- `port 81`: NPM serves its admin login on port 81 inside its container, so any container on a network joined through the attach action can reach it; only the host side is bound to loopback; whether to restrict it inside the container or to accept it is undecided.
- `port 3000`: the guard covers NPM's port 81 only, while NPM's backend API listens on port 3000 and whether it binds loopback only in the pinned image is unverified.
- `digest`: the label digest in Docker network and volume effect documents is an unsalted SHA-256, and plan diffs outside the effect document show raw label values.
- `openpgp`: the `openpgp` package of `golang.org/x/crypto` carries an advisory (GO-2026-5932) with no tagged fix; Darkpanel does not call it; the open choice is to keep the current pin until a tagged release carries the fix, or to remove the package from the module graph.

- [ ] **Step 4: Run the test to verify it passes**

```sh
export TMPDIR=/root/.cache/w10a-tmp/33 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'W10Tm' ./internal/docs/
timeout 300 go test -count=1 ./internal/docs/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 600 go test -race -count=1 ./internal/docs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf /root/.cache/w10a-tmp/33 && go clean -testcache
```

Expected: every `go test` line `ok`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Read the finished page once from top to bottom for a sentence that promises future work or names an internal identifier. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add docs/security/threat-model.md internal/docs/threat_model_test.go
git commit -m "docs(security): add the threat model"
```

### Task 34: Runbook

**Files:**
- Create: `docs/runbook.md`
- Modify: `docs/user/README.md` (a link under "When something goes wrong")
- Modify: `docs/user/lockout-recovery.md` (one sentence that links the runbook)
- Modify: `docs/user/administration.md` (one sentence in "Settle the restore quarantine" that links the runbook's agent re-enrollment)
- Test (new): `internal/docs/runbook_test.go`
- Read for the facts: `docs/user/lockout-recovery.md`, `docs/user/administration.md`, `docs/user/install.md`, `docs/user/modules/firewall.md`, `docs/user/modules/tailscale.md`, `docs/user/modules/panel.md`, `docs/versioning.md`, `cmd/darkpanel/agent.go`, `cmd/darkpanel/update.go`, `cmd/darkpanel/system.go`, `internal/cli/controller_backup.go`, `internal/cli/controller_quarantine.go`, `internal/cli/firewall.go`, `test/harness/restore_test.go` (`m7rReEnrollCommands`), `internal/controller/backup/journal.go`, `internal/controller/backup/recover.go`

**Interfaces:**
- Consumes: Contracts "Runbook outline". Task 1 (stopping a service on purpose ends its unit cleanly and does not start the rollback unit) and Task 29 (a restore whose last cleanup failed still prints its summary) are behaviour the page describes. From the existing `internal/docs` test files: `dnCommands`, `dnFenceTags`, `dnHTMLRE`, `dnInlineRE` (`modules_b_test.go`) and `dcRead`, `dcRoot`, `dcMentions`, `dcLinks` (`helpers_test.go`); from the repository `cli.NewRoot`, `registry.Build`, `update.ActionApply`, `update.ActionRollback`, `update.ActionStatus` and `rollout.ActionRollout`, `rollout.ActionRolloutCancel`, `rollout.ActionRollouts`.
- Produces: `docs/runbook.md` and the link to it from `docs/user/README.md`. Nothing else consumes it.

**Items:** 399

**Depends on:** Task 1, Task 29, Task 31

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

The runbook is the one page to open when something has already gone wrong: lockout (firewall break-glass, Tailscale preferences revert, panel listener revoke), a failed update (rollback, recover) and a lost controller (restore from backup, re-enroll agents). Row 399 is its regression: the re-enrollment of an agent after a restore uses `darkpanel agent enroll` followed by an agent restart, and never the agent role of `system install`, which keeps saved credentials that the controller still accepts and ignores the new token.

- [ ] **Step 1: Write the failing test**

Create `internal/docs/runbook_test.go`. `cmd/darkpanel` is package main and cannot be imported, so the test looks up generated commands and the hand-written commands of `internal/cli` in a real `cli.NewRoot` tree, and reads the name and flags of `agent enroll`, `update recover` and `system install` from the Go functions that build them (through `go/parser`). The first test pins the checker with a page of good and bad lines, so the other tests cannot pass vacuously; it passes on the current tree.

````go
package docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
)

const w10rbPage = "docs/runbook.md"

// w10rbOutline is every heading of the runbook, in order.
var w10rbOutline = []string{
	"# Runbook",
	"## Lockout",
	"### Firewall break-glass",
	"### Tailscale preferences revert",
	"### Panel listener revoke",
	"## Failed update",
	"### Rollback",
	"### Recover",
	"## Lost controller",
	"### Restore from backup",
	"### Unsettled restore journal",
	"### Re-enroll agents",
}

// w10rbMainCommands are the hand-written commands of cmd/darkpanel. That
// directory is package main and cannot be imported, so each command's name
// and flags are read from the function that builds it.
var w10rbMainCommands = map[string]struct{ file, fn string }{
	"agent enroll":   {"cmd/darkpanel/agent.go", "newAgentEnrollCmd"},
	"update recover": {"cmd/darkpanel/update.go", "newUpdateRecoverCmd"},
	"system install": {"cmd/darkpanel/system.go", "newSystemInstallCmd"},
}

// w10rbWant are the lines each section must carry, verbatim.
var w10rbWant = map[string][]string{
	"Firewall break-glass": {
		"sudo darkpanel firewall rollback --local",
		"sudo darkpanel firewall snapshots get --local",
		"sudo darkpanel firewall snapshot restore --local --id <id> --confirm",
		"sudo darkpanel firewall state repair --local",
	},
	"Tailscale preferences revert": {
		"sudo darkpanel tailscale status get --local",
		"darkpanel change list --state pending",
		"darkpanel change revert <id>",
		"sudo darkpanel tailscale prefs set --local --input <file> --yes --confirm",
		"darkpanel change repair <id>",
	},
	"Panel listener revoke": {
		"sudo darkpanel panel listener revoke",
		"darkpanel panel exposure remove",
	},
	"Rollback": {
		"sudo darkpanel update rollback --local --yes",
	},
	"Recover": {
		"sudo darkpanel update recover",
	},
	"Restore from backup": {
		"sudo darkpanel system install --role controller --agent-advertise <host>:<port>",
		"sudo systemctl stop darkpanel-controller.service darkpanel-agent.service",
		"sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>",
		"sudo darkpanel controller restore --recover",
		"sudo systemctl start darkpanel-controller.service darkpanel-agent.service",
		"sudo darkpanel controller quarantine list",
		"sudo darkpanel controller quarantine resolve <record> done",
		"sudo darkpanel controller quarantine release",
	},
	"Unsettled restore journal": {
		"sudo journalctl -u darkpanel-controller.service",
		"sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>",
	},
	"Re-enroll agents": {
		"sudo darkpanel controller quarantine enroll-token <host>",
		"sudo darkpanel agent enroll --token-file <token-file>",
		"sudo systemctl restart darkpanel-agent.service",
		"sudo darkpanel host enroll-local",
		"darkpanel host list",
	},
}

var (
	w10rbHeadingRE  = regexp.MustCompile(`^(#{1,6}) +(\S.*?)\s*$`)
	w10rbInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]+|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9]+|coming soon|will be added)\b`)
)

// w10rbSections returns the headings of text in order and the lines under
// each heading, ignoring everything inside fenced code blocks for headings.
func w10rbSections(text string) (headings []string, lines map[string][]string) {
	lines = map[string][]string{}
	fenced, current := false, ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if m := w10rbHeadingRE.FindStringSubmatch(line); m != nil && !fenced {
			headings = append(headings, m[1]+" "+m[2])
			current = m[2]
			continue
		}
		if current != "" {
			lines[current] = append(lines[current], line)
		}
	}
	return headings, lines
}

// w10rbRoot is the command tree the runbook's commands are looked up in:
// every generated command and every hand-written command of package cli. The
// hand-written commands of cmd/darkpanel are checked through
// w10rbMainCommands instead.
func w10rbRoot(t *testing.T) *cobra.Command {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	root, err := cli.NewRoot(cli.Options{
		Registry: reg,
		Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}, {Use: "update"}},
		Adopt: map[string][]string{"update": {
			update.ActionApply, update.ActionRollback, update.ActionStatus,
			rollout.ActionRollout, rollout.ActionRolloutCancel, rollout.ActionRollouts,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// w10rbMainFlags returns the name that the Use field of the command built by
// function fn in file declares, and the flags that function adds.
func w10rbMainFlags(t *testing.T, file, fn string) (use string, flags []string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dcRoot(t), filepath.FromSlash(file)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range parsed.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == fn {
			body = f.Body
		}
	}
	if body == nil {
		t.Fatalf("%s declares no function %s", file, fn)
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if k, ok := n.Key.(*ast.Ident); ok && k.Name == "Use" {
				if lit, ok := n.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					use, _, _ = strings.Cut(s, " ")
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "addDevFlag" {
				flags = append(flags, "dev")
			}
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.CallExpr); !ok || !strings.HasSuffix(w10rbCallName(recv), "Flags") {
				return true
			}
			for _, arg := range n.Args[:min(2, len(n.Args))] {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					flags = append(flags, strings.Trim(lit.Value, `"`))
					break
				}
			}
		}
		return true
	})
	return use, flags
}

func w10rbCallName(c *ast.CallExpr) string {
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// w10rbProblems returns one message for each darkpanel command line of the
// sh blocks of md that names no command of the CLI or passes a flag its
// command does not declare. --local and --help are on every command.
func w10rbProblems(t *testing.T, root *cobra.Command, md string) []string {
	t.Helper()
	var problems []string
	for _, c := range dnCommands(md) {
		path := strings.Join(c.words, " ")
		matched := false
		for name, src := range w10rbMainCommands {
			if path != name && !strings.HasPrefix(path, name+" ") {
				continue
			}
			matched = true
			use, flags := w10rbMainFlags(t, src.file, src.fn)
			if want := name[strings.LastIndex(name, " ")+1:]; use != want {
				t.Errorf("%s: %s declares the command %q, want %q", src.file, src.fn, use, want)
			}
			for _, f := range c.flags {
				if f != "local" && f != "help" && !slices.Contains(flags, f) {
					problems = append(problems, c.line+": "+name+" has no flag --"+f)
				}
			}
		}
		if matched {
			continue
		}
		cmd, _, err := root.Find(c.words)
		switch {
		case err != nil || cmd == root:
			problems = append(problems, c.line+": no such command")
			continue
		case cmd.HasSubCommands():
			problems = append(problems, c.line+": names the group "+cmd.CommandPath()+", not a command of it")
			continue
		}
		for _, f := range c.flags {
			if f != "help" && cmd.Flag(f) == nil {
				problems = append(problems, c.line+": "+cmd.CommandPath()+" has no flag --"+f)
			}
		}
	}
	return problems
}

func TestW10RbCheckerFindsWhatItShould(t *testing.T) {
	root := w10rbRoot(t)
	md := strings.Join([]string{
		"```sh",
		"sudo darkpanel agent enroll --token-file <token-file>",
		"sudo darkpanel agent enroll --bogus",
		"sudo darkpanel update recover --boot",
		"sudo darkpanel update rollback --local --yes",
		"sudo darkpanel update rollback --bogus",
		"sudo darkpanel controller quarantine resolve <record> done",
		"darkpanel controller quarantine",
		"sudo darkpanel nothing here",
		"sudo systemctl restart darkpanel-agent.service",
		"```",
	}, "\n")
	want := []string{
		"darkpanel agent enroll --bogus: agent enroll has no flag --bogus",
		"darkpanel update rollback --bogus: darkpanel update rollback has no flag --bogus",
		"darkpanel controller quarantine: names the group darkpanel controller quarantine, not a command of it",
		"darkpanel nothing here: no such command",
	}
	if got := w10rbProblems(t, root, md); !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
}

func TestW10RbRunbookHasItsOutline(t *testing.T) {
	headings, _ := w10rbSections(dcRead(t, w10rbPage))
	if !slices.Equal(headings, w10rbOutline) {
		t.Errorf("%s has the headings\n%s\nwant\n%s", w10rbPage, strings.Join(headings, "\n"), strings.Join(w10rbOutline, "\n"))
	}
}

func TestW10RbCommandsExistAndCarryOnlyTheirFlags(t *testing.T) {
	for _, p := range w10rbProblems(t, w10rbRoot(t), dcRead(t, w10rbPage)) {
		t.Errorf("%s: %s", w10rbPage, p)
	}
}

func TestW10RbSectionsCarryTheirProcedures(t *testing.T) {
	_, lines := w10rbSections(dcRead(t, w10rbPage))
	for section, want := range w10rbWant {
		text := strings.Join(lines[section], "\n")
		for _, line := range want {
			if !strings.Contains(text, "\n"+line+"\n") {
				t.Errorf("section %q has no line %q of its own inside a code block", section, line)
			}
		}
	}
}

func TestW10RbReEnrollUsesAgentEnrollAndARestart(t *testing.T) {
	_, lines := w10rbSections(dcRead(t, w10rbPage))
	text := strings.Join(lines["Re-enroll agents"], "\n")
	enroll := strings.Index(text, "sudo darkpanel agent enroll")
	restart := strings.Index(text, "sudo systemctl restart darkpanel-agent.service")
	if enroll < 0 || restart < enroll {
		t.Errorf("the re-enroll section runs agent enroll at %d and the agent restart at %d, want the restart after the enrollment", enroll, restart)
	}
	if strings.Contains(text, "system install --role agent") {
		t.Error("the re-enroll section names system install --role agent, which keeps credentials the lost controller still accepts and ignores the new token")
	}
}

func TestW10RbUserGuideLinksTheRunbook(t *testing.T) {
	dcMentions(t, "docs/user/README.md", "](../runbook.md)")
	dcLinks(t, w10rbPage)
}

func TestW10RbFollowsTheDocumentationStyle(t *testing.T) {
	text := dcRead(t, w10rbPage)
	open := false
	for i, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "```"); ok {
			switch {
			case open && rest != "":
				t.Errorf("%s:%d: a closing fence carries %q", w10rbPage, i+1, rest)
			case !open && !slices.Contains(dnFenceTags, rest):
				t.Errorf("%s:%d: a code block tagged %q, want one of %v", w10rbPage, i+1, rest, dnFenceTags)
			}
			open = !open
			continue
		}
		if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
			t.Errorf("%s:%d: HTML in %q", w10rbPage, i+1, line)
		}
	}
	if open {
		t.Errorf("%s ends inside a code block", w10rbPage)
	}
	if m := w10rbInternalRE.FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row, or promises future work", w10rbPage, m)
	}
	if !strings.HasPrefix(text, "# Runbook\n") {
		t.Errorf("%s must start with the title", w10rbPage)
	}
}
````

- [ ] **Step 2: Run the test to verify it fails**

```sh
mkdir -p /root/.cache/w10a-tmp/34
export TMPDIR=/root/.cache/w10a-tmp/34 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'W10Rb' ./internal/docs/
```

Expected: `TestW10RbCheckerFindsWhatItShould` passes (it reads no page). Every other test FAILS with `docs/runbook.md: open …/docs/runbook.md: no such file or directory` (the failure of `dcRead`), and `TestW10RbUserGuideLinksTheRunbook` also reports that `docs/user/README.md` does not mention `](../runbook.md)`. Record the output verbatim.

- [ ] **Step 3: Write the page and the links**

`docs/runbook.md` follows the documentation style of `docs/user/lockout-recovery.md`: English, plain Markdown, one `#`, sentence-case headings, second person, present tense, fenced blocks tagged `sh` (or `json`), no HTML, commands exactly as the CLI prints them (`sudo darkpanel …`), no plan, wave, task or register-row name, no promise of future work, every relative link resolving. It has exactly the headings of Contracts "Runbook outline". Placeholders are `<id>`, `<file>`, `<host>`, `<record>` and the like, never real values; the page carries no token, passphrase or key.

The test needs each line listed below as its own line inside an `sh` fence of its section, written exactly so, without a `$` prompt. Put a short sentence before each fence that says what the commands do and what you should see, and a sentence after it for what can go wrong. Check every command against the CLI before you write it (`darkpanel <command> --help`), and every fact against the files named under Files.

The paragraph under `# Runbook` says what the page is: procedures for when something has gone wrong, each run as root on the host itself through the agent socket (`--local`) or the controller socket, so that it works when the web interface, the API or the network does not; each procedure says what it changes and how to check that it worked. It links `user/lockout-recovery.md` for a lost password or authenticator, `user/administration.md` for the backup commands and `user/install.md` for installing a host.

**`## Lockout`.** One paragraph: a lockout is a change that cut your own way in, and every module that can do that has a guard and a revert, so the first step is almost always to wait for the revert. Link `user/lockout-recovery.md` for sign-in problems.

**`### Firewall break-glass`.** Steps:
1. Wait. A pending firewall change reverts on the host after 60 seconds (`--confirm-timeout` on the apply sets 10 to 900); this needs neither the controller nor the network.
2. If you are still out, run as root on the host `sudo darkpanel firewall rollback --local`. It reverts the newest pending firewall change that no background task made; for a change that was already confirmed it restores the snapshot taken before it and confirms the restore. Name `--change <id>` to pick one, `--yes` to skip the question, `--manual` to let the restore add or remove manual rules, and `--override firewall.lockout` only when you are sure the restore is safe for your own connection.
3. To go back to a snapshot you choose: `sudo darkpanel firewall snapshots get --local`, then `sudo darkpanel firewall snapshot restore --local --id <id> --confirm`.
4. If UFW and the kernel disagree and nothing else works: `sudo darkpanel firewall state repair --local`.
5. Say that the agent runs firewall changes one at a time, so a rollback waits for a change that is still being applied, and that the module never shows a half-applied state to a plan or a status read.

The required lines: `sudo darkpanel firewall rollback --local`, `sudo darkpanel firewall snapshots get --local`, `sudo darkpanel firewall snapshot restore --local --id <id> --confirm`, `sudo darkpanel firewall state repair --local`.

**`### Tailscale preferences revert`.** For a `tailscale.prefs.set` change (exit node, shields-up, `wantRunning`, SSH, routes) that cut the node off. There is no separate revert command: the change is revertible and the revert is the procedure. Steps:
1. Wait for the agent to revert it. An unconfirmed preferences change reverts on the host after the confirm timeout (60 seconds by default); the revert needs neither the controller nor the tailnet.
2. To see where the node stands, on the host: `sudo darkpanel tailscale status get --local`.
3. If you can reach the controller, revert it at once: `darkpanel change list --state pending` shows the change id and `darkpanel change revert <id>` reverts it.
4. If you can only reach the host, set the preferences back yourself. Write the fields to restore into a JSON file, in a `json` fence (for example `{"wantRunning": true, "shieldsUp": false}`; the fields are `wantRunning`, `advertiseRoutes`, `exitNode`, `advertiseExitNode`, `acceptRoutes`, `shieldsUp` and `runSSH`), then run `sudo darkpanel tailscale prefs set --local --input <file> --yes --confirm`. If the lockout guard `tailscale.link` refuses, `--override tailscale.link` plans again with it overridden; use it only when you know the change is safe.
5. If the revert itself failed, the change needs repair: `darkpanel change repair <id>`. Its option `outcome` is `restore` (the default, puts back the values from before the change) or `keep` (applies the values the change set), given as `--input '{"outcome":"keep"}'` in prose, not in a fence.

The required lines: `sudo darkpanel tailscale status get --local`, `darkpanel change list --state pending`, `darkpanel change revert <id>`, `sudo darkpanel tailscale prefs set --local --input <file> --yes --confirm`, `darkpanel change repair <id>`.

**`### Panel listener revoke`.** For the panel exposed through Nginx Proxy Manager or a tunnel when it must go dark now and you cannot use the web UI. Steps:
1. As root on the controller's host: `sudo darkpanel panel listener revoke`. It commits the durable revoked state and closes the listener and its open connections at once, through the controller socket. Revocation is permanent: no failure, timeout, restart or revert reopens it.
2. Finish the removal with `darkpanel panel exposure remove`, from the web UI or the CLI as an admin. Its plan has nothing left to revoke; it removes the proxy host or hostname, detaches the proxy, removes the network and deletes the firewall rule, which you confirm like the add's. If a step fails the exposure stays revoked, the panel stays unreachable through it, and `darkpanel change repair <change-id>` repairs the removal.
3. Reopening needs a new `panel exposure add`.

The required lines: `sudo darkpanel panel listener revoke`, `darkpanel panel exposure remove`.

**`## Failed update`.** One paragraph: a new binary that fails its health check rolls back by itself within two minutes (link `versioning.md`), so these procedures are for a host that is still broken afterwards.

**`### Rollback`.** Steps: run `sudo darkpanel update rollback --local --yes` as root on the host; it puts the previous binary back, and once it runs nothing stops it, neither Ctrl+C nor a closed terminal; `--force` rolls back although an enrolled agent runs a newer version than the previous binary; then check with `darkpanel version` and `sudo systemctl status darkpanel-agent.service --no-pager`. The required line: `sudo darkpanel update rollback --local --yes`.

**`### Recover`.** For a rollback or update that a crash or a power loss cut short, so that the services stay stopped until the next boot: run `sudo darkpanel update recover`. If the rollback had started to replace the binary it finishes it and restarts the services; if not it ends it; it is safe to run again. The required line: `sudo darkpanel update recover`.

**`## Lost controller`.** One paragraph: the controller's database or key is gone. With a backup made by `darkpanel controller backup` and its passphrase you restore it, and the agents come back. Without a backup, losing `controller.key` means installing a new controller and enrolling every host again (link `user/install.md`).

**`### Restore from backup`.** Steps:
1. On the replacement host install the controller role with the address the agents will dial: `sudo darkpanel system install --role controller --agent-advertise <host>:<port>` (or `install.sh --role controller`, linked to `user/install.md`).
2. Put the backup file and a passphrase file on the host; the passphrase file is owned by root with mode 0600 (without `--passphrase-file` the command asks for the passphrase).
3. Stop both services: `sudo systemctl stop darkpanel-controller.service darkpanel-agent.service`. Restore refuses while either runs. A stop you ask for ends the unit cleanly and does not start `darkpanel-rollback.service`.
4. Restore: `sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>`. It checks the whole file and the restored database before it replaces anything, keeps the old files as `.pre-<transaction>` names, and prints when the backup was taken. If it prints that the restore finished but could not remove an earlier restore's set-aside files, the restore is complete: remove those `.pre-` files by hand or let the next restore do it.
5. If a restore was cut short, for example by a power loss, run `sudo darkpanel controller restore --recover`; it finishes or rolls back, takes no other flag, and the controller unit runs it before every start.
6. Start the services: `sudo systemctl start darkpanel-controller.service darkpanel-agent.service`.
7. The restored controller starts in quarantine: unfinished changes, cloud operations and rollouts are history and nothing replays them; logins and reads work; changes are refused with `core.quarantined`. List the records with `sudo darkpanel controller quarantine list`; settle each with `sudo darkpanel controller quarantine resolve <record> done` (the work finished), `never` (it never applied) or `repair` (leave it for `darkpanel change repair` after the release); then end the quarantine with `sudo darkpanel controller quarantine release` (`--keep-unresolved` releases anyway and leaves those records, with their locks, quarantined). Link `user/administration.md#settle-the-restore-quarantine`.

The required lines: `sudo darkpanel system install --role controller --agent-advertise <host>:<port>`, `sudo systemctl stop darkpanel-controller.service darkpanel-agent.service`, `sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>`, `sudo darkpanel controller restore --recover`, `sudo systemctl start darkpanel-controller.service darkpanel-agent.service`, `sudo darkpanel controller quarantine list`, `sudo darkpanel controller quarantine resolve <record> done`, `sudo darkpanel controller quarantine release`.

**`### Unsettled restore journal`.** For the one case `controller restore --recover` cannot settle by itself. Every rename of a restore is journaled before it runs; recovery settles a journaled rename by which of its two names exists, and when both or neither exist it stops, keeps the journal and the controller does not start (`internal/controller/backup/journal.go`, `Settle`). Steps:
1. Recognise it: the controller unit fails at start, and `sudo journalctl -u darkpanel-controller.service` shows `backup: a journaled rename cannot be settled: <from> exists <true|false>, <to> exists <true|false>`. The journal is `/var/lib/darkpanel/controller/restore-<transaction>.journal`.
2. Keep both services stopped, and copy everything the restore touched to a root-only directory before you change anything: `/var/lib/darkpanel/controller/` and every `/etc/darkpanel/controller.key*` file (`sudo cp -a`, in an `sh` fence). Nothing below deletes what this copy holds.
3. End the stuck transaction by hand: remove the journal and the staged files of that transaction, the names ending in `.restore-<transaction>` beside `controller.db` and beside `controller.key`. Leave the `.pre-<transaction>` files, which hold the installation as it was before the restore.
4. Restore again from the same backup file: `sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>`. The backup file is the whole target state, so this works whichever names were present. Then continue with steps 6 and 7 of "Restore from backup" (link `#restore-from-backup`).
5. If you have no backup file, put the installation back as it was from the `.pre-<transaction>` files instead: rename each to its name without the suffix, then start the services.

The required lines: `sudo journalctl -u darkpanel-controller.service`, `sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>`. Check each fact of this section against `internal/controller/backup/journal.go`, `recover.go` and `restore.go` before you write it; where the code differs, the code wins, and the report says what differed.

**`### Re-enroll agents`.** Agents that can still reach the controller with valid credentials come back by themselves; re-enroll each one that stays offline in `darkpanel host list`. Steps:
1. On the controller's host, as root, while the quarantine is active: `sudo darkpanel controller quarantine enroll-token <host>`. It prints a one-hour, single-use token once for a host already in the restored database, works only during the quarantine and only when the controller runs with an `--agent-advertise` address, and sets the host to pending.
2. On the agent's host, as root, give the token to the agent: `sudo darkpanel agent enroll --token-file <token-file>` (a file owned by root with mode 0600; without the flag the command prompts, or reads standard input).
3. Restart the agent: `sudo systemctl restart darkpanel-agent.service`, so that it drops the session it holds and dials with the new credentials at once.
4. Check with `darkpanel host list` that the host is online.
5. The controller's own agent cannot be enrolled with a quarantine token. After you release the quarantine, run `sudo darkpanel host enroll-local` on the controller's host, which prints a token for `127.0.0.1:7443`, and repeat steps 2 and 3 there. This is what `docs/user/administration.md` (the `core.host.enroll-local` entry) documents: the command needs `admin`, is served only on the controller socket, revokes the local host's agent credentials and issues a new token for `127.0.0.1:7443`.

State that the installer's agent role is not the way to re-enroll: with saved credentials that the controller still accepts it keeps them and ignores the new token. The test refuses the exact phrase `system install --role agent` anywhere in this section, so say it without that phrase (for example: "do not run the installer again for the agent role"). The required lines: `sudo darkpanel controller quarantine enroll-token <host>`, `sudo darkpanel agent enroll --token-file <token-file>` (before) `sudo systemctl restart darkpanel-agent.service`, `sudo darkpanel host enroll-local`, `darkpanel host list`.

**The three user pages.**
- `docs/user/README.md`: under "When something goes wrong" add the bullet `[Follow the runbook](../runbook.md) lists the procedures for a lockout, a failed update and a lost controller, in the order you need them.` (the link text must say where the link goes).
- `docs/user/lockout-recovery.md`: in "Restore the controller from a backup" add one sentence: the [runbook](../runbook.md) lists every recovery procedure, including how to re-enroll agents after a restore.
- `docs/user/administration.md`: at the end of "Settle the restore quarantine" add one sentence: to bring agents back after a restore, see [Re-enroll agents](../runbook.md#re-enroll-agents).
Do not change any command or flag on those pages: `TestDuLockoutRecovery`, `TestDuAdministration`, `TestDuIndexLinksEveryPage` and `TestDuStyle` keep reading them.

- [ ] **Step 4: Run the test to verify it passes**

```sh
export TMPDIR=/root/.cache/w10a-tmp/34 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'W10Rb' ./internal/docs/
timeout 300 go test -count=1 ./internal/docs/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 600 go test -race -count=1 ./internal/docs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf /root/.cache/w10a-tmp/34 && go clean -testcache
```

Expected: every `go test` line `ok`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Read the finished page once from top to bottom for a command that you did not check with `--help`. Quote the real output.

- [ ] **Step 5: Commit**

```sh
git add docs/runbook.md docs/user/README.md docs/user/lockout-recovery.md docs/user/administration.md internal/docs/runbook_test.go
git commit -m "docs: add the recovery runbook"
```
