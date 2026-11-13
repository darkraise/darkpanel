# M3 audit gaps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the gaps of the M3 milestone audit (`.dforge/milestone-M3-audit-report.md`, owner triage of 2026-11-12) that need code, so that M3 can be audited again.

**Architecture:** Each task is one slice with its own regression test. Task 1 reconciles the spec body with the live-apply rulings and corrects gate 7's budget. Task 2 adds the S2 full-range scan of both hosts against `firewall.exposure` to the harness. Task 3 keeps the panel network off the host's routes and addresses. Tasks 4 and 5 blank the child change ids of hidden composite steps in the steps route and the live step events. Task 6 makes a host lock wait end with its context, and Task 7 limits the DERP probe's tailscale warning to hosts that run tailscale and bounds its host lookup. Tasks 8 and 9 fix two web refetch defects and the flaky revert countdown tests.

**Tech Stack:** Go 1.27.4, TypeScript and React with Vitest 5.0.5 and ESLint (`web/`), the harness under `test/harness` (KVM VMs in GitHub Actions), Markdown spec, guide and gates.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented tasks total 6 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/m3g-tmp/integ && export TMPDIR=/root/.cache/m3g-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/agent/daemon/ && timeout 600 go test -race -timeout 10m -count=1 ./internal/controller/dispatch/ && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Plan review:** 2026-11-13 — dforge:judge-opus — executability 15 / coherence 16 / coverage 17 / assumptions 15 (round 1)

## Global Constraints

- Scope is the rows this plan cites: v1-completion register rows 403, 409, 446, 469, 470, 471, 472 and 473, and M3 firewall register row 5 (`docs/dforge/registers/2026-10-17-m3-firewall.md`), as the rulings under Assumptions narrow them. Only Task 1 edits the spec and `docs/dforge/gates.md`.
- Register citation: Task 2 discharges `2026-10-17-m3-firewall#5`. That register covers the product spec (`docs/dforge/specs/2026-10-17-darkpanel-design.md`), not this plan's spec, so plan-lint does not read it: the row is named in Task 2's text, not on an `**Items:**` line. At the finish, set it with `scripts/register` like the other rows.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. The harness's VM steps run only in GitHub Actions (gate 7); locally only its `Unit` tests run. The panel tests read the host through the fakes `nwHostDeps` supplies, never netlink.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/m3g-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends. Every `pnpm` command runs from the repository root with `-C web` and an explicit `timeout`.
- No task runs `go clean -cache` or `go clean -testcache`: the build cache is shared with other sessions on this host.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`, and no task runs `pnpm install` or `pnpm add`.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1` on the task's packages (every line `ok`); `golangci-lint run --allow-serial-runners` on the task's packages (`0 issues.`, with `--build-tags harness` for `test/harness`). After every web task: the task's Vitest files, `pnpm -C web typecheck`, and `pnpm -C web exec eslint --max-warnings 0` on the changed files.
- Generated files: no task changes a wire type, an action schema or an error code, so `schemas/**` and `docs/api/**` stay unchanged and `make generate` has nothing to regenerate.
- TDD: write the test, run it and see the expected RED, then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A row that pins behaviour already present passes before the change; the step says so.
- Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- Existing tests whose expected messages or outcomes a task changes are updated in the same task, to exactly the new values the task states; no other expectation changes.
- Test names and helpers this plan adds carry the prefix `M3g`/`m3g`, `M3dx`/`m3dx` in the harness, except rows added to an existing table, existing tests a task extends, and helpers and tests that follow the prefix of the file they join (`nwHostDeps` in `network_test.go`, `cdaLate` and `cdaPendingAnswer` in `change-dialog.a11y.test.tsx`, `TestChangeStepsHideBreakGlassChildren` beside the file's `TestChange*` tests).
- The plan runs on branch `m3-audit-gaps` in the base repository, with no worktree.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name or keep the form of their file; a changed refusal keeps its error code.
- Documentation pages (`docs/security/threat-model.md`, `docs/user/**`) never name a plan, wave, task, ruling id or register row: `internal/docs` fails on `W[0-9]+-R[0-9]+`, `wave N`, `task N` and `rows N`. Every Mitigations bullet of the threat model names an existing repository path in backticks. Tasks 2, 3, 4 and 5 edit such pages and run `go test ./internal/docs/...`.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies: Task 2 edits `test/harness/`. Push the branch and run `gh workflow run harness.yml --ref m3-audit-gaps` to completion (`gh run watch`) before the final review; `TestM3Docker/exposure-equals-scan/full-scan-{agent,controller}-{v4,v6}` must pass on all four OS. Then set the note of `2026-10-17-m3-firewall#5` to: `harness TestM3Docker/exposure-equals-scan/full-scan-{agent,controller}-{v4,v6}: a full-range TCP scan (-p-) of both hosts' internet addresses from the scanner finds every open port listed open or partial in that host's firewall.exposure, and every port listed open scanned open, on all four OS (run <id>)`. No task creates or pushes a tag or runs `release.yml`. Order: the gates run on the branch HEAD before the merge; register rows and their notes are set on main by the finish, after the merge, so no register commit moves the HEAD gate 7's run was found for.

## Contracts

**K1. S2 full-range scan (Task 2), `test/harness/m3_docker_exposure_test.go`.**
- `m3dxUnlisted(rows []m3dRow, scanned []PortState, family, inet4, inet6 string) []string`, with three message forms: `tcp/<port> on <addr> is open, and the view lists no row for it`; `tcp/<port> on <addr> is open, and the view's internet verdict is <v>[, <v>...]`; `tcp/<port> on <addr> has internet verdict open, and the full scan did not find it open`.
- `m3dExpoReadHost(ctx context.Context, f *fleet, host string) ([]byte, m3dExpoView, error)`.
- Harness subtest names `TestM3Docker/exposure-equals-scan/full-scan-agent-v4`, `-agent-v6`, `-controller-v4` and `-controller-v6`: the evidence the M3 firewall register row and the M3 audit cite.

**K2. Panel subnet choice (Task 3), `internal/modules/panel/network.go`.**
- `panel.network.create` refusal when no block is free (plain `core.Definite` error, no code): `panel: no free /29 is left in 172.31.255.0/24: every one overlaps a Docker network's subnet or a host route or address (<masked prefixes overlapping the range, sorted, deduplicated, ", "-joined>)`.
- Host read failures (plain `core.Definite`): `panel: read the host's IPv4 routes: <err>` and `panel: read the host's interface addresses: <err>`.
- `networkRouteMinBits = 8`: host routes shorter than /8 are ignored by the choice.

**K3. Hidden composite step children (Tasks 4 and 5).** The steps route (`GET /api/v1/changes/{id}/steps`) and the `composite.step` events give `childChangeId: ""` (and the route `earlierChildChangeIds: []`) for a step whose child `GET /api/v1/changes/{id}` would answer `change.not_found` as hidden. No wire type, schema or OpenAPI change.

**K4. Host lock wait (Task 6), `internal/controller/dispatch/hosts.go`.** `lockHost` answers `fmt.Errorf("dispatch: wait for host %s: %w", hostID, ctx.Err())` when ctx ends while it waits. Callers test it with `errors.Is(err, context.DeadlineExceeded)` or `context.Canceled`. It is uncoded, so `errcode.From` reads it as Internal and the DERP status pass's `serversReadFailure` stores `the agent did not answer` for it.

**K5. DERP probe warnings (Task 7), `internal/modules/derp/server_probe.go`.** `derp.server.probe` no longer warns about a host whose tailscale module `core_module_state` marks disabled or unavailable, nor about a host whose agent refuses `tailscale.status` as `not_found` or `module.unavailable`. The warning text is unchanged. `probeSelf` looks up the host's agent inside the 30-second read bound.

## Assumptions (evidence)

Rulings (owner, 2026-11-12 M3 audit triage and 2026-11-13):

- **Row 446.** The spec body states `ufw reload` where W11-R2 and W11-R3 narrowed it; Task 1 reconciles the 2.3 prediction list, the `firewall.docker.enable` block bullet, the `firewall.docker.repair` bullet and W11-R2's `TransLive (Contracts)` pointer, which means the Contracts of plan `v1-w11a-live-apply`.
- **Row 403.** The panel range `172.31.255.0/24` is not made configurable for v1; when no block is free, the refusal names what occupies the range. Routes shorter than /8 are ignored, so OpenVPN's `def1` (two /1s) and similar default-route stand-ins do not block every block; every route type and table counts (a Tailscale subnet route lives in table 52, a blackhole route holds the range too); interface addresses count whether the interface is up or down. A VPN or LAN route covering the whole range (for example `172.16.0.0/12`) therefore makes the add refuse with no override. The range is avoided, not reserved, so the guide gets no reserved-range note.
- **Row 409.** Hidden children are rendered without a link, as the owner ruled; `NetworkVisible` is not widened to show a break-glass change that has a visible parent, which would contradict the break-glass hiding rule. Both halves stay risk 3: they change what the network's authorization filter shows of break-glass changes. The triage's sentence "resolved over the controller socket" is wrong (the socket serves no change route); the guide sentence points at releasing the visible removal instead.
- **Row 470.** The lock wait returns the context error wrapped and uncoded, following `planLocks.lock`; a coded `core.deadline_exceeded` was not chosen. The same defect is a clause of `2026-10-19-m2-fleet-lifecycle-core#92`, which is `deferred`, not open, so Task 6 does not cite it; the finish may note in that row that the clause is fixed. Risk 3 for a concurrency change on the path every agent call takes; no lens line, because `criteria/lenses/` has no concurrency lens.
- **Row 471.** A host with the tailscale module enabled but tailscaled not installed keeps the warning: the status read answers `tailscale.unavailable` both for "not installed" and "stopped", and silencing it would change the tailscale module's error contract. Folded in by the planner (not an owner ruling; the same defect class as row 470, a lookup outside its bound): `probeSelf` called `env.Agents.Agent` outside the probe's 30-second read bound, so a hung Hello under another caller held the probe's tailscale reads until the request's own context ended; Task 7 moves the lookup inside the bound, with one test.
- **Rows 472, 473.** The coordinator ruled (2026-11-13) to keep the `role="alert"` pattern of `0f7af5c` with the error's own message, for consistency; the alert does not hide the data below it.
- **Row 469.** The deadline is computed when the stub answers, uniformly in all four countdown tests; `vi.useFakeTimers({ toFake: ["Date"] })` would fix only the singular test, since the other three drive the component's `setInterval`.
- **M3 firewall row 5.** The full scan checks both directions: an open port the view does not list open or partial fails, and a port listed open that did not scan open fails, so a scan that reached nothing (nmap `-Pn` reports every port filtered, which `ParsePorts` accepts) cannot pass vacuously. An open port whose row says `unknown` fails; if the controller's 22/tcp turns out `unknown`, that is a product finding against R48/S2, not a test to relax. No full-range UDP scan: it cannot tell a silent open port from a filtered one and would cost about the same time while proving nothing; Linux rate-limits ICMP port-unreachable, which could stretch it to hours. The residual gap, an unlisted silent UDP listener, is recorded in the threat model by plan `v1-w12a-security-residuals`. The scan runs at the end of `m3dExpoScan`, not as a step of its own, so the published containers m3d-x1 to m3d-x7 are still up; `TestM3Firewall` was not chosen because its steps change the agent's UFW rules mid-run.

Task 1, spec and gates:

- Item 7 (gate 7's budget) has no register row: the sentence was found stale while planning (it says 120 minutes and `-timeout 100m`; the workflow says 280 and three runs). It is carried here because Task 1 is the plan's only edit of `docs/dforge/gates.md`, and it is a separate item of that task's one commit.

- W11-R2's pointer and the live prediction: `docs/dforge/plans/2026-11-08-v1-w11a-live-apply.md` line 7 and its `### TransLive` contract; `internal/modules/firewall/predict/predict.go:50-66`; the transition choice in `internal/modules/firewall/target.go:115-140`.
- `.github/workflows/harness.yml` (read 2026-11-13): the `vm` job has `timeout-minutes: 280` (line 110) and three steps, `-timeout 100m` (line 151), `-timeout 70m` for `TestM3Firewall` (line 164) and `-timeout 70m` for `TestM3Docker` (line 177); the `vm-m4` job has `timeout-minutes: 100` and `-timeout 70m` for `TestM4` (lines 201, 245). `docs/dforge/gates.md` gate 7 still says 120 and 100m.

Task 2, S2 scan:

- `ScanOpts` with empty `Ports` scans `-p-`: `test/harness/nmap.go:56`, `:87-88`.
- The controller host's id is `f.localID`, its internet addresses `targetIPv4` 198.18.0.10 and `targetIPv6` 2001:db8:d0::10: `test/harness/m2_test.go:92,155-160,256`, `m1_test.go:35-36`. `m3Ready` waits for a healthy firewall on `f.localID`. That `firewall.exposure` answers for the controller host in a harness run is unverified — Task 2 verifies it through gate 7.
- The view lists a socket bound to `[::]` also as an IPv4 wildcard row (`internal/modules/firewall/exposure_resource.go:183-186`), so a dual-stack listener gives no false "no row" message under the IPv4 scan.
- A full-range scan takes 65.8 to 65.9 s per address and family: harness run 43246348344 (2026-11-12, all four vm jobs green), `TestM1/scan-ipv{4,6}` and `TestM2/self-management/scan-*` on all four OS. In the same run `TestM3Docker` took 207 s to 301 s and whole vm jobs 23m19s to 27m50s. The four scans add about 264 s per OS, far under `m3dDockDeadline` (60 minutes, `test/harness/m3_docker_test.go:13`), the step's 70m and the job's 280 minutes; `TestM3xExpoWorkflowFile` keeps passing because no timeout changes.
- No agent row had an unknown internet verdict on any OS in that run. The controller's 22/tcp verdict is unverified — Task 2 verifies it through gate 7. Both VMs run the same `TargetScript`, which allows 22/tcp (`test/harness/provision.go:66`).
- Before Docker only 22/tcp is open on both hosts from the scanner (`checkScan` wants exactly `[22/tcp]`, `m1_test.go:363-376`, `m2_test.go:398-402`), and in `m3dExpoScan` every plain row (22, 18301, 18303, 18306, 18307) is already compared exactly by `m3dExpoCheck`, so the expected open set under the full scan is 22, 18301, 18302 and 18307 over both families, all listed open or partial: an inference from the existing checks.
- RED and GREEN were shown by the drafter on 2026-11-13 through `go test -overlay`; mutations of the scratch implementation (returning nil, dropping the second pass, counting `unknown` as listed) each failed the test. `golangci-lint` was not run (no overlay mode); Task 2's Step 4 runs it.

Task 3, panel network:

- The panel module's only host abstraction is `agentDeps{docker}`: `internal/modules/panel/deps.go:25-37`. `netstat.Routes(family)` dumps every table of one family and `netstat.Interfaces()` returns IPv4 and IPv6 prefixes with host bits set: `internal/platform/netstat/routes.go:165-245`; other modules wire them the same way (`internal/modules/firewall/wire.go:54`, `internal/modules/tailscale/deps.go:94`). `networkChoose` is called only by `networkCreateHandler.Plan` and `.Apply` (`rg -n networkChoose internal/`, 2026-11-13).

Tasks 4 and 5, hidden step children:

- Controller-side steps store host `""`, as controller-side changes do: `internal/core/lifecycle/composite_run.go:1169-1171`, `release_test.go:290`. Break-glass actions are controller-side only: `internal/core/registry.go:504`.
- The controller socket serves no change route: `internal/controller/api/socket.go:203-215`, `internal/controller/backup/contribute.go:56-76`. `darkpanel change repair` and `change release` use the network API (`internal/cli/change.go:295-303,389-402`), whose repair and release routes answer a hidden change `change.not_found` (`internal/controller/api/changes.go:75-86`, `TestChangeOperationsHideChanges`).
- A composite's repair refuses while its cursor child is in needs_repair, and releasing the composite releases its children in needs_repair as `system:recovery`: `internal/core/lifecycle/composite_ops.go:165-176,248-256,326-420`. Existing tests pin both halves of the guide's recovery sentence: `TestCompositeReleaseWithAnUnreachableAgent` and `TestCompositeReleaseWaitsForAChildRepair` (`internal/core/lifecycle/composite_ops_test.go:478-534`) release a composite stuck on a child in needs_repair and see the child abandoned, and `TestReleaseOfAStepByItsComposite` (`release_test.go:180-190`) shows a controller-side engine releasing a step's change for its composite as `system:recovery`. That the panel removal's own revoke child goes through the same `releaseStep` path is an inference from the code (`releaseStep` releases every needs_repair step child through `kids.Release` whatever its action), not a panel test.
- A removal of an exposure whose earlier removal did not complete plans the same removal again: `internal/modules/panel/expose_remove_test.go:186-216`.
- The web UI renders an empty `childChangeId` as `—` without a link (`web/src/pages/change.tsx:146`) and uses `composite.step` events only to refetch (`web/src/api/events.ts:61`), so no web change and no Vitest test: a new assertion would pass before the fix. `Dispatcher.Steps` builds a new slice on every call (`internal/core/lifecycle/release.go:243`).
- Task 5's branch for a child unreadable for another reason (warn and blank) has no test: it needs a database read failure after the parent read succeeded, which the server test harness cannot inject.

Tasks 6 and 7, dispatch and DERP:

- The per-host lock is a `sync.Mutex` taken without regard to ctx: `internal/controller/dispatch/hosts.go:144-151`. A one-slot channel lock with a ctx-aware wait exists in the package: `planLocks.lock`, `apply.go:214-250`. `h.locks` is used only by `NewHosts` and `lockHost`.
- The DERP status pass bounds each whole `readStatus`, including `env.Agents.Agent`, with a 15 s timer: `internal/modules/derp/servers.go:262-266`, `server_status.go:184`. `readStatus` in `probeServer` (`server_probe.go:115`) stays unbounded; not in this plan's scope.
- The agent runtime does not refuse a resource of a disabled module: `Runtime.Resource` refuses only an unknown resource (`not_found`) and a module whose migrations failed (`module.unavailable`), `internal/agent/runtime/client.go:57-83`; the enabled flag lives on the controller in `core_module_state` (`internal/agent/runtime/enablement.go:13-16`, schema `internal/core/store/migrations/controller/0001_core.sql:18-26`). The remote agent client turns a coded answer back into `*errcode.Error`: `internal/agent/api/client.go:176-183`.
- The tailscale module is enabled by default and available on supported OS and architecture even before tailscaled is installed (`internal/modules/tailscale/tailscale.go:28`, `preflight.go:18-20`); a missing or stopped tailscaled reads as `tailscale.unavailable` (`status.go:70-75`). The DERP test fake answers a missing resource with an uncoded error (`internal/modules/derp/testhost_test.go:785-791`), so `TestW10SpProbeWarnsAboutAHostWhoseTailscaleStatusCannotBeRead` keeps passing.
- RED and GREEN of Tasks 6 and 7's drafted tests were shown on 2026-11-13 through `-overlay` (Task 6 RED after 5.50 s; `go test -race ./internal/controller/dispatch/` ok in 133.8 s; `./internal/modules/derp/` ok in 294.8 s at load near 7). Task 7's bound test was checked on 2026-11-13 at `114a40c` the same way: RED `the probe of a host whose lookup hangs did not end` after 10.3 s; GREEN with the lookup moved inside the bound, 3 of 3 runs together with `TestW10Sp*`.
- Task 6's test waits 100 ms for the waiter to block before the release; under heavy load the waiter can start late, and the test then passes without exercising the shared Hello, but it cannot fail spuriously.

Tasks 8 and 9, web:

- The pattern to copy is commit `0f7af5c` (`fix(web): keep NPM data when a refetch fails`). `ConnectorsCard` checks `connectors.isError` before held data (`web/src/modules/cloudflare/connectors-page.tsx:101-109`), `DeployCard` already prefers `connectors.data` (`:176-182`), and `ConnectorsPage` returns the error alert on `hosts.isError` even with hosts held (`:240-241`), which unmounts an open `CloudflareChange` dialog. `AdminExposureBanner` returns the generic warning on `fleet.isError` before reading retained data (`web/src/modules/npm/banner.tsx:29-32`).
- `renderWithProviders` builds a `QueryClient` with `retry: false` and `StubHandler` accepts functions (`web/src/test/utils.tsx:23,62`). A Radix dialog sets `aria-hidden` on the page behind it, so the hosts test finds the alert by text.
- Under `shouldAdvanceTime`, vitest 5.0.5's fake timers advance the clock on a real `setInterval`, so real time moves the fake clock; the announced value is the countdown's first value (`web/src/components/change-dialog.tsx:73-91,202-207`). A real one-second delay reproduces the flake, but races `findByRole`'s one-second default timeout, so the tests jump the clock instead. What remains after Task 9 is the time between the stub's answer and the progress view's first render, which would have to exceed a full second of real time.
- Both web tasks' Step 2 failures and Step 4 passes were reproduced on a scratch copy of `web/src` on 2026-11-13 (Task 8: 3 failed, 22 passed; then 25 passed in 3 of 3 runs; Task 9: 4 failed, 13 passed; then 17 passed in 3 of 3 runs); `tsc --noEmit` and `eslint --max-warnings 0` on the changed files exited 0. `make test-web` runs `vitest run` (`Makefile:89-90`). Not covered: no banner test names an unreadable id that a loaded host list does not contain (the `?? id` branch).

Process:

- Plan review round 1 (2026-11-13, dforge:judge-opus) found 0 Critical and 2 Important (Task 1's line count, Task 4's recovery evidence), both fixed; its Minor findings were folded in. No round 2: no Critical finding and no score of 8 or below.

## Task index

1. Reconcile the spec with the live-apply rulings and correct gate 7's budget
2. Full-range scan of both hosts against firewall.exposure
3. Keep the panel network off the host's routes and addresses
4. Leave hidden step children out of the steps route
5. Leave hidden step children out of the live step events
6. End a host lock wait when its context ends
7. Warn only for hosts that run tailscale
8. Keep held connector, host and exposure data when a refetch fails
9. Time the revert countdown tests from the stub's answer

---

### Task 1: Reconcile the spec with the live-apply rulings and correct gate 7's budget

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (2.3 Prediction, 2.6 Block and Health bullets, W8-R9, section 8's break-glass paragraph, W11-R2)
- Modify: `docs/dforge/gates.md` (gate 7's `Why:` budget sentence)

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 2026-10-21-v1-completion#446
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

- [ ] **Step 1: No new test, because the edits are spec and gate prose that no script reads; `internal/docs` reads the spec only for the W11 ruling names (`TestW11b11GuardDocumentation`), which stay**

- [ ] **Step 2: Run the existing checks before the change**

Run: `timeout 300 go test -count=1 ./internal/docs/...`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`.

- [ ] **Step 3: Implement**

The spec's prose wraps near 76 columns: match each quoted passage across its line breaks and rewrap the edited paragraph to the file's width. Change nothing else.

1. **2.3, Prediction** (`#### 2.3 Prediction, evaluation and the lockout guard`): replace `for the transition the action causes (rule change with reload, enable, disable, reload after a file change):` with `for the transition the action causes (a live change on an active UFW that stays active, W11-R2; enable; disable; a reload where W11-R3 keeps one):`. Then insert this sub-bullet directly before the sub-bullet that begins `  - for disable:`:

   ```markdown
     - for a live change (`fwstate.TransLive`): in IPv4, and in IPv6 when
       IPv6 is enabled and ip6tables is present, the three user logging
       chains are flushed and every chain the family's user rules file
       declares is loaded as `iptables-restore -n` loads it, and the logging
       chains are rebuilt after a `ufw logging` command; the built-in
       policies, every other chain and the sysctl values are unchanged. The
       Docker block's chains are loaded by Darkpanel's own `-n` restore (2.6);
   ```

2. **2.6, Block (R44)**: replace `` `firewall.docker.enable` (revertible) writes marked block `docker` into `/etc/ufw/after.rules` and `after6.rules` and reloads UFW. `` with `` `firewall.docker.enable` (revertible) writes marked block `docker` into `/etc/ufw/after.rules` and `after6.rules`. On an active UFW it runs no `ufw reload`: it loads the block's chains with one `iptables-restore -n` per family that declares `DOCKER-USER` and Darkpanel's own chains, never `ufw-user-forward` (W11-R2); UFW loads the block from the files when it next starts. ``
3. **2.6, health**: replace `` the UI offers `firewall.docker.repair` (revertible), which rewrites and reloads. `` with `` the UI offers `firewall.docker.repair` (revertible), which rewrites the block and loads Darkpanel's chains live as `firewall.docker.enable` does, and restarts UFW with `ufw reload` only when its differences reach a chain outside Darkpanel's own (W11-R3). ``
4. **W11-R2** (the line that begins `  - **W11-R2 live apply.**`): replace `` The prediction is `TransLive` (Contracts). `` with `` The prediction is `TransLive`, defined in the Contracts of plan `docs/dforge/plans/2026-11-08-v1-w11a-live-apply.md` and in 2.3's prediction list. ``
5. **W8-R9** (`  - **W8-R9 network.**`): replace its first sentence, from `` `panel.network.create` picks the highest /29 `` up to and including `` named `darkpanel-panel` exists. ``, with: `` `panel.network.create` picks the highest /29 in `172.31.255.0/24` that overlaps no IPv4 subnet of any Docker network on the host, no IPv4 route of the host of /8 or longer (in any table) and no IPv4 address of the host's interfaces, and refuses with a plain definite error naming what takes the range when none is free; the range is fixed for v1. It creates `darkpanel-panel` with `EnableIPv6` false and exactly that one IPAM config, and refuses `panel.already_exposed` when a network named `darkpanel-panel` exists. `` The sentence about `panel.network.remove` that follows stays.
6. **Section 8**, the break-glass paragraph: after the sentence `` An interrupted break-glass revoke is completed by running the action again (it has `Converges`). `` insert: `The network change routes and event stream show a composite step whose child they hide (a break-glass action, or a controller-side action no longer registered) with an empty child change id and no earlier child change ids, so the web UI renders it without a link; a removal stopped at a revoke child in needs_repair is given up with a release of the removal, which releases the child, and is then run again.`
7. **`docs/dforge/gates.md`, gate 7**, the `Why:` line: replace from `Budget: the vm job's` to the end of the line with: ``Budget: the vm job's `timeout-minutes` is 280, covering three go test runs: the M1 and M2 run with `-timeout 100m` (TestM1's deadline 40 minutes, TestM2's 55), `TestM3Firewall` with `-timeout 70m`, and `TestM3Docker` with `-timeout 70m` (its deadline 60 minutes); the vm-m4 job runs `TestM4` with `-timeout 70m` under `timeout-minutes: 100`. Measured on run 39470066212 of 2026-10-20: TestM1 183s to 220s and TestM2 543s to 588s; on run 43246348344 of 2026-11-12 (all four vm jobs green): a vm job took 23m19s to 27m50s and TestM3Docker 207s to 301s.`` First confirm the numbers with `rg -n 'timeout-minutes|-timeout' .github/workflows/harness.yml`; if they differ, write what the file says and note it in the ledger.

- [ ] **Step 4: Run the existing checks after the change**

Run: `timeout 300 go test -count=1 ./internal/docs/... && rg -c 'TransLive' docs/dforge/specs/2026-10-21-v1-completion-design.md && ! rg -n 'reloads UFW\.|rewrites and reloads|TransLive` \(Contracts\)' docs/dforge/specs/2026-10-21-v1-completion-design.md`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`; the `TransLive` count is 2 (the new 2.3 sub-bullet and the W11-R2 line; `rg -c` counts lines); the negated `rg` prints nothing, so the whole command exits 0.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md docs/dforge/gates.md
git commit -m "docs(spec): reconcile live apply and gate budget"
```

### Task 2: Full-range scan of both hosts against firewall.exposure

**Files:**
- Modify: `test/harness/m3_docker_exposure_test.go` (`m3dExpoRead`, new `m3dExpoReadHost`, new `m3dxUnlisted`, new `m3dExpoFullScan`, `m3dExpoScan`)
- Modify: `docs/security/threat-model.md` (Firewall, Mitigations: the exposure view bullet)
- Test: `test/harness/m3_docker_exposure_unit_test.go`

**Interfaces:**
- Consumes: none
- Produces: K1

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

This task discharges M3 firewall register row `2026-10-17-m3-firewall#5` (the S2 exit criterion; see Global Constraints for why it has no `**Items:**` line). Risk 2: the change is test-only and additive, but it adds a gate on a harness path that all four OS runs depend on, and the `m3dExpoRead` refactor touches four callers.

- [ ] **Step 1: Write the failing test**

In `test/harness/m3_docker_exposure_unit_test.go`, add `"slices"` to the import block (between `"os"` and `"strings"`), then append:

```go
func TestM3dxUnitUnlisted(t *testing.T) {
	const inet4, inet6 = "198.18.0.11", "2001:db8:d0::11"
	rows := []m3dRow{
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 22, Verdict: "open"},
		{Proto: "tcp", Addr: "::", Family: "v6", Port: 22, Verdict: "open"},
		{Proto: "tcp", Addr: "127.0.0.1", Family: "v4", Port: 8080, Verdict: "closed"},
		{Proto: "tcp", Addr: "::", Family: "v6", Port: 8443, Verdict: "open"},
		{Proto: "tcp", Addr: "198.18.0.11", Family: "v4", Port: 9100, Verdict: "open"},
		{Proto: "tcp", Addr: "10.77.0.11", Family: "v4", Port: 7443, Verdict: "open"},
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 18302, Verdict: "partial", Allowed: []string{"198.18.0.20/32"}},
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 18303, Verdict: "filtered"},
		{Proto: "udp", Addr: "0.0.0.0", Family: "v4", Port: 18304, Verdict: "open"},
		{Proto: "tcp", Addr: "0.0.0.0", Family: "v4", Port: 18310, Verdict: "unknown"},
	}
	open := func(port uint16) PortState { return PortState{Port: port, Proto: "tcp", State: "open"} }
	scan4 := func(extra ...PortState) []PortState {
		return append([]PortState{open(22), open(9100), open(18302)}, extra...)
	}
	twoRows := append(slices.Clone(rows), m3dRow{Proto: "tcp", Addr: "198.18.0.11", Family: "v4", Port: 18303, Verdict: "partial"})
	shut := append(slices.Clone(rows), m3dRow{Proto: "tcp", Addr: "198.18.0.11", Family: "v4", Port: 18303, Verdict: "closed"})
	cases := []struct {
		name    string
		rows    []m3dRow
		scanned []PortState
		family  string
		want    []string
	}{
		{"every open port listed, IPv4", rows, scan4(), "v4", nil},
		{"every open port listed, IPv6", rows, []PortState{open(22), open(8443)}, "v6", nil},
		{"UDP entries are not compared", rows, scan4(PortState{Port: 18399, Proto: "udp", State: "open"}), "v4", nil},
		{"an open port without a row", rows, scan4(open(18399)), "v4",
			[]string{"tcp/18399 on 198.18.0.11 is open, and the view lists no row for it"}},
		{"a row bound to another address", rows, scan4(open(7443)), "v4",
			[]string{"tcp/7443 on 198.18.0.11 is open, and the view lists no row for it"}},
		{"a row bound to loopback", rows, scan4(open(8080)), "v4",
			[]string{"tcp/8080 on 198.18.0.11 is open, and the view lists no row for it"}},
		{"a row of the other family", rows, scan4(open(8443)), "v4",
			[]string{"tcp/8443 on 198.18.0.11 is open, and the view lists no row for it"}},
		{"an IPv4-only row under the IPv6 scan", rows, []PortState{open(22), open(8443), open(18302)}, "v6",
			[]string{"tcp/18302 on 2001:db8:d0::11 is open, and the view lists no row for it"}},
		{"a filtered verdict scanned open", rows, scan4(open(18303)), "v4",
			[]string{"tcp/18303 on 198.18.0.11 is open, and the view's internet verdict is filtered"}},
		{"an unknown verdict scanned open", rows, scan4(open(18310)), "v4",
			[]string{"tcp/18310 on 198.18.0.11 is open, and the view's internet verdict is unknown"}},
		{"one of two rows partial", twoRows, scan4(open(18303)), "v4", nil},
		{"neither of two rows open", shut, scan4(open(18303)), "v4",
			[]string{"tcp/18303 on 198.18.0.11 is open, and the view's internet verdict is filtered, closed"}},
		{"an open row the scan found closed", rows, []PortState{{Port: 22, Proto: "tcp", State: "closed"}, open(9100)}, "v4",
			[]string{"tcp/22 on 198.18.0.11 has internet verdict open, and the full scan did not find it open"}},
		{"a scan that found nothing open", rows, []PortState{}, "v4", []string{
			"tcp/22 on 198.18.0.11 has internet verdict open, and the full scan did not find it open",
			"tcp/9100 on 198.18.0.11 has internet verdict open, and the full scan did not find it open",
		}},
		{"both directions at once", rows, []PortState{open(9100), open(18399)}, "v4", []string{
			"tcp/18399 on 198.18.0.11 is open, and the view lists no row for it",
			"tcp/22 on 198.18.0.11 has internet verdict open, and the full scan did not find it open",
		}},
	}
	for _, c := range cases {
		got := m3dxUnlisted(c.rows, c.scanned, c.family, inet4, inet6)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: m3dxUnlisted = %q, want %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -tags harness -count=1 -run 'TestM3dxUnitUnlisted' ./test/harness/`
Expected: FAIL (build failed) with `test/harness/m3_docker_exposure_unit_test.go:<line>:10: undefined: m3dxUnlisted`

- [ ] **Step 3: Implement**

Items 1 to 5 are in `test/harness/m3_docker_exposure_test.go`; no new import is needed (`fmt`, `strings`, `testing`, `context` are already imported).

1. `m3dExpoReadHost(ctx context.Context, f *fleet, host string) ([]byte, m3dExpoView, error)`: the current body of `m3dExpoRead` with `f.agentID` replaced by `host` in the `f.api.Resource(ctx, ..., "firewall.exposure", &raw)` call. `m3dExpoRead(ctx, f)` keeps its signature and doc comment and becomes `return m3dExpoReadHost(ctx, f, f.agentID)`, so its four callers do not change. Doc comment of the new function: it reads firewall.exposure of `host` (a host id) and returns its JSON and the decoded view.

2. `m3dxUnlisted(rows []m3dRow, scanned []PortState, family, inet4, inet6 string) []string`, placed after `m3dxExpoTarget`. It compares a full-range TCP scan of one host address with that host's view and returns one message per disagreement (nil when none):
   - The scanned address `target` is `inet4` when `family` is `"v4"` and `inet6` otherwise.
   - The rows it considers are those with `Proto == "tcp"`, `Family == family`, and `m3dxExpoTarget(r.Addr, r.Family, inet4, inet6)` returning ok (a wildcard row of that family or a row bound to `target` itself). Every other row is ignored: UDP rows, the other family, loopback, the private link, Docker bridges.
   - Scan entries other than `Proto == "tcp"` with `State == "open"` are ignored (UDP entries, closed, filtered).
   - First pass, over the open TCP scan entries in scan order. Collect the verdicts of the considered rows with that port, in row order. No such row: append `fmt.Sprintf("tcp/%d on %s is open, and the view lists no row for it", port, target)`. Rows exist but none has verdict `open` or `partial`: append `fmt.Sprintf("tcp/%d on %s is open, and the view's internet verdict is %s", port, target, strings.Join(verdicts, ", "))`. `unknown`, `filtered` and `closed` all count as not listed open.
   - Second pass, over the considered rows in row order: a row with verdict `open` whose port is not among the open TCP scan entries appends `fmt.Sprintf("tcp/%d on %s has internet verdict open, and the full scan did not find it open", r.Port, target)`. This direction keeps a scan that reached nothing (nmap with `-Pn` reports every port filtered) from passing: every host's view lists 22/tcp as open.
   - Doc comment: state both directions, that a partial row counts as listed because the default scanner source may be inside or outside its allowed set, and that UDP is out of scope because a UDP scan cannot tell an open silent port from a filtered one.

3. `m3dExpoFullScan(ctx context.Context, t *testing.T, f *fleet)`, placed after `m3dExpoScan`:
   - Hosts in this order: `{"agent", f.agentID, agentIPv4, agentIPv6}` and `{"controller", f.localID, targetIPv4, targetIPv6}`. Families in this order: `{"v4", "-4", <host inet4>}` and `{"v6", "-6", <host inet6>}`.
   - For each host and family, one subtest `t.Run("full-scan-"+<host name>+"-"+<family>, ...)` that:
     a. runs `scanned := m3dScan(ctx, t, f.scanner, ScanOpts{Family: <flag>, Target: <address>})`. `Ports` stays empty, so `NmapArgs` emits `-p-`, the full range 1-65535 with the M1 scan's timing. No `Source`: the scan leaves from `scannerIPv4` / `scannerIPv6`.
     b. after the scan, reads `raw, view, err := m3dExpoReadHost(ctx, f, <host id>)`; on `err != nil`, `t.Fatal(err)`.
     c. when `len(view.Notes) > 0`, logs `t.Logf("%s exposure notes: %q", <host name>, view.Notes)` (notes are not this check's subject; `m3dExpoCheck` already fails the agent's notes).
     d. `rows, err := m3dxRows(raw)`; on error `t.Fatal(err)`.
     e. for each message of `m3dxUnlisted(rows, scanned, <family>, <host inet4>, <host inet6>)`: `t.Errorf("%s: %s", <host name>, msg)`.
   - Doc comment: spec S2's full-range check; name the hosts, both families and that it runs while `m3dExpoScan`'s containers are published.

4. In `m3dExpoScan`, add `m3dExpoFullScan(ctx, t, f)` as the last statement, after the line `m3dExpoUDPAnswers(ctx, t, f, 18305, false)`. Running inside this step keeps the containers m3d-x1 to m3d-x7 published (open, partial, filtered and proxied-IPv6 rows), which Docker exposes outside UFW's INPUT chain and are where an under-reporting view is most likely; their `t.Cleanup` removals run after the full scan.

5. What must not change: `m3dExpoCheck`, `m3dxMismatch`, `NmapArgs`, `ScanOpts`, `m3dDockDeadline` (60 minutes), the step order of `TestM3Docker`, and `.github/workflows/harness.yml`. The four scans add about 264 s (65.8 to 65.9 s per full-range scan), far under the 60-minute deadline, the step's `-timeout 70m` and the vm job's `timeout-minutes: 280`. `TestM3xExpoWorkflowFile` keeps passing because no timeout changes.

6. `docs/security/threat-model.md`, **Firewall, Mitigations**, the bullet that begins `- The exposure view computes reachability per port`: replace its first sentence `The exposure view computes reachability per port and the harness checks it against an external scanner.` with `The exposure view computes reachability per port, and the harness checks it against an external scanner: a full-range TCP scan of the controller's and an agent's internet addresses, IPv4 and IPv6, must find no open port that the host's view does not list as open or partial, and every UDP port the view lists is probed.` The bullet's second sentence and its paths stay.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -tags harness -count=1 -run 'Unit|TestM3x|TestM3dx' ./test/harness/... && GOTOOLCHAIN=local timeout 600 go vet -tags harness ./test/... && timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/harness/... && timeout 300 go test -count=1 ./internal/docs/...`
Expected: PASS, every package `ok`, vet silent, lint `0 issues.`. The VM run is gate 7 (harness.yml on the pushed branch): `TestM3Docker/exposure-equals-scan/full-scan-{agent,controller}-{v4,v6}` PASS on ubuntu-22.04, ubuntu-24.04, debian-12 and debian-13, each subtest about 66 s.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m3_docker_exposure_test.go test/harness/m3_docker_exposure_unit_test.go docs/security/threat-model.md
git commit -m "test(harness): full-range scan against exposure"
```

### Task 3: Keep the panel network off the host's routes and addresses

**Files:**
- Modify: `internal/modules/panel/deps.go` (`agentDeps`, `productionAgent`)
- Modify: `internal/modules/panel/network.go` (`networkChoose`, new `networkHostTaken`, new constant `networkRouteMinBits`, `networkCreateHandler.Plan`, `networkCreateHandler.Apply`)
- Modify: `docs/user/modules/panel.md` (step 2 of "What the add does")
- Test: `internal/modules/panel/network_test.go`
- Test: `internal/modules/panel/deps_test.go`

**Interfaces:**
- Consumes: none
- Produces: K2

**Items:** 2026-10-21-v1-completion#403
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

Ruling: the panel range is not made configurable for v1; when no block is free, the refusal names what occupies the range.

- [ ] **Step 1: Extend the test `internal/modules/panel/network_test.go::TestNwCreatePlanPicksSubnet`**

This step also extends `internal/modules/panel/network_test.go::TestNwCreatePlanRefusals` and `internal/modules/panel/deps_test.go::TestFdProductionAgentDialsNoDaemon`.

In `internal/modules/panel/network_test.go`:

1. Add `"github.com/darkraise/darkpanel/internal/platform/netstat"` to the import block, after the `dockerapi` import.

2. Replace the whole `nwDeps` function (its comment and its three lines) with:

```go
// nwDeps points the agent actions at the fake Engine, on a host with no IPv4 route or address.
func nwDeps(d *fdDocker) agentDeps { return nwHostDeps(d, nil, nil) }

// nwHostDeps points the agent actions at the fake Engine, on a host whose IPv4 routes have the destinations routes and
// whose one interface has the addresses addrs. A read of the IPv6 routes fails, because the actions need only IPv4.
func nwHostDeps(d *fdDocker, routes, addrs []string) agentDeps {
	var rs []netstat.Route
	for _, r := range routes {
		rs = append(rs, netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix(r), OutIface: "eth0", OutIndex: 2})
	}
	ifc := netstat.Interface{Index: 2, Name: "eth0", Up: true, Running: true}
	for _, a := range addrs {
		ifc.Addrs = append(ifc.Addrs, netip.MustParsePrefix(a))
	}
	return agentDeps{
		docker: func() (panelDocker, error) { return d, nil },
		routes: func(f netstat.Family) ([]netstat.Route, error) {
			if f != netstat.IPv4 {
				return nil, fmt.Errorf("fake: routes of family %d", f)
			}
			return rs, nil
		},
		interfaces: func() ([]netstat.Interface, error) { return []netstat.Interface{ifc}, nil },
	}
}
```

3. In `TestNwCreatePlanPicksSubnet`, replace the doc comment's second line
`// network, gives the gateway the first host address and the proxy the second, and refuses when none is free.` with:

```go
// network and no IPv4 route of /8 or longer or address of the host, gives the gateway the first host address and the
// proxy the second, and refuses when none is free, naming what takes the range.
```

4. In `TestNwCreatePlanPicksSubnet`, replace the line `tests := []struct {` and the three field lines after it (`name`, `setup`, `subnet`) with:

```go
	const taken = "every one overlaps a Docker network's subnet or a host route or address"
	tests := []struct {
		name          string
		setup         func(d *fdDocker)
		routes, addrs []string // the host's IPv4 route destinations and interface addresses
		subnet        string   // "" when none is free
		why           string   // what the refusal names, when set
```

5. Add these rows after the existing last row (`{name: "every block taken", ...}},`), before the closing `}` of the table:

```go
		{name: "a host route over the top block", routes: []string{"172.31.255.248/29"}, subnet: "172.31.255.240/29"},
		{name: "a VPN route over the upper half", routes: []string{"172.31.255.128/25"}, subnet: "172.31.255.120/29"},
		{name: "a LAN address inside the range", addrs: []string{"172.31.255.200/26"}, subnet: "172.31.255.184/29"},
		{name: "a host address of one address", addrs: []string{"172.31.255.250/32"}, subnet: "172.31.255.240/29"},
		{name: "the default route", routes: []string{"0.0.0.0/0"}, subnet: "172.31.255.248/29"},
		{name: "a default route split in halves", routes: []string{"0.0.0.0/1", "128.0.0.0/1"}, subnet: "172.31.255.248/29"},
		{name: "a /7 route over the range", routes: []string{"172.0.0.0/7"}, subnet: "172.31.255.248/29"},
		{name: "routes and addresses outside the range", routes: []string{"10.0.0.0/8", "192.168.1.0/24"},
			addrs: []string{"127.0.0.1/8", "192.168.1.10/24", "fd00::1/64"}, subnet: "172.31.255.248/29"},
		{name: "a /8 route over the range", routes: []string{"172.0.0.0/8"}, why: taken + " (172.0.0.0/8)"},
		{name: "a VPN route covering the range", routes: []string{"172.16.0.0/12"}, why: taken + " (172.16.0.0/12)"},
		{name: "Docker and the host together", setup: takeFrom8, routes: []string{"172.31.255.8/29"}, addrs: []string{"172.31.255.3/29"},
			why: taken + " (172.31.255.0/29, 172.31.255.8/29, 172.31.255.16/28, 172.31.255.32/27, 172.31.255.64/26, 172.31.255.128/25)"},
```

6. In the loop body of `TestNwCreatePlanPicksSubnet`, replace from the line
`out, err := networkCreateAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))`
through the `return` and closing `}` of the `if tt.subnet == "" {` block with:

```go
			out, err := networkCreateAction(nwHostDeps(d, tt.routes, tt.addrs)).Handler.Plan(context.Background(), nwEnv(nil),
				nwJSON(t, NetworkCreateInput{}))
			if tt.subnet == "" {
				var coded *errcode.Error
				if err == nil || !strings.Contains(err.Error(), "panel: no free /29 is left in 172.31.255.0/24: ") ||
					!strings.Contains(err.Error(), tt.why) || errors.As(err, &coded) || !core.IsDefinite(err) {
					t.Fatalf("error %v, want a definite plain error naming that no /29 is free and %q", err, tt.why)
				}
				return
			}
```

7. In `TestNwCreatePlanRefusals`, insert before the line `t.Run("client cannot be built", func(t *testing.T) {`:

```go
	for _, tc := range []struct{ name, want string }{
		{"host routes cannot be read", "panel: read the host's IPv4 routes: fake: netlink refused"},
		{"host addresses cannot be read", "panel: read the host's interface addresses: fake: netlink refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := fdNewDocker()
			deps := nwDeps(d)
			if strings.Contains(tc.name, "routes") {
				deps.routes = func(netstat.Family) ([]netstat.Route, error) { return nil, errors.New("fake: netlink refused") }
			} else {
				deps.interfaces = func() ([]netstat.Interface, error) { return nil, errors.New("fake: netlink refused") }
			}
			_, err := networkCreateAction(deps).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
			if err == nil || err.Error() != tc.want || !core.IsDefinite(err) {
				t.Errorf("error %v, want the definite error %q", err, tc.want)
			}
			if len(d.Created) != 0 {
				t.Error("a refused plan created a network")
			}
		})
	}
```

In `internal/modules/panel/deps_test.go`, in `TestFdProductionAgentDialsNoDaemon`, replace the line
`docker, err := productionAgent().docker()` with:

```go
	d := productionAgent()
	if d.routes == nil || d.interfaces == nil {
		t.Fatal("productionAgent does not read the host's routes and interface addresses")
	}
	docker, err := d.docker()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 600 go test -count=1 -run 'TestNwCreatePlan|TestFdProductionAgentDialsNoDaemon' ./internal/modules/panel/`
Expected: FAIL, build failed, with `unknown field routes in struct literal of type agentDeps` and `d.routes undefined (type agentDeps has no field or method routes)`. Once only the two fields of Step 3.1 exist, the same run fails on behaviour: four rows with `diff [{+ network darkpanel-panel 172.31.255.248/29} ...], want [{+ network darkpanel-panel 172.31.255.240/29} ...]` (or `.120/29`, `.184/29`), three rows with `error <nil>, want a definite plain error naming that no /29 is free and "every one overlaps ..."`, and both new refusal subtests with `error <nil>, want the definite error "panel: read the host's ..."`.

- [ ] **Step 3: Implement**

1. `internal/modules/panel/deps.go`: import `github.com/darkraise/darkpanel/internal/platform/netstat`. Add two fields to `agentDeps`, after `docker`: `routes func(netstat.Family) ([]netstat.Route, error)` and `interfaces func() ([]netstat.Interface, error)`. In `productionAgent`, set them to `netstat.Routes` and `netstat.Interfaces` in the returned literal; both are read-only netlink and `net.Interfaces` reads, so the test-binary guard on the Docker socket needs no counterpart for them.
2. `internal/modules/panel/network.go`: add to the `const` block, with a comment that says why (a shorter route stands in for a default route, as OpenVPN's `def1` splits `0.0.0.0/0` into two `/1`s, and names no network of hosts a /29 could shadow):

   ```go
   networkRouteMinBits = 8
   ```

3. Add `func networkHostTaken(d agentDeps) ([]netip.Prefix, error)`. It calls `d.routes(netstat.IPv4)`; on error it returns `core.Definite(fmt.Errorf("panel: read the host's IPv4 routes: %w", err))`. Then `d.interfaces()`; on error `core.Definite(fmt.Errorf("panel: read the host's interface addresses: %w", err))`. It returns the `Dst` of every route whose `Dst` is valid, IPv4 and has `Bits() >= networkRouteMinBits` (every route type and every table: a Tailscale subnet route lives in table 52, a blackhole route also holds the range), followed by every valid IPv4 prefix in every interface's `Addrs` (up or down, loopback included; IPv6 prefixes are skipped).
4. Change `networkChoose` to `func networkChoose(ctx context.Context, d agentDeps, docker panelDocker) (networkChoice, error)`. Keep the `ListNetworks` read, its error and the `PanelAlreadyExposed` refusal first and unchanged, so an unreachable Engine and an existing network still refuse before the host is read. After the network loop, append `networkHostTaken(d)`'s prefixes to `taken` (return its error as is). The block search is unchanged (`block.Overlaps`, which masks host bits). Update its doc comment to name the host's routes and addresses.
5. Replace the no-free-block refusal with a `core.Definite` plain `fmt.Errorf` (not an `*errcode.Error`; no registered code fits, as before) whose text is exactly
   `panel: no free /29 is left in 172.31.255.0/24: every one overlaps a Docker network's subnet or a host route or address (<list>)`,
   built from `networkBlockBits` and `PanelRange`, where `<list>` is every prefix in `taken` that overlaps `PanelRange`, each `Masked()`, sorted by address (`netip.Addr.Compare`) then by prefix length, with duplicates removed (`slices.Compact`), joined by `", "`.
6. In `networkCreateHandler.Plan` and `networkCreateHandler.Apply`, call `networkChoose(ctx, h.d, docker)`. Nothing else in either changes; the plan hash still covers only the subnet, so a route that appears over the planned subnet changes the hash and the apply refuses the stale plan.
7. `docs/user/modules/panel.md`, step 2 of "What the add does": replace the sentence `` The subnet is the highest free /29 of `172.31.255.0/24` that overlaps no subnet of any Docker network on the host. `` with, keeping the three-space indent and the wrap near 80 columns:

   ```
   The subnet is the highest free /29 of `172.31.255.0/24` that overlaps no
   subnet of any Docker network on the host, no route of the host of /8 or
   longer, and no address of the host's interfaces, so a LAN or VPN already on
   that range keeps its addresses. If none is free, the add refuses and names
   what takes the range.
   ```

   The next sentence, `The gateway is the first host address of the subnet, and the proxy the second.`, stays.
8. No wire type, action schema or action `Summary` changes, so `make generate` has nothing to regenerate.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/modules/panel/ ./internal/docs/... && timeout 300 go vet ./internal/modules/panel/`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/modules/panel` and `ok  	github.com/darkraise/darkpanel/internal/docs`, and no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/panel/deps.go internal/modules/panel/network.go internal/modules/panel/network_test.go internal/modules/panel/deps_test.go docs/user/modules/panel.md
git commit -m "fix(panel): avoid host routes when choosing subnet"
```

### Task 4: Leave hidden step children out of the steps route

**Files:**
- Modify: `internal/controller/api/changes.go` (`handleSteps`, new `hideStepChildren`)
- Modify: `docs/user/modules/panel.md` ("What the removal does")
- Test: `internal/controller/api/changes_test.go`

**Interfaces:**
- Consumes: none
- Produces: K3 (the steps route half)

**Items:** 2026-10-21-v1-completion#409
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Row 409 is split in two tasks: as one task it would score files 2, spec 2, coupling 2 (the api route and the server's event wiring), reducible 6, which Rule S forbids. This half and Task 5 share no file and neither consumes the other.

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/api/changes_test.go` (every import it uses is already in the file):

```go
// TestChangeStepsHideBreakGlassChildren: the steps of a change the network shows keep their child change ids,
// except a step whose action the network hides (a break-glass action, or a controller-side action no longer
// registered), whose child and earlier children are blanked, so the web UI renders it without a link that would
// answer change.not_found.
func TestChangeStepsHideBreakGlassChildren(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	step := func(id, host, action, child string, earlier ...string) core.StepView {
		var ref core.ActionRef
		if err := ref.UnmarshalText([]byte(action)); err != nil {
			t.Fatal(err)
		}
		return core.StepView{ID: id, Host: host, Action: ref, State: core.StepApplied, Attempt: 1, ChildChangeID: child,
			EarlierChildChangeIDs: append([]string{}, earlier...)}
	}
	h.disp.StepsFunc = func(context.Context, core.Principal, string) ([]core.StepView, error) {
		return []core.StepView{
			step("add", "h1", "demo.item.add/v1", "k1", "k0"),
			step("remove", "", "demo.item.remove/v1", "k2"),
			step("reset", "", "demo.secret.reset/v1", "k3", "k3a"),
			step("gone", "", "demo.gone.away/v1", "k4"),
			step("other", "h1", "other.item.set/v1", "k5"),
		}, nil
	}
	rec := h.serve(withToken(request("GET", "/api/v1/changes/c7/steps", ""), h.token(core.ScopeRead)))
	if rec.Code != http.StatusOK {
		t.Fatalf("steps: %d %s", rec.Code, rec.Body)
	}
	var got stepsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"add": "k1 [k0]", "remove": "k2 []", "reset": " []", "gone": " []", "other": "k5 []"}
	if len(got.Steps) != len(want) {
		t.Fatalf("steps %+v, want %d", got.Steps, len(want))
	}
	for _, s := range got.Steps {
		if g := fmt.Sprintf("%s %v", s.ChildChangeID, s.EarlierChildChangeIDs); g != want[s.ID] {
			t.Errorf("step %s: child and earlier children %q, want %q", s.ID, g, want[s.ID])
		}
	}
	if strings.Contains(rec.Body.String(), `"k3`) || strings.Contains(rec.Body.String(), `"k4"`) {
		t.Errorf("the answer names a hidden child: %s", rec.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 600 go test -count=1 -run 'TestChangeStepsHideBreakGlassChildren' ./internal/controller/api/`
Expected: FAIL with
`step reset: child and earlier children "k3 [k3a]", want " []"`,
`step gone: child and earlier children "k4 []", want " []"` and
`the answer names a hidden child: {"steps":[...]}`.

- [ ] **Step 3: Implement**

1. In `internal/controller/api/changes.go`, add `func hideStepChildren(reg *core.Registry, steps []core.StepView)`. For each step, the step is hidden when `!NetworkVisible(reg, core.ChangeView{Action: st.Action.Name, Version: st.Action.Version, HostID: st.Host})`: the child change of a step runs the step's action on the step's host (a controller-side step stores host `""`, like a controller-side change), so this is exactly the rule `handleChange` applies when the link is followed. For a hidden step set `ChildChangeID` to `""` and `EarlierChildChangeIDs` to `[]string{}` (non-nil, so the JSON stays `[]`). Leave every other field (`State`, `Attempt`, `ChildDeadline`, `Error`) as is, so the web UI still shows the step's state and error.
2. In `handleSteps`, call `hideStepChildren(s.cfg.Registry, steps)` after the `if steps == nil` normalization and before `writeJSON`. It mutates in place; `Dispatcher.Steps` builds a new slice on every call (`internal/core/lifecycle/release.go:243`). No socket exception is needed: the controller socket mux (`internal/controller/api/socket.go:203`) does not serve the steps route.
3. No web change: `web/src/pages/change.tsx:146` already renders `—` and no link when `childChangeId` is `""`.
4. `docs/user/modules/panel.md`, "What the removal does": after the sentence ending `` or `darkpanel change repair <change-id>`. ``, add to the same paragraph:

   ```
   The web UI shows the removal's revoke step without a link to its change,
   because the network API hides break-glass changes; if that step needs repair,
   give the removal up with `darkpanel change release <change-id> --reason <why>`,
   which releases the revoke change with it, and run the removal again.
   ```

5. No wire type or schema changes (`childChangeId` stays a string, `earlierChildChangeIds` an array), so `make generate` has nothing to regenerate.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/controller/api/ ./internal/docs/... && timeout 300 go vet ./internal/controller/api/`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/controller/api` and `ok  	github.com/darkraise/darkpanel/internal/docs`, and no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/changes.go internal/controller/api/changes_test.go docs/user/modules/panel.md
git commit -m "fix(api): drop hidden children from steps"
```

### Task 5: Leave hidden step children out of the live step events

**Files:**
- Modify: `internal/controller/server/services.go` (`publishEngineEvent`)
- Modify: `docs/security/threat-model.md` (Authentication, Mitigations: the break-glass bullet)
- Test: `internal/controller/server/services_test.go`

**Interfaces:**
- Consumes: none
- Produces: K3 (the event half)

**Items:** 2026-10-21-v1-completion#409
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Extend the test `internal/controller/server/services_test.go::TestSvStepEventsHideBreakGlassComposites`**

Replace its doc comment's second line `// network listeners hide, and of a change it cannot read, and passes every other event on unchanged.` with:

```go
// network listeners hide, and of a change it cannot read, and passes every other event on unchanged, except that it
// blanks the child of a step whose child the network listeners hide. A child the controller does not hold is an
// agent's, which is never break-glass, and is kept.
```

Append to the end of the test body, after `pubWant(t, ch, events.TypeHost, events.HostEvent{HostID: "h1", Online: true})`:

```go

	const agentChild = "01SVAGENTCHILD000000000000"
	for _, child := range []string{hidden, shown, agentChild} {
		publish(lifecycle.EventCompositeStep, lifecycle.StepEvent{ChangeID: shown, StepID: "s2", State: core.StepApplied, ChildChangeID: child})
	}
	for _, child := range []string{"", shown, agentChild} {
		pubWant(t, ch, events.TypeCompositeStep, lifecycle.StepEvent{ChangeID: shown, StepID: "s2", State: core.StepApplied, ChildChangeID: child})
	}
```

(`hidden` is the test's stored `core.user.reset-password` change, a break-glass controller change; `shown` its `core.module.disable` change; `agentChild` has no row, as an agent's child has none in the controller database.)

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 600 go test -count=1 -run 'TestSvStepEventsHideBreakGlassComposites|TestServerEventWiring' ./internal/controller/server/`
Expected: FAIL with `composite.step event = {"changeId":"01SVSHOWN00000000000000000","stepId":"s2","state":"applied","childChangeId":"01SVHIDDEN0000000000000000"}, want {"changeId":"01SVSHOWN00000000000000000","stepId":"s2","state":"applied","childChangeId":""}`; `TestServerEventWiring` PASSES.

- [ ] **Step 3: Implement**

1. In `publishEngineEvent` (`internal/controller/server/services.go`), inside the `if step, ok := data.(lifecycle.StepEvent); ok && eventType == lifecycle.EventCompositeStep {` block, after the existing `if !s.networkVisible(view) { return }`: when `step.ChildChangeID != ""`, read the child with `s.engine.Change(context.Background(), step.ChildChangeID)` and decide:
   - the error is an `*errcode.Error` (`errors.As`) with `Code == errcode.ChangeNotFound`: keep the child. The controller database holds no row for an agent's change, and an agent-side action is never break-glass (`internal/core/registry.go:504`), so such a child is shown.
   - any other error: `s.log.Warn("server: composite step event: its child is unreadable; the child id is left out", "change", step.ChangeID, "child", step.ChildChangeID, "error", err)` and blank the child (fail closed; the event still goes out so the web UI refetches).
   - `!s.networkVisible(child)`: blank the child.
   Then set `data = step` so the published value carries the decision. The "any other error" branch has no test (see Assumptions): it is an accepted residual that the final review's security lens reads by inspection. An event without a child, and every other event type, is published unchanged.
2. Import `github.com/darkraise/darkpanel/internal/core/errcode` in `services.go` (not imported today).
3. Update the doc comment of `publishEngineEvent` to say that the child of a step whose child the network hides is left out.
4. Why the child and not the step's action: `lifecycle.StepEvent` carries no action, and adding one would change the event's JSON on the stream; reading the child is one indexed query, the same rule `handleChange` applies when the link is followed. `TestServerEventWiring` publishes a step event whose child `K1` has no row and expects it unchanged; the not-found branch keeps it.
5. No web change: `web/src/api/events.ts:61` uses a `composite.step` event only to refetch `["change", id]` and `["steps", id]`, never its `childChangeId`. No wire type changes.
6. `docs/security/threat-model.md`, **Authentication, Mitigations**, the bullet `` - Break-glass user commands run only on the controller socket, which is root-only through `SO_PEERCRED` (`internal/controller/api`). ``: append ` The network routes and event stream hide break-glass changes, including the child change ids of composite steps that run a break-glass action (\`internal/controller/api\`, \`internal/controller/server\`).`

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/controller/server/ ./internal/docs/... && timeout 300 go vet ./internal/controller/server/`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/controller/server` and `ok  	github.com/darkraise/darkpanel/internal/docs`, and no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/services.go internal/controller/server/services_test.go docs/security/threat-model.md
git commit -m "fix(server): drop hidden children from step events"
```

### Task 6: End a host lock wait when its context ends

**Files:**
- Modify: `internal/controller/dispatch/hosts.go` (`Hosts` struct field `locks`, `NewHosts`, `lockHost`, the doc comments of `Agent` and `lockHost`)
- Test: `internal/controller/dispatch/hosts_test.go`

**Interfaces:**
- Consumes: none
- Produces: K4

**Items:** 2026-10-21-v1-completion#470
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

In `internal/controller/dispatch/hosts_test.go`, add `"sync/atomic"` to the import block (between `"strings"` and `"testing"`), then append:

```go
// TestM3gALockWaitEndsWithItsContext: a reach of a host whose Hello hangs under another caller returns its own
// context's error once that context ends, and says no Hello of its own; a waiter whose context outlives the hung
// Hello shares that one Hello.
func TestM3gALockWaitEndsWithItsContext(t *testing.T) {
	h := newHarness(t)
	h.addHost("h2")
	var hellos atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		if hellos.Add(1) == 1 {
			close(started)
			<-release
		}
		return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
	}})
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer releaseOnce()
	holder := make(chan error, 1)
	go func() {
		_, err := h.hosts.Agent(context.Background(), "h2")
		holder <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the Hello of h2 never started")
	}

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	bounded := make(chan error, 1)
	go func() {
		_, err := h.hosts.Agent(short, "h2")
		bounded <- err
	}()
	select {
	case err := <-bounded:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Agent(h2) with a 50 ms deadline = %v, want an error wrapping context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Agent(h2) with a 50 ms deadline still waits for the host lock after 5 s")
	}

	waiter := make(chan error, 1)
	go func() {
		_, err := h.hosts.Agent(context.Background(), "h2")
		waiter <- err
	}()
	select {
	case err := <-waiter:
		t.Fatalf("Agent(h2) returned %v while the Hello of h2 hangs; want it to wait for that Hello", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce()
	for name, ch := range map[string]chan error{"the holder": holder, "the waiter": waiter} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("%s: Agent(h2) after the Hello returned: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: Agent(h2) did not return after the Hello was released", name)
		}
	}
	if n := hellos.Load(); n != 1 {
		t.Errorf("Hello calls = %d, want 1: the callers of one host share one Hello", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestM3gALockWaitEndsWithItsContext' ./internal/controller/dispatch/`
Expected: FAIL after about 5 s with `Agent(h2) with a 50 ms deadline still waits for the host lock after 5 s`

- [ ] **Step 3: Implement**

1. In the `Hosts` struct, change the field `locks map[string]*sync.Mutex` to `locks map[string]chan struct{}`: one channel of capacity 1 per host, holding a value while the host is locked. Keep the field comment's meaning (held across the client lookup and Hello) and say it is a one-slot semaphore so a wait can end with its context. `mu` stays a `sync.Mutex`, so the `"sync"` import stays.
2. In `NewHosts`, initialise `locks: map[string]chan struct{}{}`.
3. In `func (h *Hosts) lockHost(ctx context.Context, hostID string) (unlock func(), err error)`, keep the `core_hosts` query and its two error returns exactly as they are (not_found `no host <id>`, and `dispatch: read host <id>: %w`). Under `h.mu`, look up the host's channel and create it with `make(chan struct{}, 1)` when missing; release `h.mu` before waiting (it is never held across a wait). Then `select` on sending `struct{}{}` into the channel, which returns the unlock function `func() { <-l }` and a nil error, and on `<-ctx.Done()`, which returns `nil` and `fmt.Errorf("dispatch: wait for host %s: %w", hostID, ctx.Err())`. This is the same pattern as `planLocks.lock` in `internal/controller/dispatch/apply.go`.
4. The lock table still never shrinks, and a host absent from `core_hosts` still adds no entry. Do not change `Agent`, `Hello` or `hello` beyond their doc comments.
5. Doc comments: on `lockHost`, add that a wait for the lock ends with ctx and answers ctx's error wrapped. On `Agent`, change "A Hello that hangs delays only the calls for its own host." to "A Hello that hangs delays only the calls for its own host, each only until its own context ends."

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 -race ./internal/controller/dispatch/ && timeout 300 go vet ./internal/controller/dispatch/`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/controller/dispatch` and no vet output

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/hosts.go internal/controller/dispatch/hosts_test.go
git commit -m "fix(dispatch): end a host lock wait with its context"
```

### Task 7: Warn only for hosts that run tailscale

**Files:**
- Modify: `internal/modules/derp/server_probe.go` (`probeSelves`, `probeSelf`, new `probeTailscaleOff`)
- Test: `internal/modules/derp/server_probe_test.go`

**Interfaces:**
- Consumes: none
- Produces: K5

**Items:** 2026-10-21-v1-completion#471
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 1 = 5

Coupling 1: DERP now reads `core_module_state`, a core table it does not own, as it already reads `core_hosts`. Ruling: a host with the tailscale module enabled but tailscaled not installed keeps the warning.

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/derp/server_probe_test.go` (every import it needs is already in the file):

```go
// TestM3gProbeWarnsOnlyWhereTailscaleRuns: a host whose tailscale module core_module_state marks disabled or
// unavailable is not read and not warned about, nor is a host whose agent refuses tailscale.status as not_found or
// module.unavailable; a host where the module is usable, or has no state row, still gets the warning when the read
// fails.
func TestM3gProbeWarnsOnlyWhereTailscaleRuns(t *testing.T) {
	const charlie = "the tailscale status of host charlie could not be read, so its node use is not reported"
	type state struct {
		enabled, available int
		reason             any // nil stores NULL
	}
	cases := []struct {
		name   string
		state  *state // h3's core_module_state row for tailscale; nil for none
		refuse error  // h3's answer to tailscale.status
		read   bool   // whether the probe reads h3's tailscale.status
		warned bool   // whether the warnings name charlie
	}{
		{"module disabled", &state{0, 1, nil}, errors.New("dpHost: no resource tailscale.status"), false, false},
		{"module unavailable", &state{1, 0, "tailscale: the architecture 386 is not supported; amd64 or arm64 is needed"},
			errors.New("dpHost: no resource tailscale.status"), false, false},
		{"agent lacks the module", nil, errcode.New(errcode.NotFound, "no resource tailscale.status"), true, false},
		{"agent refuses the module", nil, errcode.New(errcode.ModuleUnavailable,
			"module tailscale is unavailable: the module's agent migrations failed; see the service log"), true, false},
		{"module usable, read fails", &state{1, 1, nil}, errors.New("dpHost: no resource tailscale.status"), true, true},
		{"no state row, read fails", nil, errors.New("dpHost: no resource tailscale.status"), true, true},
		{"tailscaled does not answer", &state{1, 1, nil},
			errcode.New(errcode.TailscaleUnavailable, "tailscaled does not answer on its socket"), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := pbWorld(t)
			h.tailnet = false
			pbNodeWorld(h)
			if c.state != nil {
				if _, err := h.ctlDB.Writer().ExecContext(context.Background(),
					`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
					 VALUES ('h3', ?, ?, ?, ?, ?)`,
					tailscale.ID, c.state.enabled, c.state.available, c.state.reason, storeTime(h.clock.Now())); err != nil {
					t.Fatal(err)
				}
			}
			env := h.ctlEnv("")
			var reads atomic.Int32
			fake := h.agent("h3").fake()
			resource := fake.ResourceFunc
			fake.ResourceFunc = func(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
				if name != "tailscale.status" {
					return resource(ctx, p, name)
				}
				reads.Add(1)
				return nil, c.refuse
			}
			env.Agents.(coretest.MapHosts)["h3"] = fake
			got, err := h.dp().probeServer(context.Background(), env, ServerProbeInput{HostID: "h1"})
			if err != nil {
				t.Fatal(err)
			}
			if read := reads.Load() > 0; read != c.read {
				t.Errorf("tailscale.status of h3 read %v, want %v", read, c.read)
			}
			if named := slices.Contains(got.Warnings, charlie); named != c.warned {
				t.Errorf("warnings %q: charlie named %v, want %v", got.Warnings, named, c.warned)
			}
		})
	}
}

// m3gHangingHosts is a host directory whose lookup of host hang blocks until the caller's context ends, as
// dispatch.Hosts does while another caller's Hello of that host hangs.
type m3gHangingHosts struct {
	core.HostDirectory
	hang string
}

func (d m3gHangingHosts) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	if hostID == d.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return d.HostDirectory.Agent(ctx, hostID)
}

// TestM3gProbeBoundsTheTailscaleReadsHostLookup: a tailscale status read whose host lookup hangs fails once 30 s have
// passed on the controller's clock, and the probe answers with that host named in the warnings.
func TestM3gProbeBoundsTheTailscaleReadsHostLookup(t *testing.T) {
	h := pbWorld(t)
	h.tailnet = false
	pbNodeWorld(h)
	env := h.ctlEnv("")
	env.Agents = m3gHangingHosts{HostDirectory: env.Agents, hang: "h3"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	var got ServerProbe
	var err error
	go func() {
		defer close(done)
		got, err = h.dp().probeServer(ctx, env, ServerProbeInput{HostID: "h1"})
	}()
	timeout := time.After(10 * time.Second)
wait:
	for {
		select {
		case <-done:
			break wait
		case <-timeout:
			t.Fatal("the probe of a host whose lookup hangs did not end")
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
	const charlie = "the tailscale status of host charlie could not be read, so its node use is not reported"
	if !slices.Contains(got.Warnings, charlie) {
		t.Errorf("warnings %q, want %q", got.Warnings, charlie)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestM3gProbeWarnsOnlyWhereTailscaleRuns|TestM3gProbeBoundsTheTailscaleReadsHostLookup' ./internal/modules/derp/`
Expected: FAIL. Subtests `module_disabled` and `module_unavailable` each print `tailscale.status of h3 read true, want false` and `warnings ["the tailscale status of host charlie could not be read, so its node use is not reported"]: charlie named true, want false`. Subtests `agent_lacks_the_module` and `agent_refuses_the_module` print only the `charlie named true, want false` line. The last three subtests pass: they pin the warning that must stay. `TestM3gProbeBoundsTheTailscaleReadsHostLookup` fails after about 10 s with `the probe of a host whose lookup hangs did not end`.

- [ ] **Step 3: Implement**

1. Add `func probeTailscaleOff(ctx context.Context, env core.Env) (map[string]bool, error)` to `server_probe.go`. It returns the ids of the hosts whose tailscale module the controller records as not usable: `SELECT host_id FROM core_module_state WHERE module_id = ? AND (enabled = 0 OR available = 0)` with `tailscale.ID`, read through `env.DB.Read().QueryContext`. Scan, `rows.Err` and query failures return `fmt.Errorf("derp: read the tailscale module state: %w", err)`, and rows are closed as `probeHosts` closes them. A host with no row is not in the map. Give it a doc comment saying why: the agent does not refuse a disabled module's resource, so `core_module_state` is the authority on enablement.
2. In `probeSelves`, call `probeTailscaleOff` first and return its error as `nil, nil, err`. Build the list of hosts not in that map, in host order, and run `serverProbeEach` and the result loop over that list instead of `hosts`. A skipped host lands in neither `selves` nor `unreadable`.
3. In the result loop, a failed read goes into `unreadable` unless `errcode.From(r.err).Code` is `errcode.NotFound` (the agent has no `tailscale.status` resource, as `runtime.Resource` answers when its registry lacks the module) or `errcode.ModuleUnavailable` (the agent refuses the module). Those hosts land in neither result. Every other failure, `tailscale.unavailable` and uncoded errors included, still goes into `unreadable`.
4. In `probeSelf`, take `bounded, cancel := serverProbeBounded(ctx, env)` and `defer cancel()` first, then call `env.Agents.Agent(bounded, hostID)` and `agent.Resource(bounded, ...)`, so the 30-second bound covers the host lookup too, as `probeFromAgent` bounds its whole read. A lookup that the bound ends fails the read like any other failure, so the host lands in `unreadable`. Update its doc comment to say the lookup and the read share the bound.
5. Update the doc comment of `probeSelves`: hosts whose tailscale module is disabled or unavailable are not read, and a read refused as not_found or module.unavailable is not counted unreadable.
6. Do not change: `probeUnreadableWarning`'s text, the `ctx.Err()` check after the reads, the rule that only a node with addresses enters `selves`, and `probeHosts` (the probing-agent list still covers every host).

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/modules/derp/ && timeout 300 go vet ./internal/modules/derp/`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/modules/derp` and no vet output. The package takes about 5 minutes on a loaded host.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/server_probe.go internal/modules/derp/server_probe_test.go
git commit -m "fix(derp): warn only for hosts that run tailscale"
```

### Task 8: Keep held connector, host and exposure data when a refetch fails

**Files:**
- Modify: `web/src/modules/cloudflare/connectors-page.tsx` (`ConnectorsCard`, `ConnectorsPage`)
- Modify: `web/src/modules/npm/banner.tsx` (`AdminExposureBanner`)
- Test: `web/src/modules/cloudflare/connectors-page.test.tsx`
- Test: `web/src/modules/npm/banner.test.tsx`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 2026-10-21-v1-completion#472, 2026-10-21-v1-completion#473
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

This task covers three defects: the connectors list and the host list (row 472) and the banner (row 473).

- [ ] **Step 1: Write the failing test**

In `web/src/modules/cloudflare/connectors-page.test.tsx`, add this import directly above `import type { Change, Host, Plan } from "@/api/types"`:

```tsx
import { HOSTS_QUERY_KEY } from "@/api/client"
```

this import directly above `import { ConnectorsPage } from "./connectors-page"`:

```tsx
import { connectorListKey } from "./connector-types"
```

and these two tests inside `describe("ConnectorsPage", ...)`, directly above `test("has no accessibility violations", ...)`:

```tsx
  test("keeps the connectors and shows the reason when a refetch of them fails", async () => {
    let failing = false
    const { client } = cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: () =>
        failing
          ? {
              status: 503,
              body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker Engine is not reachable", code: "docker.engine_unavailable" },
            }
          : { body: { output: { connectors: [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })] } } },
    })
    await cnTable()
    failing = true
    await client.invalidateQueries({ queryKey: connectorListKey("h1") })
    expect((await screen.findByRole("alert")).textContent).toBe("the Docker Engine is not reachable")
    const rows = within(screen.getByRole("table", { name: "Connectors" })).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 3).map((c) => c.textContent))).toEqual([
      ["darkpanel-c1", "darkpanel-cloudflared-t1", "running"],
    ])
    const form = screen.getByRole("form", { name: "Deploy a connector" })
    expect(within(form).queryByRole("radio", { name: "darkpanel-c1" })).toBeNull()
    failing = false
    await client.invalidateQueries({ queryKey: connectorListKey("h1") })
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
    expect(screen.getByRole("table", { name: "Connectors" })).toBeTruthy()
  })

  test("keeps the page and an open change dialog and shows the reason when a refetch of the hosts fails", async () => {
    let failing = false
    const { client } = cnRender([], {
      [HOSTS]: () =>
        failing
          ? { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } }
          : { body: { output: { hosts: [cnHost("h1", "web-1")] } } },
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/plan": { body: cnPlan("cloudflare.connector.stop", "h1") },
    })
    const user = userEvent.setup()
    await cnTable()
    await user.click(screen.getByRole("button", { name: "Stop the connector of darkpanel-c1" }))
    const dialog = await screen.findByRole("dialog", { name: "Stop the connector of darkpanel-c1" })
    await within(dialog).findByLabelText("Planned changes")
    failing = true
    await client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
    // The open dialog hides the page from the accessibility tree, so the alert is found by its text.
    expect((await screen.findByText("the database is busy")).closest('[role="alert"]')).not.toBeNull()
    expect(dialog.isConnected).toBe(true)
    expect(screen.getByRole("dialog", { name: "Stop the connector of darkpanel-c1" })).toBe(dialog)
    expect(screen.getByText("Connectors on web-1")).toBeTruthy()
    failing = false
    await client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
    await waitFor(() => expect(screen.queryByText("the database is busy")).toBeNull())
    expect(dialog.isConnected).toBe(true)
  })

```

In `web/src/modules/npm/banner.test.tsx` (its imports already carry `waitFor`, `npmFleetKey`, `BN_HOSTS`, `bnHosts` and `bnHost`), add this test directly above `test("the fleet read is kept for 60 seconds before a new mount reads it again", ...)`:

```tsx
test("keeps the exposure warnings and adds the could-not-check warning when a refetch fails", async () => {
  let failing = false
  const { client } = bnRender(
    () =>
      failing
        ? { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } }
        : bnFleet([bnExposed("h1", "web-1", "npm.example.com")], ["h2"]),
    <AdminExposureBanner />,
    { [BN_HOSTS]: bnHosts(bnHost("h2", "web-2")) },
  )
  expect(await screen.findByText(BN_WEB1)).toBeTruthy()
  await screen.findByText("Could not check whether the NPM admin UI is exposed on: web-2.")
  failing = true
  await client.invalidateQueries({ queryKey: npmFleetKey })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
  expect(screen.getByText(BN_WEB1)).toBeTruthy()
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-1" }).getAttribute("href")).toBe("/hosts/h1/npm")
  expect(screen.getByText("Could not check whether the NPM admin UI is exposed on: web-2.")).toBeTruthy()
  failing = false
  await client.invalidateQueries({ queryKey: npmFleetKey })
  await waitFor(() => expect(screen.queryByText(BN_UNCHECKED)).toBeNull())
  expect(screen.getByText(BN_WEB1)).toBeTruthy()
})

```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 pnpm -C web exec vitest run src/modules/cloudflare/connectors-page.test.tsx src/modules/npm/banner.test.tsx`
Expected: FAIL, `Tests  3 failed | 22 passed (25)`:
- `keeps the connectors and shows the reason when a refetch of them fails` with `TestingLibraryElementError: Unable to find an accessible element with the role "table" and name "Connectors"`
- `keeps the page and an open change dialog and shows the reason when a refetch of the hosts fails` with `AssertionError: expected false to be true // Object.is equality` at `expect(dialog.isConnected).toBe(true)` (the alert shows, but the page and the dialog are unmounted)
- `keeps the exposure warnings and adds the could-not-check warning when a refetch fails` with `TestingLibraryElementError: Unable to find an element with the text: The NPM admin interface of web-1 is exposed at https://npm.example.com.`

- [ ] **Step 3: Implement**

Follow the pattern of commit `0f7af5c` (`fix(web): keep NPM data when a refetch fails`, `web/src/modules/npm/access-lists-tab.tsx`): branch on whether the query holds data (`data === undefined`), not on `isError`; with no data show the spinner while pending, else the error alert; with data show the error alert above the data while `isError` is true. The alert's wording is the existing one: the error's own message.

1. `web/src/modules/cloudflare/connectors-page.tsx`, `ConnectorsCard`: replace the chain that starts `{connectors.isError ? (` with:
   - `connectors.data === undefined`: `connectors.isPending` shows `<Spinner aria-label="Loading the connectors" />`, otherwise `<ErrorAlert error={connectors.error} />` (unchanged first-read behaviour; the tests "a failed read of the connectors shows the reason" and "a connector list that cannot be read leaves only a new tunnel on offer" pin it).
   - data held: `{connectors.isError && <ErrorAlert error={connectors.error} />}` followed by the existing empty state (`No connectors on this host`) when `connectors.data.connectors.length === 0`, else the existing `<ConnectorsTable ... />`. Give the `<CardContent>` of `ConnectorsCard` `className="flex flex-col gap-4"` (as `DeployCard` has) so the alert and the data are spaced, and wrap the two in a fragment.
   - Do not change `ErrorAlert` or `DeployCard` (it already uses `connectors.data` when held, so held connectors stay excluded from the tunnel radios).
2. `web/src/modules/cloudflare/connectors-page.tsx`, `ConnectorsPage`: replace the two lines `if (hosts.isPending) return <Spinner aria-label="Loading the hosts" />` and `if (hosts.isError) return <ErrorAlert error={hosts.error} />` with one early return taken only when `hosts.data === undefined`: the spinner while `hosts.isPending`, else `<ErrorAlert error={hosts.error} />`. With hosts held, render the page as now and add `{hosts.isError && <ErrorAlert error={hosts.error} />}` as the first child of `<div className="flex flex-col gap-6">`. The page must stay mounted, so `HostConnectors` keeps its `pending` state and an open `CloudflareChange` dialog stays open. The no-eligible-host `EmptyState` return is unchanged.
3. `web/src/modules/npm/banner.tsx`, `AdminExposureBanner`:
   - `unreadable` becomes `fleet.data?.unreadable ?? []` (retained data counts, not only `fleet.isSuccess`).
   - Replace the two early returns (`if (fleet.isError) return <NotCheckedAlert .../>` and `if (!fleet.isSuccess) return null`) with one: when `fleet.data === undefined`, return `<NotCheckedAlert text="Could not check whether the NPM admin UI is exposed." />` if `fleet.isError`, else `null`.
   - Compute `exposed` from `fleet.data.instances` as now. After the per-host line (`{names.length > 0 && <NotCheckedAlert ... on: ${names.join(", ")}. />}`), add `{fleet.isError && <NotCheckedAlert text="Could not check whether the NPM admin UI is exposed." />}`. Order: exposure warnings, the per-host could-not-check warning, the generic could-not-check warning. All stay `role="status"`; no `role="alert"` is added (the test "warns that the check failed when the fleet read fails" asserts none).
   - Update the doc comment's last sentences to say it keeps the warnings of the last successful read when a later read fails, and adds the could-not-check warning beside them.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 300 pnpm -C web exec vitest run src/modules/cloudflare/connectors-page.test.tsx src/modules/npm/banner.test.tsx`
Expected: PASS, `Tests  25 passed (25)`

Run: `timeout 600 pnpm -C web typecheck && timeout 300 pnpm -C web exec eslint --max-warnings 0 src/modules/cloudflare/connectors-page.tsx src/modules/cloudflare/connectors-page.test.tsx src/modules/npm/banner.tsx src/modules/npm/banner.test.tsx`
Expected: both exit 0 with no output from eslint.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/cloudflare/connectors-page.tsx web/src/modules/cloudflare/connectors-page.test.tsx web/src/modules/npm/banner.tsx web/src/modules/npm/banner.test.tsx
git commit -m "fix(web): keep held data when a refetch fails"
```

### Task 9: Time the revert countdown tests from the stub's answer

**Files:**
- Test: `web/src/components/change-dialog.a11y.test.tsx` (`cdaLate`, `cdaPendingAnswer`, and the four countdown tests below)

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 2026-10-21-v1-completion#469
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

Why the four tests flake: each calls `vi.useFakeTimers({ shouldAdvanceTime: true })`, which ticks the fake clock 20 ms for every 20 ms real interval, then `vi.setSystemTime(<start>)` and fixes the revert deadline at start + 1 s or start + 60 s before the dialog opens. `ChangeDialog` (`web/src/components/change-dialog.tsx:73-91`, `secondsLeft` and `useSecondsLeft`) reads `Date.now()` when the progress view mounts, and `const [spoken, setSpoken] = useState(left)` (line 203) keeps that first value as the announcement. Every real second spent opening the dialog, reading availability, planning, clicking Apply and applying moves the fake clock, so on a loaded host the first value is 0 instead of 1, or 59 instead of 60, and `make test-web` fails. Step 1 makes that second of delay reproducible by jumping the fake clock one second when the apply is answered.

- [ ] **Step 1: Extend the test `web/src/components/change-dialog.a11y.test.tsx::a countdown one second from its end is announced in the singular`**

This step extends four tests of that file: `the countdown announces itself politely at fixed points, not every second`, `a countdown one second from its end is announced in the singular`, `a tick that skips past both points is announced once` and `a revert deadline that arrives after the apply shows its countdown at once and is announced once`.

Add this helper directly below the `afterEach(() => { vi.useRealTimers() })` block:

```tsx
// cdaLate answers as handler does, one second of the fake clock late. Under
// shouldAdvanceTime real time moves the fake clock, so this is what a loaded
// test host does to the seconds spent opening, planning and applying.
function cdaLate(handler: StubHandler): StubHandler {
  return (call) => {
    vi.setSystemTime(Date.now() + 1000)
    return typeof handler === "function" ? handler(call) : handler
  }
}
```

In the first three tests named above, replace the route line

```tsx
    { [PLAN]: { body: cdaPlan() }, [APPLY]: { body: pending }, "GET /api/v1/changes/c1": { body: pending } },
```

with

```tsx
    { [PLAN]: { body: cdaPlan() }, [APPLY]: cdaLate({ body: pending }), "GET /api/v1/changes/c1": { body: pending } },
```

In the fourth test, replace

```tsx
    { [PLAN]: { body: cdaPlan() }, [APPLY]: { status: 202, body: applying }, "GET /api/v1/changes/c1": { body: pending } },
```

with

```tsx
    { [PLAN]: { body: cdaPlan() }, [APPLY]: cdaLate({ status: 202, body: applying }), "GET /api/v1/changes/c1": { body: pending } },
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 pnpm -C web exec vitest run src/components/change-dialog.a11y.test.tsx`
Expected: FAIL, `Tests  4 failed | 13 passed (17)`:
- `the countdown announces itself politely at fixed points, not every second`: `expected 'Reverts automatically in 59 seconds u…' to be 'Reverts automatically in 60 seconds u…'`
- `a countdown one second from its end is announced in the singular`: `expected 'Reverts automatically in 0 seconds un…' to be 'Reverts automatically in 1 second unl…'`
- `a tick that skips past both points is announced once`: `expected 'Reverts automatically in 59 seconds u…' to be 'Reverts automatically in 60 seconds u…'`
- `a revert deadline that arrives after the apply shows its countdown at once and is announced once`: `expected 'Reverts automatically in 59 s unless …' to be 'Reverts automatically in 60 s unless …'`

- [ ] **Step 3: Implement**

The fix is in the test file only; `web/src/components/change-dialog.tsx` does not change.

1. Add this helper directly below `cdaLate` (a stub handler, as `StubHandler` in `web/src/test/utils.tsx:23` accepts functions). It must compute the deadline once, at its first answer, and give the same change to every later call, because the change query refetches `GET /api/v1/changes/c1` on mount and a second, later deadline would restart the countdown:

```tsx
// cdaPendingAnswer answers with a pending change whose revert deadline is
// offset ms past the fake clock at its first answer, and with that same change
// after. A deadline fixed before the dialog opens would lose to the countdown
// whatever time the clock gains while the dialog opens, plans and applies.
function cdaPendingAnswer(offset: number): StubHandler {
  let pending: Change | undefined
  return () => {
    pending ??= cdaChange({ state: "pending", revertDeadline: new Date(Date.now() + offset).toISOString() })
    return { body: pending }
  }
}
```

2. In `the countdown announces itself politely at fixed points, not every second` and `a tick that skips past both points is announced once`: replace `const pending = cdaChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })` with `const pending = cdaPendingAnswer(60_000)`, and the route line with `{ [PLAN]: { body: cdaPlan() }, [APPLY]: cdaLate(pending), "GET /api/v1/changes/c1": pending },`.
3. In `a countdown one second from its end is announced in the singular`: replace `const pending = cdaChange({ state: "pending", revertDeadline: "2026-10-23T08:00:01Z" })` with `const pending = cdaPendingAnswer(1000)`, and the route line as in item 2.
4. In `a revert deadline that arrives after the apply shows its countdown at once and is announced once`: replace `const pending = cdaChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })` with `const pending = cdaPendingAnswer(60_000)`, and the route line with `{ [PLAN]: { body: cdaPlan() }, [APPLY]: cdaLate({ status: 202, body: applying }), "GET /api/v1/changes/c1": pending },`. Here the GET carries the deadline, so it is computed when the GET answers.
5. Keep `cdaLate` in all four tests: it is the reproducible slow-down that proves the tests no longer depend on real time. Leave every expected string, the `vi.useFakeTimers({ shouldAdvanceTime: true })` and `vi.setSystemTime(...)` calls, and `vi.setSystemTime(start + 55_000)` in the third test unchanged (with the one-second jump the third test's first tick still leaves 5 or 6 seconds, below its bound of 10).

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 300 pnpm -C web exec vitest run src/components/change-dialog.a11y.test.tsx`
Expected: PASS, `Tests  17 passed (17)`; run it three times, PASS each time.

Run: `timeout 600 pnpm -C web typecheck && timeout 300 pnpm -C web exec eslint --max-warnings 0 src/components/change-dialog.a11y.test.tsx`
Expected: both exit 0 with no output from eslint.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/change-dialog.a11y.test.tsx
git commit -m "test(web): time revert countdowns from the answer"
```
