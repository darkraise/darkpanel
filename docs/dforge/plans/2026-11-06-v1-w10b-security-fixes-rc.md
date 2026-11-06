# v1 wave 10b: security review fixes and release-candidate readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the second half of wave 10 of the v1 completion spec. That means fixing every high and medium finding of the R73 security review, correcting the threat model and recording the risks the owner accepted, and fixing the leftover rows 303, 389, 435 and 436. It also means documenting the 0.x to 1.0 reinstall boundary, so that `main` is ready to tag `v1.0.0-rc.1`.

**Architecture:** Every fix is a feature slice with its own regression test, at the lowest layer that can fail. The fixes touch the NPM, Docker, DERP and firewall modules, the login limiter, the runtime listener and one core lifecycle function. Fixes that touch security run alone, in the order their shared files require. Ordinary fixes and documentation run in parallel waves, with no two tasks of a wave naming the same file. The threat model is updated last, because it describes what the fixes changed. After the last wave, the owner's two-reviewer panel re-reviews the high findings before the final whole-branch review.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), SQLite through the existing `internal/core/store` migrations, React 19 + TypeScript with Vitest 5.0.5 and @testing-library/user-event 14.6.8 (already in `web/pnpm-lock.yaml`), the KVM harness in `test/harness` (GitHub Actions `harness.yml`), Markdown documentation checked by `internal/docs`.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-4 | 5 | 6-7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20 | 21 | 22 | 23-24 | 25

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w10b-tmp/integ && export TMPDIR=/root/.cache/w10b-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 120 bash scripts/fuzz.test.sh && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w10b-tmp/wave && export TMPDIR=/root/.cache/w10b-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./internal/... ./cmd/... ./release/... && timeout 1200 make lint-web test-web`

## Global Constraints

- Scope is spec revision 8 as amended on `main`, plus the rulings W10b-R1 to W10b-R14 under Assumptions, which Task 1 copies into the spec. It covers the security review bullet of spec section 9 (R73), the "Release candidate" bullet of section 10, and the register rows the tasks' **Items:** lines name. Row numbers prefixed `security-review#` are rows of `docs/dforge/registers/2026-11-06-v1-security-review.md`. Row numbers prefixed `v1-completion#` are rows of `docs/dforge/registers/2026-10-21-v1-completion.md`. The Item cell of each row gives the finding with its file and line on `main` at dc91aa2. The reviews behind them are `docs/dforge/notes/v1-security-review-fable.md` (Fable, F-numbers) and `docs/dforge/notes/v1-security-review-codex.md` (Codex, C-numbers). Each has a failure scenario per finding, and a task's regression test reproduces that scenario. Out of scope, for the owner: tagging, `make release-prep` and `CHANGELOG.md` (W10b-R13), the live demonstrations (row 55), the release prerequisites (row 35) and the 14-day production run (row 44).
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan does any of the following:
  - talks to the Docker daemon, D-Bus or systemd;
  - runs `systemctl`, `docker`, `ufw`, `iptables`, `nft`, `useradd`, `userdel` or `nmap`;
  - binds an address other than `127.0.0.1` (port 0);
  - resolves a real DNS name (Task 9's resolver is a fake);
  - reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/run/darkpanel*`, or `/proc` of another process.

  Unix sockets bind only under `t.TempDir()`. Host effects are tested through the existing fakes (`core.Runner`, `core.Units`, the Docker and NPM fakes, the file providers). Real installs, services and agents run only on harness VMs.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w10b-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first. When the task ends, remove it and run `go clean -testcache`. The reasons: `/tmp` is an 8 GiB tmpfs, and Unix socket paths over 108 bytes fail. When `df -h /` shows 90 % or more before a task's first build, trim the Go build cache (`go clean -cache` is acceptable).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml` or `web/pnpm-workspace.yaml`. After every task, `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every Go task all of these pass, with their real output recorded:
  - `go build ./...`
  - `go vet ./...` (no output)
  - `go test -count=1 ./...` (every line `ok` or `?`)
  - `go test -race -count=1` on the task's packages
  - `golangci-lint run --allow-serial-runners ./...` (`0 issues.`)
  - `go mod tidy -diff` (no output)

  A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. A task that edits `cmd/darkpanel/` also runs `go test -tags harness -count=1 ./cmd/darkpanel/` and `go vet -tags harness ./cmd/...`. A task that edits `web/` runs `make lint-web test-web`.
- Generated files: a task that changes a wire type (an exported type in a module's `wire.go`, or a `jsonschema` tag or description on one) runs `make schemas` and `make api-docs`, and commits the regenerated `schemas/<module>.*` and `docs/api/**`. That includes `docs/api/openapi.json`. Such tasks never share a wave. Every other task leaves `schemas/**` and `docs/api/**` unchanged, so `TestAdAPIDocsCurrent` stays green.
- TDD: write the test, run it and see the expected RED (a build failure naming the first undefined identifier, or a failed assertion), then implement and run GREEN. Quote the RED and GREEN output in the report verbatim, not as a paraphrase. A security fix's test reproduces the review's failure scenario and fails on `main` for the reason the finding gives. A task that skips a line of the full-suite constraint above says so and why in its report.
- Frozen files (spec 1.12). No task edits them except where its **Files:** block names the file:
  - `internal/core/**`
  - `internal/platform/*.go` (the package's own non-test files)
  - `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`
  - `internal/modules/modules.go`, `internal/cli/modules.go`, `cmd/darkpanel/paths.go`
  - `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`
  - `web/src/app.tsx`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`
  - `test/harness/pins.go`, `schemas/**` (regeneration under the Generated files rule excepted)

  New `*_test.go` files in a frozen package are allowed. Production code outside these paths changes only where a task's **Files:** block names it. A task that finds it needs an unlisted file stops and reports it; it does not edit the file.
- File ownership: only Task 1 edits the spec; only Task 4 edits `docs/versioning.md`; only Task 25 edits `docs/security/threat-model.md`. No task edits `.github/workflows/**`, `Makefile`, `docs/runbook.md` or `CHANGELOG.md`. A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave).
- Test isolation:
  - Files and databases go under `t.TempDir()`.
  - The only TCP listeners are `httptest` on `127.0.0.1` port 0.
  - Tests write no package-level mutable state, except an existing test seam (a package variable such as `serveRun`, `newSystemd` or `daemonRun`) that a helper replaces and restores through `t.Cleanup`; such tests do not call `t.Parallel`.
  - Vitest tests stub `fetch` through `stubFetch` and never reach a network.
  - Harness tests derive VM names, work directories and address ranges from the test name (`NewLab`). Harness unit tests carry `Unit` in their name, so `-run 'Unit'` selects them.

  Tasks of one wave therefore share no port, path or global state.
- Go identifiers: every new Go test function carries its task's prefix from Contracts "Test prefixes", and every new unexported helper in a test file carries the same prefix in lower case (`TestV1r08…`, `v1r08…`). Tasks that share a package therefore declare no identifier another task declares. Web test cases join the component's existing test file, or a new one named after the component.
- Code style:
  - Match the surrounding code. Put a doc comment on every exported identifier.
  - Comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review. `//nolint` names the rule and a reason.
  - gofmt clean; in `web/`, eslint `--max-warnings 0` and `tsc --noEmit` clean.
  - Error strings start with the package name. A changed refusal keeps its existing error code unless Contracts names another.
- Documentation style (Tasks 1, 4, 25): English, plain Markdown (ATX headings, one `#` per file, fenced code blocks tagged `sh`, `json` or `text`, no HTML), sentence-case headings, second person, present tense, and commands exactly as the CLI prints them (`sudo darkpanel …`). A page describes what exists on `main`; it never promises future work and never names a plan, wave, task or register row. The spec is exempt from the last two rules, as its existing "Plan rulings" bullets are. Every relative link resolves to a file in the repository. The existing `internal/docs` style, link and threat-model tests cover every page these tasks write.
- Secrets: no log line, error, test failure message, document example or screenshot carries a real token, password, TOTP secret, passphrase, tunnel token or private key. Tests use obvious placeholders (`placeholder-secret`, `example.com`, `203.0.113.10`). A test that proves a secret does not leak asserts that the placeholder is absent from the output, the log buffer, the effect document or the error.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies to the branch once it is pushed, as well as on `main`, because Task 24 edits `test/harness/` and Tasks 8, 9, 12, 18 and 19 change a module's host behaviour. Dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag, runs `release.yml` or runs `make release-prep`.
- Re-review (spec 9: "every critical and high finding is fixed and re-reviewed"). After the last wave and before the final whole-branch review, the controller runs the owner's two-reviewer panel again: `dforge:judge-fable`, and `codex exec -m gpt-6-astra -c model_reasoning_effort=high -s read-only`. Both get one shared brief: `docs/dforge/notes/v1-security-review-brief.md`, narrowed to security-review rows 1-7 and to the code their tasks changed. A row closes only when both reviewers find it closed. A finding the re-review raises is a new register row, and a critical or high one is fixed on the branch before the merge.

## Contracts

### Test prefixes

| Task | Package(s) | Go test prefix |
|---|---|---|
| 1 | — (spec only) | — |
| 2 | `internal/modules/derp`, `internal/controller/api` | `TestV1r02` |
| 3 | — (web only) | — |
| 4 | `internal/docs` | `TestV1r04` |
| 5 | `internal/modules/docker` | `TestV1r05` |
| 6 | `internal/modules/tailscale` | `TestV1r06` |
| 7 | `internal/core/lifecycle`, `internal/modules/derp` | `TestV1r07` |
| 8 | `internal/modules/npm/proxy` | `TestV1r08` |
| 9 | `internal/modules/npm`, `internal/modules/npm/proxy` | `TestV1r09` |
| 10 | `internal/modules/npm` | `TestV1r10` |
| 11 | `internal/modules/npm`, `internal/modules/npm/npmapi` | `TestV1r11` |
| 12 | `internal/modules/npm` | `TestV1r12` |
| 13 | `internal/modules/docker` | `TestV1r13` |
| 14 | `internal/modules/docker`, `internal/modules/docker/dstate` | `TestV1r14` |
| 15 | `internal/modules/derp`, `internal/platform` | `TestV1r15` |
| 16 | `internal/modules/derp` | `TestV1r16` |
| 17 | `internal/controller/auth`, `internal/controller/api` | `TestV1r17` |
| 18 | `internal/core/store`, `internal/controller/listen` | `TestV1r18` |
| 19 | `internal/modules/panel`, `internal/controller/api`, `internal/modules/firewall/guard` | `TestV1r19` |
| 20 | `internal/modules/firewall/eval` | `TestV1r20` |
| 21 | `internal/modules/firewall/guard` | `TestV1r21` |
| 22 | `internal/controller/backup` | `TestV1r22` |
| 23 | `internal/modules/npm` | `TestV1r23` |
| 24 | `test/harness` and the package its root cause lies in | `TestV1r24…Unit…` in `test/harness` (the name contains `Unit`), `TestV1r24` elsewhere |
| 25 | `internal/docs` | `TestV1r25` |

### Keyed fingerprints

Used by Tasks 10, 14.

An effect document never holds a value that the change's plan, input or effect would otherwise leak. When the document needs a value only so that a later observation can compare it, it holds a keyed fingerprint:

- format `hmac-sha256:` followed by the lower-case hex of `HMAC-SHA256(key, value)`;
- `key` is `env.Sealer.Derive(label)` (`core.Sealer.Derive`, `internal/core/contract.go`), so the fingerprint cannot be checked against a guessed value without the role key;
- `label` is `npm.effect.fingerprint/v1` in the NPM module (Task 10) and `docker.effect.fingerprint/v1` in the Docker module (Task 14);
- a value needed to restore the before-state goes into the effect's sealed secret (`EffectJournal`'s secret, `internal/core/lifecycle/effects.go`), never into the effect document.

Each module keeps its own unexported helper. No shared package is added.

### NPM forward-host refusals

Used by Tasks 8, 9, 1.

- Task 8: outside the `npm.admin.expose` composite's parent, `CheckUpstream` refuses with `npm.reserved` every port of NPM's own container names, its addresses (`r.NPMAddrs`) and `darkpanel-npm`. Port 81 under that parent stays allowed.
- Task 9: the agent resolves a forward host that is not an IP literal with the host's resolver (`net.Resolver.LookupNetIP`, network `ip`), at plan and again at apply. A test replaces the resolver through a seam on the NPM module's dependencies.
  - Every answer runs the address branch of `CheckUpstream`. A reserved answer refuses with `npm.reserved`.
  - A name with no answer refuses with `request.invalid_input`, on field `forwardHost`.
  - The sorted answer set is stored in the plan. At apply, a set that differs from it refuses with `plan.state_changed`.
  - `npm.proxyhost.add` and `npm.proxyhost.update` both apply this rule.

### Probe errors

Used by Tasks 16, 1.

`ProbeResult.Error` of `derp.node.probe` (`internal/modules/derp/wire.go`) is `""` when the probe succeeded. Otherwise it is exactly one of: `timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`, `certificate_not_trusted`, `pin_mismatch`, `bad_answer`, `no_stun_answer`, `failed`. `failed` covers every error the other values do not name. The raw error is logged on the agent at debug level only, and never returned.

### Compose failures

Used by Tasks 5, 1.

A failed `docker compose config` or run returns, and journals, exactly `docker compose <verb> failed with exit status <n>; run "docker compose -f <file> <verb>" on the host to see why`. `<verb>` is `config` or the run's verb, and `<file>` is the project's file path. No byte of Compose's stderr reaches an error, a change record or a log line.

### Login locks

Used by Tasks 17, 1.

- The per-source lock is unchanged.
- The per-username lock becomes a lock on the pair (username, source key). The source key is the client's IPv4 address, or the /64 that holds its IPv6 address.
- Behind a runtime listener, the source key comes from the client address the listener parsed (`core.Proxied.Client`), never from the audit source text `<client> via <proxy>`. That text stays as it is in audit rows.

### Composite read-step outputs

Used by Task 7.

- `completeComposite` (`internal/core/lifecycle/composite_run.go`) passes the redacted output of every step to the composer's `Output`, read steps included, keyed by step id.
- `RegionChanged` and `ServerRemoved` (`internal/modules/derp/wire.go`) each gain `Warnings []string \`json:"warnings"\``. It is `[]`, never `null`, and carries the `warnings` of the composite's `edits` step.

### Internet cells

Used by Tasks 20, 21.

Package `eval` (`internal/modules/firewall/eval`) gains:

```go
// Cell is one range of the internet address space that every address
// prefix of a ruleset either wholly contains or wholly excludes.
type Cell struct {
	From, To netip.Addr // inclusive bounds
	Rep      netip.Addr // the address the guard evaluates for the cell
}

// InternetCells partitions the public unicast space of one family by
// every address prefix any rule of rs matches on.
func InternetCells(rs *netfilter.Ruleset, v6 bool) []Cell
```

- The public unicast space is IPv4 `0.0.0.0/0`, or IPv6 `2000::/3`, minus every address that is not `IsGlobalUnicast()`, that `IsPrivate()`, that is CGNAT `100.64.0.0/10`, or that is a documentation range (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`).
- Cells come back sorted by `From`. They never overlap, and together they cover the whole space, which always yields at least one cell.
- `Rep` is the lowest public unicast address of the cell.
- Task 21 replaces `internet4` and `internet6` in `internal/modules/firewall/guard/flows.go`. A flow that stood in "any internet address" is evaluated once per cell, and holds only when every cell's evaluation lets it through. The guard's refusal names the first blocked cell as `<From>-<To>`.

## Assumptions (evidence)

- Every wave of the spec but 10b is planned and merged: `docs/dforge/plans/completed.md` ends with `2026-11-06-v1-w10a-known-fixes-threat-model-runbook.md`, merged at 47c6db8 (read 2026-11-06).
- The R73 review ran on 2026-11-06 against `main` at dc91aa2, as ruling W10-R1 orders: Fable as `dforge:judge-fable`, Codex `gpt-6-astra` at high effort through `codex exec` (codex-cli 0.163.0 answered a ping the same day). Both used the brief `docs/dforge/notes/v1-security-review-brief.md`. The Codex-only findings were verified by `dforge:judge-opus` the same day, and its verdicts are in the register's Note cells. Result: 0 critical, 7 high, 8 medium, 8 low (`docs/dforge/registers/2026-11-06-v1-security-review.md`).
- Owner decisions on 2026-11-06 (multiple-choice prompt):
  - medium findings: fix all eight (rows 8-15);
  - low findings: accept rows 16, 17, 18 and 20, and correct the threat-model claims of rows 19, 21, 22 and 23, with no code change;
  - forward hosts: resolve at plan and at apply (row 2);
  - leftovers: fix rows 436, 389 and 435, deferring 435's threat-model path-test limitation.
- `make release-check VERSION=1.0.0-rc.1` exits 0 on `main` at dc91aa2 (run 2026-11-06), so no task changes it; Task 4 runs it again.
- `git-cliff` is not installed on this host (`command -v git-cliff` printed nothing on 2026-11-06). `CHANGELOG.md` does not exist yet. `make release-prep` writes it and freezes `schemas/released/v<version>/` (`Makefile`, target `release-prep`; `release/README.md`, "Cutting a release" step 1).
- No tagged `golang.org/x/crypto` release carries the GO-2026-5932 fix: `go list -m -versions golang.org/x/crypto` on 2026-11-06 ends at `v0.58.0`, the version `go.mod` pins. Row v1-completion#429 is deferred as accepted (security-review#17).
- Row v1-completion#389 recurred after the w10a firewall lock merged. Harness run 42231106496 (2026-11-06, branch `worktree-v1-w10a-known-fixes`, ubuntu-22.04) failed `TestM3Docker/traffic-recreate-under-scan` with `IPv6 probe: harness: port 18209 connected in 3 of 23 attempts` (`m3_docker_traffic_test.go:519`). The next run, 42237150792, passed.
- `core.Env` carries `Sealer` with `Derive(label string) []byte` on both roles (`internal/core/contract.go:124-138, 620-625`). The auth service already derives its CSRF key that way (`internal/controller/auth/service.go:87`).
- `completeComposite` skips read steps when it builds the composer's outputs (`internal/core/lifecycle/composite_run.go:977`). The spec says so too (spec line 621: "read steps are not among them"). Read-step outputs are sealed into their step rows, and `stepInput` opens them for references (`composite_run.go:603-622`). The DERP region step is `tailnet.policy.patch`, another module's action (`internal/modules/derp/composite_region.go:42-49`), so the warning cannot ride on that step's output.
- `core.ProxyPath.Public` exists (`internal/core/guard.go:47`), and dispatch copies it (`internal/controller/dispatch/guard.go:55`). `RuntimeHandler` never sets it (`internal/controller/api/runtime.go:30`), and the guard's flow 3 reads it (`internal/modules/firewall/guard/flows.go:238-260`).
- The guard's stand-ins are `198.51.100.1` and `2001:db8:ffff::1` (`internal/modules/firewall/guard/flows.go:28-31`), a deliberate v1-w1 choice (verifier, w1 plan lines 397-415). The owner chose to replace it.
- Existing records: 0.x does not upgrade to 1.0 (spec 12 item 4), so no task migrates plaintext labels or configurations already stored in a 0.x database.

**Plan rulings** (Task 1 copies them into the spec):

- **W10b-R1 review result.** The R73 review ran on 2026-11-06. Its 23 findings are in `docs/dforge/registers/2026-11-06-v1-security-review.md`: 7 high, 8 medium, 8 low, none critical. Every high and medium finding is fixed in `w10b`. The owner accepted four low findings: NPM's ports inside its container, the `openpgp` advisory, the absent Origin header, and the global enrollment budget. The four other low findings are inaccurate threat-model claims, which are corrected. The two reviewers re-review the high findings before `w10b` merges.
- **W10b-R2 NPM self-targets.** Outside the `npm.admin.expose` composite, a proxy host may not forward to any port of NPM's own container names or addresses. NPM's backend API listens on 3000 on every interface of its container.
- **W10b-R3 forward hosts.** A forward host that is a DNS name is resolved on the agent's host at plan and again at apply. Each answer is checked as an address. No answer is invalid input, and answers that changed between plan and apply are `plan.state_changed`. A name that is re-pointed after apply is a residual risk, which the threat model records.
- **W10b-R4 container inspection.** Every element of a container's `Cmd` is masked. An element of `Entrypoint` keeps its existing rule.
- **W10b-R5 effect documents.** An effect document holds a value it only compares as a keyed fingerprint (`hmac-sha256:` over a key derived from the role key). A value it restores lives in the effect's sealed secret. This covers NPM proxy-host configurations, custom locations included, and Docker network and volume labels.
- **W10b-R6 Docker labels.** Network and volume label values are secret inputs. A plan diff shows a label's key and whether it is added, changed or removed, never its value.
- **W10b-R7 NPM update.** `npm.proxyhost.update` keeps the advanced configuration the operator wrote in NPM, unless its input sets one.
- **W10b-R8 NPM answers.** The NPM API client keeps only the status and an allowlisted error code of an NPM answer. NPM's message text never reaches an error, its cause, a log line or a change record.
- **W10b-R9 NPM bootstrap.** When the first-admin creation finds an admin it did not create, the deploy stops and removes the container and the data volumes this deploy created before it fails.
- **W10b-R10 DERP files.** The agent reads DERP certificate and key files through the platform's descriptor-based reads: no symlink anywhere on the path, a regular file only, at most 1 MiB.
- **W10b-R11 DERP probe.** `derp.node.probe` needs `operate`. It refuses RFC 1918, CGNAT (`100.64.0.0/10`) and ULA (`fc00::/7`) targets, both as literals and among resolved answers, after unmapping IPv4-mapped IPv6 addresses. A failed probe reports one value of a fixed vocabulary: `timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`, `certificate_not_trusted`, `pin_mismatch`, `bad_answer`, `no_stun_answer` or `failed`.
- **W10b-R12 lockout guard and logins.**
  - The panel exposure's public bindings come from the exposure's own record, and a request through an NPM exposure without them fails closed.
  - "Any internet address" is evaluated once per cell of the public unicast space that the ruleset's prefixes cut, and holds only when every cell passes.
  - The per-username login lock is keyed by username and source (IPv4 address, IPv6 /64). Behind an exposure, the source is the client address the listener parsed.
- **W10b-R13 changelog.** `CHANGELOG.md` is written by `make release-prep` from the conventional commits when the owner cuts `v1.0.0-rc.1` (`release/README.md`, "Cutting a release"; owner checklist item 3). No plan hand-writes it.
- **W10b-R14 Compose diagnostics.** A failed Compose config or run reports its exit status and the command to run on the host, never Compose's stderr.

## Task index

1. Record the wave 10b rulings in the spec
2. Steady the two flaky tests
3. Keep NPM lists and the expose loader on a failed refetch
4. Document the 0.x to 1.0 reinstall boundary
5. Report Compose failures without their diagnostics
6. Roll back the Tailscale repository file on its own context
7. Carry read-step outputs and the DERP grant warning
8. Reserve every port of NPM's own names and addresses
9. Resolve NPM forward hosts at plan and at apply
10. Fingerprint NPM configurations in effects and keep advanced config
11. Drop NPM's answer text from errors and logs
12. Remove the NPM instance after a bootstrap conflict
13. Mask every command argument in container inspection
14. Keep Docker label values out of plans, inputs and effects
15. Read DERP service files without following links
16. Refuse private DERP probe targets and classify probe errors
17. Key login locks by user and source prefix
18. Record the public binding of an exposure's listener
19. Protect the public leg of an NPM panel exposure
20. Partition the internet address space by the ruleset's prefixes
21. Check internet flows against every partition cell
22. Set backup file modes through the open descriptor
23. Clear the later-step definite mark of nm.run
24. Diagnose and fix the IPv6 recreate-under-scan flake
25. Update the threat model to the reviewed state

---

### Task 1: Record the wave 10b rulings in the spec

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md`, in four places:
  - the `Composer` interface comment in section 1.3 (the line `// outputs, keyed by step id; read steps are not among them.`, about line 621);
  - the "Read steps" bullet of section 1.3 (about line 719);
  - the "Release candidate" bullet of section 10 (about line 5302);
  - a new "Plan rulings (wave 10b)" bullet at the end of section 10, after the last sub-bullet of the "Plan rulings (wave 10a)" bullet (`W10-R14`), before the heading `### 11. Error codes, actors and dependencies`.

**Interfaces:**
- Consumes: Contracts "Composite read-step outputs" (the first sentence is the new meaning of the `Composer` comment) and the ruling W10b-R13 (the new text of the "Release candidate" bullet). The wave 10b rulings W10b-R1 to W10b-R14 are the bullets of the plan's Assumptions under "Plan rulings"; this task copies them word for word, and the text to insert is given in full in Step 3.
- Produces: the spec text the later tasks and reviews cite as W10b-R1 to W10b-R14. No code, no name.

**Depends on:** none
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

This task has no Go or web test. The spec is not read by any test of the repository, so Step 1 is a check script that fails on the current tree and passes once the four edits are made. Run the commands with `mkdir -p /root/.cache/w10b-tmp/1 && export TMPDIR=/root/.cache/w10b-tmp/1 GOTOOLCHAIN=local`.

- [ ] **Step 1: Write the failing check**

Write this file to `$TMPDIR/spec-check.py`. It is a scratch file: do not commit it. It compares the spec with the plan's rulings and with the three amended passages.

```python
import re
import sys

SPEC, PLAN = sys.argv[1], sys.argv[2]


def norm(text):
    return " ".join(text.split())


spec = open(SPEC, encoding="utf-8").read()
plan = open(PLAN, encoding="utf-8").read().split("\n")
first = next(k for k, line in enumerate(plan) if line.startswith("- **W10b-R1 "))
last = next(k for k, line in enumerate(plan) if line.startswith("## Task index"))
rulings = norm(" ".join(line for line in plan[first:last] if line.strip()))
flat = norm(spec)
failures = []
if rulings not in flat:
    failures.append("the spec does not hold the rulings W10b-R1 to W10b-R14 of the plan, word for word")
n = len(re.findall(r"^  - \*\*W10b-R\d+ ", spec, flags=re.M))
if n != 14:
    failures.append(f"the spec has {n} lines that start a W10b ruling, want 14")
a = spec.find("- **Plan rulings (wave 10a).**")
b = spec.find("- **Plan rulings (wave 10b).**")
c = spec.find("### 11. Error codes, actors and dependencies")
if not (0 <= a < b < c):
    failures.append("the wave 10b bullet is not between the wave 10a bullet and section 11")
for text, why in [
    ("read steps are not among them", "line 621 still says read steps are not among the outputs"),
    ("never appears in the composite's output", "the Read steps bullet still says the result never appears in the composite's output"),
    ("`CHANGELOG.md` and `docs/versioning.md` updated", "the Release candidate bullet still says CHANGELOG.md is updated by hand"),
]:
    if text in flat:
        failures.append(why)
for text, why in [
    ("outputs, keyed by step id; read steps are included.", "line 621 does not say read steps are included"),
    ("The composer's `Output` receives its redacted form with every other step's, and the composite's output carries only what that composer builds from it.", "the Read steps bullet does not say the composer receives the output"),
    ("- **Release candidate.** `docs/versioning.md` updated (0.x → 1.0 is a reinstall); `CHANGELOG.md` written by `make release-prep` from the conventional commits when the owner cuts the tag (W10b-R13); `make release-check` green; the commit ready to tag `v1.0.0-rc.1`. Tagging is the owner's (section 12).", "the Release candidate bullet is not amended"),
]:
    if text not in flat:
        failures.append(why)
for f in failures:
    print("FAIL:", f)
if failures:
    sys.exit(1)
print("spec check ok")
```

- [ ] **Step 2: Run the check to verify it fails**

Run: `python3 -I "$TMPDIR/spec-check.py" docs/dforge/specs/2026-10-21-v1-completion-design.md docs/dforge/plans/2026-11-06-v1-w10b-security-fixes-rc.md; echo exit $?`
Expected: nine lines starting `FAIL:`, one for each condition above, then `exit 1`.

- [ ] **Step 3: Edit the spec**

Edit `docs/dforge/specs/2026-10-21-v1-completion-design.md` in these four places. Keep every other line as it is, the wave 10a rulings and `W10-R1` included.

1. In the `Composer` interface of section 1.3, change the second comment line of `Output` to this, so the two lines read:

   ```text
         // Output builds the composite's output from its steps' redacted
         // outputs, keyed by step id; read steps are included.
   ```

2. Replace the "Read steps" bullet of section 1.3 (it starts `- **Read steps.** Executed by the controller's read dispatch`, and ends `unwind skips read steps.`) with this bullet. Only the sentences about the result and the composer change; the rest is as before:

   ```text
   - **Read steps.** Executed by the controller's read dispatch (for an
     `Internal` read, 1.7's controller-only path) when the cursor reaches
     them; no plan, no change, no effects. The result is sealed into the step
     row and never appears in the lookup, audit or the API. The composer's
     `Output` receives its redacted form with every other step's, and the
     composite's output carries only what that composer builds from it. A read
     step that is not yet `read` is read again when the cursor reaches it;
     unwind skips read steps.
   ```

3. Replace the "Release candidate" bullet of section 10 (it starts `- **Release candidate.**` and ends `Tagging is the owner's (section 12).`) with:

   ```text
   - **Release candidate.** `docs/versioning.md` updated (0.x → 1.0 is a
     reinstall); `CHANGELOG.md` written by `make release-prep` from the
     conventional commits when the owner cuts the tag (W10b-R13);
     `make release-check` green; the commit ready to tag `v1.0.0-rc.1`.
     Tagging is the owner's (section 12).
   ```

4. After the last line of the `W10-R14` sub-bullet (`with NPM's own procedure, which NPM documents.`) add one blank line and this bullet, at the same indentation as the "Plan rulings (wave 10a)" bullet. The line breaks are free; the words must match the plan's Assumptions exactly, because the check compares them:

   ```text
   - **Plan rulings (wave 10b).** Wave 10b (plan
     `v1-w10b-security-fixes-rc`) settles these gaps and underdetermined points
     of the text above:
     - **W10b-R1 review result.** The R73 review ran on 2026-11-06. Its 23
       findings are in
       `docs/dforge/registers/2026-11-06-v1-security-review.md`: 7 high, 8
       medium, 8 low, none critical. Every high and medium finding is fixed in
       `w10b`. The owner accepted four low findings: NPM's ports inside its
       container, the `openpgp` advisory, the absent Origin header, and the
       global enrollment budget. The four other low findings are inaccurate
       threat-model claims, which are corrected. The two reviewers re-review
       the high findings before `w10b` merges.
     - **W10b-R2 NPM self-targets.** Outside the `npm.admin.expose` composite,
       a proxy host may not forward to any port of NPM's own container names or
       addresses. NPM's backend API listens on 3000 on every interface of its
       container.
     - **W10b-R3 forward hosts.** A forward host that is a DNS name is resolved
       on the agent's host at plan and again at apply. Each answer is checked
       as an address. No answer is invalid input, and answers that changed
       between plan and apply are `plan.state_changed`. A name that is
       re-pointed after apply is a residual risk, which the threat model
       records.
     - **W10b-R4 container inspection.** Every element of a container's `Cmd`
       is masked. An element of `Entrypoint` keeps its existing rule.
     - **W10b-R5 effect documents.** An effect document holds a value it only
       compares as a keyed fingerprint (`hmac-sha256:` over a key derived from
       the role key). A value it restores lives in the effect's sealed secret.
       This covers NPM proxy-host configurations, custom locations included,
       and Docker network and volume labels.
     - **W10b-R6 Docker labels.** Network and volume label values are secret
       inputs. A plan diff shows a label's key and whether it is added, changed
       or removed, never its value.
     - **W10b-R7 NPM update.** `npm.proxyhost.update` keeps the advanced
       configuration the operator wrote in NPM, unless its input sets one.
     - **W10b-R8 NPM answers.** The NPM API client keeps only the status and an
       allowlisted error code of an NPM answer. NPM's message text never
       reaches an error, its cause, a log line or a change record.
     - **W10b-R9 NPM bootstrap.** When the first-admin creation finds an admin
       it did not create, the deploy stops and removes the container and the
       data volumes this deploy created before it fails.
     - **W10b-R10 DERP files.** The agent reads DERP certificate and key files
       through the platform's descriptor-based reads: no symlink anywhere on
       the path, a regular file only, at most 1 MiB.
     - **W10b-R11 DERP probe.** `derp.node.probe` needs `operate`. It refuses
       RFC 1918, CGNAT (`100.64.0.0/10`) and ULA (`fc00::/7`) targets, both as
       literals and among resolved answers, after unmapping IPv4-mapped IPv6
       addresses. A failed probe reports one value of a fixed vocabulary:
       `timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`,
       `certificate_not_trusted`, `pin_mismatch`, `bad_answer`,
       `no_stun_answer` or `failed`.
     - **W10b-R12 lockout guard and logins.**
       - The panel exposure's public bindings come from the exposure's own
         record, and a request through an NPM exposure without them fails
         closed.
       - "Any internet address" is evaluated once per cell of the public
         unicast space that the ruleset's prefixes cut, and holds only when
         every cell passes.
       - The per-username login lock is keyed by username and source (IPv4
         address, IPv6 /64). Behind an exposure, the source is the client
         address the listener parsed.
     - **W10b-R13 changelog.** `CHANGELOG.md` is written by `make release-prep`
       from the conventional commits when the owner cuts `v1.0.0-rc.1`
       (`release/README.md`, "Cutting a release"; owner checklist item 3). No
       plan hand-writes it.
     - **W10b-R14 Compose diagnostics.** A failed Compose config or run reports
       its exit status and the command to run on the host, never Compose's
       stderr.
   ```

   Indent the block to match the "Plan rulings (wave 10a)" bullet: the bullet starts at column 0 and its rulings at two spaces, as in the text above.

- [ ] **Step 4: Run the check to verify it passes**

Run: `python3 -I "$TMPDIR/spec-check.py" docs/dforge/specs/2026-10-21-v1-completion-design.md docs/dforge/plans/2026-11-06-v1-w10b-security-fixes-rc.md`
Expected: `spec check ok`.

Then run these. The first prints `14`, the second and third print nothing and exit 1, and the tests print `ok`:

```sh
rg -c '^  - \*\*W10b-R[0-9]+ ' docs/dforge/specs/2026-10-21-v1-completion-design.md
rg -n 'read steps are not among them' docs/dforge/specs/2026-10-21-v1-completion-design.md
rg -n 'CHANGELOG.md. and .docs/versioning.md. updated' docs/dforge/specs/2026-10-21-v1-completion-design.md
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/docs/
git diff --stat
```

`git diff --stat` lists only the spec file. Then `rm -rf "$TMPDIR"` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): record the wave 10b plan rulings"
```

### Task 2: Steady the two flaky tests

**Files:**
- Modify (test only): `internal/modules/derp/regions_w10_test.go` (the helper `w10rgDrive`, line 20, and the test `TestW10RgPassReadsHostsTogetherAndBoundsEachRead`, line 194)
- Modify (test only): `internal/controller/api/extensions_test.go` (the helper `xrEvents` and the test `TestExtensionStreamEndFrame`, lines 500-574)
- Read: the register row `v1-completion#436` in `docs/dforge/registers/2026-10-21-v1-completion.md` (row 436), which names both tests. Do not edit the register.

**Interfaces:**
- Consumes: nothing from earlier tasks. The helpers `srWorld`, `srDeps`, `srPut`, `srLoad` (`internal/modules/derp/servers_test.go`), `xrHarness`, `pxProxied`, `sseWait`, `newSSERecorder` (`internal/controller/api`) are used as they are.
- Produces: nothing for other tasks. No production file changes. The test-local names are `v1r02AnswerLag` (derp) and `v1r02StartLag`, `v1r02Wait` (api).

**Items:** 2026-10-21-v1-completion#436

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

Both fixes are to the tests' timing. The production code is right in both cases, so no production file changes. Run the commands with `mkdir -p /root/.cache/w10b-tmp/2 && export TMPDIR=/root/.cache/w10b-tmp/2 GOTOOLCHAIN=local`.

**Why the two tests fail.**
- `TestW10RgPassReadsHostsTogetherAndBoundsEachRead`: `w10rgDrive` advances the fake clock by one second, with no pause, as soon as all three reads have started. Fifteen advances take microseconds. If the reads of h2 and h3 have not returned by then (the race detector and a busy host make that likely), the bound of 15 s (`serversReadWait`) expires and cancels them. The pass then stores no record of h2, and the test fails. The bound of a read that answers must not expire while it answers.
- `TestExtensionStreamEndFrame`: the test ends the stream as soon as the stream's two timers are armed. The timers are armed by the watcher goroutine, which starts before the handler runs. A handler that starts late (under load) then finds its context already ended: `deadlineWriter.WriteHeader` writes nothing once the context has ended, so the response has no `text/event-stream` header, and a stream without it gets no end frame, as it should. The test must end the stream only after the handler has written its first frame.

- [ ] **Step 1: Extend the test `internal/modules/derp/regions_w10_test.go::TestW10RgPassReadsHostsTogetherAndBoundsEachRead`**

Make the agents of h2 and h3 answer after 20 ms of real time, as a busy agent does, still honouring their context. Add this constant above the comment of `w10rgDrive`:

```go
// v1r02AnswerLag is how long the agents that answer take to do so, in real time.
const v1r02AnswerLag = 20 * time.Millisecond
```

In the test, the `ReadFunc` of each host ends with `return json.Marshal(answers[id])`. Put this before that line, so the end of the function reads:

```go
			if id == "h1" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			select {
			case <-time.After(v1r02AnswerLag):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return json.Marshal(answers[id])
		}}
```

Leave the driver and the rest of the test as they are for now.

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test -race -count=1 -run '^TestW10RgPassReadsHostsTogetherAndBoundsEachRead$' ./internal/modules/derp/`
Expected: FAIL with `regions_w10_test.go:<line>: no record of h2, whose read answered at once`. The driver advanced the clock past h2's bound while h2's agent was still answering.

- [ ] **Step 3: Extend the test `internal/controller/api/extensions_test.go::TestExtensionStreamEndFrame`**

Make the handler start late. Add this constant above `xrEvents`:

```go
// v1r02StartLag is how long xrEvents waits before it writes its response header, as a handler that starts late
// does under load.
const v1r02StartLag = 30 * time.Millisecond
```

and make the first statement of the function that `xrEvents` returns:

```go
		time.Sleep(v1r02StartLag)
```

Change nothing else yet.

- [ ] **Step 4: Run the test to verify it fails**

Run: `timeout 300 go test -race -count=1 -run '^TestExtensionStreamEndFrame$' ./internal/controller/api/`
Expected: FAIL, four of the five subtests: `revoked`, `hard limit` and `CloseStreams` with `body ends "" with 0 end frames, want one end frame "event: end\ndata: {\"reason\":...`, and `its own end frame` with `body "": the wrapper added an end frame`. `not server-sent events` passes.

- [ ] **Step 5: Fix the derp driver and its test**

In `internal/modules/derp/regions_w10_test.go`:

1. Replace `w10rgDrive` and its comment with this. The change is the pause after each `Advance`, as `rcDrive` in `binary_update_recover_test.go` has:

```go
// w10rgDrive runs call and, once ready reports true, advances the host's clock by one second whenever a timer
// waits on it, pausing after each step. Advancing before ready would let a bound expire before the goroutines
// under test have run at all.
func w10rgDrive(t *testing.T, h *dpHost, ready func() bool, call func()) {
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
		isReady := ready()
		if isReady && h.clock.Pending() > 0 {
			h.clock.Advance(time.Second)
			// A bound stays armed while the code under test does real work; pacing keeps the clock from passing it during one step.
			time.Sleep(2 * time.Millisecond)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			if !isReady {
				t.Fatal("the pass never reached the state the clock waits for")
			}
			t.Fatal("the pass did not finish")
		}
	}
}
```

2. In the test, replace the call `w10rgDrive(t, h, allStarted, ...)` and the lines before it that define `allStarted` with the following. The clock may move only when h1's bound is the one still armed, because the reads of h2 and h3 stop their bounds when they end:

```go
	allStarted := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(started) == len(hosts)
	}
	// The reads of h2 and h3 stop their bounds when they end, so once only h1's bound is armed, the clock may move.
	armed := h.clock.Pending()
	onlyH1Waits := func() bool { return allStarted() && h.clock.Pending() == armed+1 }
	w10rgDrive(t, h, onlyH1Waits, func() { err = h.dp().checkServers(context.Background(), deps, h.clock.Now()) })
```

Keep the existing comment above `allStarted` (the one that says a pass reading the hosts one at a time never moves the clock). `armed` is read before the pass starts, so it counts the timers that already exist.

3. Replace the assertion on the record of h2 (`if _, ok := srLoad(t, h, "h2"); !ok {` with its `t.Error`), so that it no longer says the read answered at once and also requires that the record carries no error:

```go
	if r, ok := srLoad(t, h, "h2"); !ok || r.Error != "" {
		t.Errorf("record of h2 = %+v, %v; want a record without an error, since its read answered", r, ok)
	}
```

- [ ] **Step 6: Fix the api test's timing**

In `internal/controller/api/extensions_test.go`, replace the constant of Step 3, `xrEvents` and `TestExtensionStreamEndFrame` (everything from the comment of `xrEvents` up to the comment of `xrFlushFails`) with this. The handler now closes `began` after its first frame, and the test waits for it before it ends the stream. The wait bound is 30 s, which only a failing run reaches:

```go
// v1r02StartLag is how long xrEvents waits before it writes its response header, as a handler that starts late
// does under load. v1r02Wait bounds each wait for something that must happen; only a failing run reaches it.
const (
	v1r02StartLag = 30 * time.Millisecond
	v1r02Wait     = 30 * time.Second
)

// xrEvents is a server-sent event stream handler that ignores its context:
// after v1r02StartLag it writes a frame every millisecond until a write
// fails, sends that error on failed, and, with ownEnd, first writes an end
// frame of its own. It closes began once its first frame was written.
func xrEvents(contentType string, ownEnd bool, began chan<- struct{}, failed chan<- error) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(v1r02StartLag)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if ownEnd {
			_, _ = w.Write([]byte("event: end\ndata: {\"reason\":\"done\"}\n\n"))
		}
		first := true
		for {
			if _, err := w.Write([]byte("event: data\ndata: {}\n\n")); err != nil {
				failed <- err
				return
			}
			if first {
				first = false
				close(began)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// TestExtensionStreamEndFrame: once a contributed stream must end, the
// handler's writes fail even when it ignores its context, and a
// server-sent event stream gets one end frame naming why, unless it wrote
// its own. The stream is ended only after the handler has written its first
// frame, because a stream that ends before the response header gets no end
// frame.
func TestExtensionStreamEndFrame(t *testing.T) {
	const end = "event: end\ndata: {\"reason\":"
	for _, tc := range []struct {
		name        string
		contentType string
		ownEnd      bool
		end         func(h *harness, revoked chan struct{})
		want        string // the start of the end frame the wrapper adds, "" for none
	}{
		{"revoked", "text/event-stream", false, func(_ *harness, revoked chan struct{}) { close(revoked) }, end + "\"revoked\"}\n\n"},
		{"hard limit", "text/event-stream", false, func(h *harness, _ chan struct{}) { h.clock.Advance(SSEHardLimit) }, end + "\"expired\"}\n\n"},
		{"CloseStreams", "text/event-stream", false, func(h *harness, _ chan struct{}) { h.srv.CloseStreams() }, end + "\"error\",\"error\":{\"code\":\"internal\""},
		{"its own end frame", "text/event-stream", true, func(_ *harness, revoked chan struct{}) { close(revoked) }, ""},
		{"not server-sent events", "application/x-ndjson", false, func(_ *harness, revoked chan struct{}) { close(revoked) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			began := make(chan struct{})
			failed := make(chan error, 1)
			h := xrHarness(t, ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/follow", Scope: core.ScopeRead,
				Stream: true, Handler: xrEvents(tc.contentType, tc.ownEnd, began, failed)})
			revoked := make(chan struct{})
			r := withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead))
			r = r.WithContext(core.WithProxied(r.Context(), pxProxied(revoked)))
			rec := newSSERecorder()
			base := h.clock.Pending()
			done := make(chan struct{})
			go func() {
				h.srv.Handler().ServeHTTP(rec, r)
				close(done)
			}()
			select {
			case <-began:
			case err := <-failed:
				t.Fatalf("the handler's write failed with %v before its first frame, so the stream ended before the test ended it", err)
			case <-time.After(v1r02Wait):
				t.Fatal("the handler never wrote its first frame")
			}
			sseWait(t, "the stream's timers", func() bool { return h.clock.Pending() >= base+2 })
			tc.end(h, revoked)
			select {
			case err := <-failed:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("the handler's write failed with %v, want context.Canceled", err)
				}
			case <-time.After(v1r02Wait):
				t.Fatal("the handler kept writing after its stream ended")
			}
			<-done
			body := rec.text()
			last := body[max(0, strings.LastIndex(body, "event: end\n")):]
			if n := strings.Count(body, "event: end\n"); tc.want != "" && (n != 1 || !strings.HasPrefix(last, tc.want)) {
				t.Errorf("body ends %q with %d end frames, want one end frame %q", last, n, tc.want)
			}
			if tc.want == "" && strings.Count(body, "event: end\n") != map[bool]int{true: 1, false: 0}[tc.ownEnd] {
				t.Errorf("body %q: the wrapper added an end frame", body[max(0, len(body)-60):])
			}
		})
	}
}
```

- [ ] **Step 7: Run both tests 100 times under the race detector**

The derp run takes about 95 s and the api run about 5 minutes on this host, so run each as a background command. Each prints the number of runs that passed:

```sh
set -o pipefail
timeout 900 go test -race -count=100 -v -run '^TestW10RgPassReadsHostsTogetherAndBoundsEachRead$' ./internal/modules/derp/ | rg -c '^--- PASS: TestW10RgPass'
timeout 900 go test -race -count=100 -v -run '^TestExtensionStreamEndFrame$' ./internal/controller/api/ | rg -c '^--- PASS: TestExtensionStreamEndFrame '
```

Expected: `100` for each command, and no `--- FAIL` line. Record both outputs.

Then run the constraint checks for the two packages:

```sh
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -race -count=1 ./internal/modules/derp/ ./internal/controller/api/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test line `ok` or `?`, `0 issues.` from the linter, and no output from `go vet` and `go mod tidy -diff`. Then `rm -rf "$TMPDIR"` and `go clean -testcache`.

- [ ] **Step 8: Commit**

```bash
git add internal/modules/derp/regions_w10_test.go
git commit -m "test(derp): pace the fake clock of the region test"
git add internal/controller/api/extensions_test.go
git commit -m "test(api): end the stream after the first frame"
```

### Task 3: Keep NPM lists and the expose loader on a failed refetch

**Files:**
- Modify: `web/src/modules/npm/list-parts.tsx` (add the exported component `ReadFailure`)
- Modify: `web/src/modules/npm/proxy-hosts-tab.tsx`, `web/src/modules/npm/access-lists-tab.tsx`, `web/src/modules/npm/certificates-tab.tsx` (the `content` branch after `useQuery`)
- Modify: `web/src/modules/npm/container-actions.tsx` (`ExposeLoader` and `ExposeAction`)
- Test: `web/src/modules/npm/proxy-hosts-tab.test.tsx`, `web/src/modules/npm/access-lists-tab.test.tsx`, `web/src/modules/npm/certificates-tab.test.tsx`, `web/src/modules/npm/container-actions.test.tsx`
- Read: `docs/dforge/notes/v1-w10a-known-fixes-minors.md`, line 111 (the hazard this task fixes: "the NPM list tabs and `ExposeLoader` do not keep data when a refetch fails"). Do not edit the note.
- Not edited: `web/src/modules/npm/npm-page.tsx` (its warning reads `status.data`, which React Query keeps after a failed refetch, and the Instance panel already keeps its data) and `web/src/modules/npm/list-replies.ts` (the readers only validate and reject).

**Interfaces:**
- Consumes: `messageOf` (`web/src/modules/npm/status.ts`), `npmKey` and `dockerKey` (the query keys the tests invalidate), the `renderWithProviders` and `stubFetch` helpers of `web/src/test/utils.tsx`. It returns the `QueryClient` as `client`.
- Produces: `ReadFailure({ error }: { error: unknown })`, exported from `list-parts.tsx`, used only inside this task. It renders the destructive alert with `role="alert"` that the tabs show today.

**Items:** 2026-10-21-v1-completion#435

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

Row 435 lists five hazards. This task fixes the NPM list tabs and the `ExposeLoader` one; the cloud repository file, `nm.run`, the backup `chmod` and the path-test limitation belong to other tasks or are deferred by the owner.

**The defect.** React Query keeps `data` when a refetch fails: the query's status becomes `error` and `data` is still the last good value. Each tab tests `query.isError` before it looks at `data`, so a failed refetch replaces the table with the alert, and `ExposeLoader` replaces the whole expose form with the alert, which loses what the operator typed. A tab refetches when it is opened again, and any module event refetches too. After this task a panel that has data keeps showing it, with the alert above it; a panel without data shows the alert alone, as before.

Run the commands with `mkdir -p /root/.cache/w10b-tmp/3 && export TMPDIR=/root/.cache/w10b-tmp/3 GOTOOLCHAIN=local`.

- [ ] **Step 1: Write the failing tests**

In `proxy-hosts-tab.test.tsx`, change the import `import type { ProxyHostView } from "./types"` to `import { npmKey, type ProxyHostView } from "./types"`, and append at the end of the file:

```tsx
test("keeps the table and shows the reason when a refetch fails", async () => {
  let failing = false
  const { client } = phRender(phHosts(), {
    [PH_LIST]: () =>
      failing
        ? { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } }
        : { body: { output: { items: phHosts() } } },
  })
  await screen.findByRole("table", { name: "Proxy hosts" })
  failing = true
  await client.invalidateQueries({ queryKey: npmKey("h1", "proxyhost.list") })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
  expect(phCells(1).slice(0, 2)).toEqual(["3", PH_NAMES])
  failing = false
  await client.invalidateQueries({ queryKey: npmKey("h1", "proxyhost.list") })
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
  expect(phCells(1).slice(0, 2)).toEqual(["3", PH_NAMES])
})
```

In `access-lists-tab.test.tsx`, change `import { screen, within } from "@testing-library/react"` to `import { screen, waitFor, within } from "@testing-library/react"` and `import type { AccessListView } from "./types"` to `import { npmKey, type AccessListView } from "./types"`, and append:

```tsx
test("keeps the table and shows the reason when a refetch fails", async () => {
  let failing = false
  const { client } = alRender(alLists(), {
    [AL_LIST]: () =>
      failing
        ? { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } }
        : { body: { output: { items: alLists() } } },
  })
  await screen.findByRole("table", { name: "Access lists" })
  failing = true
  await client.invalidateQueries({ queryKey: npmKey("h1", "accesslist.list") })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
  expect(alCells(1).slice(0, 2)).toEqual(["2", "darkpanel-01jd-1"])
  failing = false
  await client.invalidateQueries({ queryKey: npmKey("h1", "accesslist.list") })
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
  expect(alCells(1).slice(0, 2)).toEqual(["2", "darkpanel-01jd-1"])
})
```

In `certificates-tab.test.tsx`, change `import { screen, within } from "@testing-library/react"` to `import { screen, waitFor, within } from "@testing-library/react"` and `import type { CertificateView } from "./types"` to `import { npmKey, type CertificateView } from "./types"`, and append:

```tsx
test("keeps the table and shows the reason when a refetch fails", async () => {
  let failing = false
  const { client } = ceRender(ceCerts(), {
    [CE_LIST]: () =>
      failing
        ? { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } }
        : { body: { output: { items: ceCerts() } } },
  })
  await screen.findByRole("table", { name: "Certificates" })
  failing = true
  await client.invalidateQueries({ queryKey: npmKey("h1", "certificate.list") })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
  expect(ceCells(1).slice(0, 2)).toEqual(["5", "darkpanel-01jd-3"])
  failing = false
  await client.invalidateQueries({ queryKey: npmKey("h1", "certificate.list") })
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
  expect(ceCells(1).slice(0, 2)).toEqual(["5", "darkpanel-01jd-3"])
})
```

In `container-actions.test.tsx`, change `import type { InstanceStatus } from "./types"` to `import { npmKey, type InstanceStatus } from "./types"`, and append:

```tsx
const CA_DOWN = { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" }

test.each([
  ["the NPM status", CA_STATUS, npmKey("h1", "instance.status"), { body: { output: caStatus() } }],
  ["the container", CA_INSPECT, ["resource", "h1", "docker.container", "c1"], { body: { output: caDetail() } }],
  ["the networks", CA_NETWORKS, dockerKey("h1", "networks"), { body: { networks: caNetworks() } }],
])("keeps the form and what was typed in it when a refetch of %s fails", async (_, route, key, answer) => {
  let failing = false
  const { client } = caRender({ [route]: () => (failing ? { status: 503, body: CA_DOWN } : answer) })
  const user = userEvent.setup()
  const form = await caForm()
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")

  failing = true
  await client.invalidateQueries({ queryKey: key })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
  expect(form.isConnected).toBe(true)
  expect((within(form).getByLabelText("Domains") as HTMLInputElement).value).toBe("app.example.com")

  failing = false
  await client.invalidateQueries({ queryKey: key })
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
  expect(form.isConnected).toBe(true)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 pnpm -C web exec vitest run src/modules/npm`
Expected: FAIL, six tests: `keeps the table and shows the reason when a refetch fails` in each of the three tab files, and the three rows of `keeps the form and what was typed in it when a refetch of %s fails`. In each, the first assertion after the failed refetch fails: for a tab the table is gone, and for the expose form it is no longer in the document. Every other test of the directory still passes.

- [ ] **Step 3: Implement**

1. `list-parts.tsx`: export a component `ReadFailure({ error }: { error: unknown })`. It renders the markup the tabs use today for a failed read: `<Alert variant="destructive" role="alert">` holding `<AlertDescription>` with `messageOf(error)`. Import `Alert` and `AlertDescription` from `darkraise-ui/components/alert` and `messageOf` from `./status`. Give it a doc comment saying that a panel that holds data from an earlier read shows the failure above that data. The file keeps exporting components only, as the react-refresh lint rule needs.
2. `proxy-hosts-tab.tsx`, `access-lists-tab.tsx`, `certificates-tab.tsx`: replace the `isPending` / `isError` / else chain that sets `content` with a branch on `query.data`:
   - `query.data === undefined`: the spinner (same `aria-label`) while `query.isPending`, otherwise `<ReadFailure error={query.error} />`.
   - otherwise a fragment: `{query.isError && <ReadFailure error={query.error} />}` first, then the table exactly as before, with its props and handlers unchanged. The `false` the fragment holds when there is no error keeps the table at the same position in the tree.
   - Remove the imports the change leaves unused (`Alert`, `AlertDescription`, `messageOf`) and import `ReadFailure` from `./list-parts`. Lint fails on an unused import.
3. `container-actions.tsx`, `ExposeLoader`: when `detail.data` or `networks.data` is undefined, return the spinner (same label) if either query is pending, otherwise `<ReadFailure error={detail.error ?? networks.error} />`. When both have data, return a fragment with `{(detail.isError || networks.isError) && <ReadFailure error={detail.error ?? networks.error} />}` first and `ExposeForm` second, with the same props as before. `ExposeForm` must stay at the same position in the tree whether the alert shows or not, so React keeps its state and the operator's draft.
4. `container-actions.tsx`, `ExposeAction`: the same shape for `status`. With no `status.data`: the spinner while pending, otherwise `ReadFailure`. With data: keep the "not deployed" paragraph for `!status.data.deployed`, and otherwise return a fragment with `{status.isError && <ReadFailure error={status.error} />}` first and `ExposeLoader` second. Update the imports of the file: `Field, ReadFailure` from `./list-parts`, and `readInstanceStatus` alone from `./status`; keep the `Alert` import, which the unsupported-network warning still uses.

Change no other component, no query key and no wire type.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run src/modules/npm`
Expected: PASS, every test of the directory (204 at the time of writing, 6 of them new).

Then, with `GOTOOLCHAIN=local` and the explicit timeout of the constraints:

```sh
timeout 1200 make lint-web test-web
timeout 300 go mod tidy -diff
```

Expected: eslint with `--max-warnings 0` and both `tsc --noEmit` runs print no problem, every Vitest file passes, and `go mod tidy -diff` prints nothing. `git status` shows no change to `web/package.json`, `web/pnpm-lock.yaml` or `web/pnpm-workspace.yaml`. Then `rm -rf "$TMPDIR"` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/npm/list-parts.tsx web/src/modules/npm/proxy-hosts-tab.tsx web/src/modules/npm/access-lists-tab.tsx web/src/modules/npm/certificates-tab.tsx web/src/modules/npm/container-actions.tsx web/src/modules/npm/proxy-hosts-tab.test.tsx web/src/modules/npm/access-lists-tab.test.tsx web/src/modules/npm/certificates-tab.test.tsx web/src/modules/npm/container-actions.test.tsx
git commit -m "fix(web): keep NPM data when a refetch fails"
```

### Task 4: Document the 0.x to 1.0 reinstall boundary

**Files:**
- Modify: `docs/versioning.md` (add the section "Upgrade from 0.x" between the sections "Updates" and "Databases")
- Create: `internal/docs/versioning_test.go`
- Read: `internal/update/version.go` (`Steps`, the refusal across a major version, line 183), `docs/user/install.md`, `docs/user/uninstall.md` (the commands the section names).

**Interfaces:**
- Consumes: `update.Steps` and `update.Release` (`internal/update/version.go`), `core.ChannelBeta`, and the helpers of the `internal/docs` tests: `dcRead`, `dcMissing`, `dcLinks`, `duFenceRE`, `duHeadingRE`, `duCodeRE`, `duHTMLRE`, `duPlanRE` (`helpers_test.go`, `user_test.go`).
- Produces: the section "Upgrade from 0.x" of `docs/versioning.md`. The tests `TestV1r04VersioningStatesTheReinstallBoundary` and `TestV1r04VersioningFollowsTheDocumentationStyle` keep it in place. No other task links to it.

**Depends on:** none
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

Spec section 12, item 4: 0.x does not upgrade to 1.0, so moving to a 1.0 release is a reinstall. The updater already refuses it: `update.Steps` answers an update between major versions with `updates from <current> to <target> cross a major version, which is not supported`. The page's existing style tests (`TestDuStyle`, `TestWcDocsLinks`) cover `docs/user`, `docs/dev` and `docs/api` only, not `docs/versioning.md`, so the second test below holds this page to the same style. This task does not write `CHANGELOG.md` and does not edit the `Makefile`: `make release-prep` writes the changelog when the owner cuts the tag. Run the commands with `mkdir -p /root/.cache/w10b-tmp/4 && export TMPDIR=/root/.cache/w10b-tmp/4 GOTOOLCHAIN=local`.

- [ ] **Step 1: Write the failing tests**

Create `internal/docs/versioning_test.go`:

```go
package docs_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/update"
)

const v1r04Page = "docs/versioning.md"

// v1r04Refusal is the part of the updater's refusal across a major version that the page quotes.
const v1r04Refusal = "cross a major version, which is not supported"

// v1r04Section returns the text of the page's "Upgrade from 0.x" section, up to the next section.
func v1r04Section(t *testing.T) string {
	t.Helper()
	const heading = "\n## Upgrade from 0.x\n"
	_, after, ok := strings.Cut(dcRead(t, v1r04Page), heading)
	if !ok {
		t.Fatalf("%s has no section headed %q", v1r04Page, strings.TrimSpace(heading))
	}
	if i := strings.Index(after, "\n## "); i >= 0 {
		after = after[:i]
	}
	return after
}

// TestV1r04VersioningStatesTheReinstallBoundary: the page says that 0.x does not upgrade to 1.0, quotes the
// refusal the updater gives, and names the commands of a reinstall, which exist as the user guide documents them.
func TestV1r04VersioningStatesTheReinstallBoundary(t *testing.T) {
	section := v1r04Section(t)
	for _, name := range dcMissing(section, []string{
		"0.x does not upgrade to 1.0",
		"reinstall",
		v1r04Refusal,
		"sudo darkpanel uninstall",
		"--keep-data",
		"sudo sh install.sh --role controller",
		"darkpanel host create",
		"sudo sh install.sh --role agent",
		"](user/install.md)",
		"](user/uninstall.md)",
	}) {
		t.Errorf("the 0.x section does not mention %q", name)
	}

	_, err := update.Steps("0.9.0", "1.0.0-rc.1", core.ChannelBeta,
		[]update.Release{{Version: "1.0.0-rc.1", Channel: core.ChannelBeta}}, false)
	if err == nil || !strings.Contains(err.Error(), v1r04Refusal) {
		t.Errorf("update.Steps from 0.9.0 to 1.0.0-rc.1 = %v, want a refusal that says %q", err, v1r04Refusal)
	}
}

// TestV1r04VersioningFollowsTheDocumentationStyle: the page, with its new section, keeps one title, tagged code
// blocks, no HTML, no mention of a plan or of work to come, and links that resolve.
func TestV1r04VersioningFollowsTheDocumentationStyle(t *testing.T) {
	v1r04Section(t)
	text := dcRead(t, v1r04Page)
	level, titles, inFence := 0, 0, false
	for i, line := range strings.Split(text, "\n") {
		at := func(msg string) { t.Errorf("%s:%d: %s: %q", v1r04Page, i+1, msg, line) }
		if m := duFenceRE.FindStringSubmatch(line); m != nil || strings.HasPrefix(line, "```") {
			switch {
			case inFence:
				inFence = false
			case m == nil || !slices.Contains([]string{"sh", "json", "text"}, m[1]):
				at("a fenced block must be tagged sh, json or text")
				inFence = true
			default:
				inFence = true
			}
			continue
		}
		if inFence {
			continue
		}
		if m := duHeadingRE.FindStringSubmatch(line); m != nil {
			n := len(m[1])
			if n == 1 {
				titles++
				if i != 0 {
					at("the title must be the first line")
				}
			}
			if n > level+1 {
				at("a heading skips a level")
			}
			level = n
		}
		prose := duCodeRE.ReplaceAllString(line, "")
		if duHTMLRE.MatchString(prose) {
			at("HTML in the page")
		}
		if duPlanRE.MatchString(prose) {
			at("the page names the plan or promises future work")
		}
	}
	if inFence {
		t.Errorf("%s ends inside a fenced block", v1r04Page)
	}
	if titles != 1 {
		t.Errorf("%s has %d titles, want 1", v1r04Page, titles)
	}
	dcLinks(t, v1r04Page)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 go test -count=1 -run 'TestV1r04' ./internal/docs/`
Expected: FAIL, both tests, each with `docs/versioning.md has no section headed "## Upgrade from 0.x"`.

- [ ] **Step 3: Write the section**

In `docs/versioning.md`, insert this section directly before the heading `## Databases`. The tests need the quoted refusal on one line and each fenced block at the start of a line (the page's style tests do not recognise a fence inside a list item), so keep both as written:

````text
## Upgrade from 0.x

0.x does not upgrade to 1.0. Moving from a 0.x release to any 1.0 release,
release candidates included, is a reinstall. The updater refuses an update
between major versions, and its refusal says that the two versions
`cross a major version, which is not supported`. Darkpanel provides no way to
carry the data of a 0.x install over to 1.0.

To move to 1.0, first remove 0.x from every host, the controller's host
included. Leave out `--keep-data`, so that nothing of the 0.x install is left
for the new one:

```sh
sudo darkpanel uninstall
```

Then install 1.0 on the controller's host as
[Install Darkpanel](user/install.md) describes:

```sh
sudo sh install.sh --role controller --agent-advertise <address agents reach>:7443
```

Create each host again on the new controller, and enroll it with the token
that the first command prints, by running the second command on that host:

```sh
darkpanel host create --name <name>
sudo sh install.sh --role agent
```

[Uninstall Darkpanel](user/uninstall.md) lists the steps that
`darkpanel uninstall` runs and the options it takes.
````

Add nothing else to the page, and leave the rest of it as it is.

- [ ] **Step 4: Run the tests and the release check**

Run: `timeout 300 go test -count=1 -run 'TestV1r04' ./internal/docs/`
Expected: `ok`.

Then run the whole documentation suite, the text check and the release check:

```sh
timeout 300 go test -count=1 ./internal/docs/
rg -n 'Upgrade from 0.x|0.x does not upgrade to 1.0' docs/versioning.md
timeout 300 make release-check VERSION=1.0.0-rc.1; echo exit $?
```

Expected: `ok` for `internal/docs`; the `rg` command prints the heading and the sentence (two lines); `make release-check` runs `go run ./release/releasetool check-version 1.0.0-rc.1` and prints `exit 0`. Record the real output.

Then the checks for a Go task:

```sh
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 300 go test -race -count=1 ./internal/docs/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every test line `ok` or `?`, `0 issues.` from the linter, and no output from `go vet` and `go mod tidy -diff`. `git status` shows only `docs/versioning.md` and `internal/docs/versioning_test.go`, and no `CHANGELOG.md`. Then `rm -rf "$TMPDIR"` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/versioning.md internal/docs/versioning_test.go
git commit -m "docs(versioning): state the 0.x reinstall boundary"
```

### Task 5: Report Compose failures without their diagnostics

**Files:**
- Modify: `internal/modules/docker/compose_check.go` (the `prepareCompose` branch for a non-zero `config` exit, near the call to `composeLastLine`; delete `composeLastLine` and the `composeStderrMax` constant, and the `bytes` and `unicode/utf8` imports they alone use)
- Modify: `internal/modules/docker/compose_run.go` (`composeFailure` and its call in `composeVerb.Apply`)
- Create: `internal/modules/docker/compose_failure_test.go`
- Test: `internal/modules/docker/compose_check_test.go` (rewrite `TestCCPrepareReportsAFailedConfigWithoutLeakingOutput`)
- Test: `internal/modules/docker/compose_run_test.go` (delete `TestCUFailedRunNamesTheVerbTheStatusAndTheLastStderrLine` and the imports that leaves unused)

**Interfaces:**
- Consumes: Contracts "Compose failures" (the exact message, its two verbs, and the rule that no byte of Compose's stderr reaches an error, a change record or a log line). Existing test helpers `newDKHost`, `ccProject`, `ccPrepare`, `ccRefused`, `cuProject`, `cuApply`, `cuRow`, `cuKey`, `cuInput`, and `h.results` (a scripted `core.CmdResult` per argument vector, keyed by the vector joined with single spaces).
- Produces: nothing another task consumes. One new unexported function in `compose_check.go`: `composeFailureDetail(verb string, p projectRow, exit int) string`. `composeFailure` becomes `composeFailure(word string, p projectRow, res core.CmdResult) error`.

**Items:** 2026-11-06-v1-security-review#12

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

The failure scenario (Codex F10): an operator interpolates a secret from an env file into a value Compose rejects, such as an image name. Compose quotes the value on its last stderr line. `composeLastLine` copies that line, cut to 300 bytes, into the error of `docker compose config` (a refusal) and of a run (`composeFailure`, which the lifecycle engine persists in the change record). The fix removes stderr from both errors and keeps only the exit status and the command the operator can run on the host to see the diagnostic themselves.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/docker/compose_failure_test.go`:

```go
package docker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const v1r05Secret = "placeholder-secret"

// v1r05Stderr is a Compose diagnostic whose last line quotes a value that came from an env file.
func v1r05Stderr() []byte {
	return []byte("warning: the TAG variable is not set\nservice \"web\" has an invalid image \"nginx:" + v1r05Secret + "\"\n\n")
}

// v1r05Clean fails the test when the error carries any part of the diagnostics the fake Compose printed.
func v1r05Clean(t *testing.T, err error) {
	t.Helper()
	text := err.Error() + " " + errcode.From(err).Detail
	for _, leaked := range []string{v1r05Secret, "invalid image", "TAG variable", "ZZZZ"} {
		if strings.Contains(text, leaked) {
			t.Errorf("error %q carries %q from Compose's stderr", text, leaked)
		}
	}
}

func TestV1r05FailedConfigReportsOnlyTheExitStatus(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		stderr []byte
	}{
		{"stderr quotes a secret", 1, v1r05Stderr()},
		{"long stderr line", 15, []byte(strings.Repeat("Z", 500) + v1r05Secret + "\n")},
		{"no stderr", 2, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			h.results[strings.Join(composeArgs(p, nil, "config", "--format", "json"), " ")] = core.CmdResult{ExitCode: tc.code, Stderr: tc.stderr}
			_, err := ccPrepare(h, "shop", nil)
			ccRefused(t, err, errcode.DockerComposeUnavailable)
			want := fmt.Sprintf(`docker compose config failed with exit status %d; run "docker compose -f /srv/shop/compose.yaml config" on the host to see why`, tc.code)
			if got := errcode.From(err).Detail; got != want {
				t.Errorf("detail %q, want %q", got, want)
			}
			v1r05Clean(t, err)
		})
	}
}

func TestV1r05FailedRunReportsOnlyTheVerbAndStatus(t *testing.T) {
	tests := []struct {
		verb   string
		def    func(dk) core.ActionDef
		args   []string
		code   int
		stderr []byte
	}{
		{"up", dk.composeUp, []string{"up", "-d", "--no-build"}, 1, v1r05Stderr()},
		{"down", dk.composeDown, []string{"down"}, 2, v1r05Stderr()},
		{"pull", dk.composePull, []string{"pull"}, 3, []byte(strings.Repeat("Z", 500) + v1r05Secret)},
		{"restart", dk.composeRestart, []string{"restart"}, 4, nil},
	}
	for _, tc := range tests {
		t.Run(tc.verb, func(t *testing.T) {
			h := newDKHost(t)
			p := cuProject(t, h)
			before := cuRow(t, h)
			h.results[cuKey(composeArgs(p, p.Profiles, tc.args...))] = core.CmdResult{ExitCode: tc.code, Stderr: tc.stderr}
			_, err := cuApply(t, h, tc.def(h.dk()), cuInput)
			want := fmt.Sprintf(`docker compose %s failed with exit status %d; run "docker compose -f /srv/shop/compose.yaml %s" on the host to see why`,
				tc.verb, tc.code, tc.verb)
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.DockerComposeUnavailable || coded.Detail != want {
				t.Fatalf("Apply = %v, want docker.compose_unavailable %q", err, want)
			}
			if core.IsDefinite(err) {
				t.Error("a failed run is marked definite, but the run may have changed containers")
			}
			v1r05Clean(t, err)
			if after := cuRow(t, h); after.RunHash != before.RunHash || !after.LastRun.Equal(before.LastRun) {
				t.Errorf("run recorded after a failure: %+v, was %+v", after, before)
			}
		})
	}
}

func TestV1r05FailureDetailRepeatsTheFlagForEveryComposeFile(t *testing.T) {
	p := projectRow{Name: "shop", Files: []string{"/srv/shop/compose.yaml", "/srv/shop/override.yaml"}}
	got := composeFailureDetail("up", p, 3)
	want := `docker compose up failed with exit status 3; run "docker compose -f /srv/shop/compose.yaml -f /srv/shop/override.yaml up" on the host to see why`
	if got != want {
		t.Errorf("composeFailureDetail = %q, want %q", got, want)
	}
}
```

Two existing tests assert the behaviour this task removes. In `internal/modules/docker/compose_check_test.go`, replace the whole of `TestCCPrepareReportsAFailedConfigWithoutLeakingOutput` with the version below. It keeps the two rows about unreadable `config` output and drops the two rows (and the 300-byte check) about the stderr line:

```go
func TestCCPrepareReportsAFailedConfigWithoutLeakingOutput(t *testing.T) {
	tests := []struct {
		name   string
		res    core.CmdResult
		detail []string
		absent []string
	}{
		{"unreadable output", core.CmdResult{Stdout: []byte(`{"name":"shop","services":"SECRET_TOKEN_VALUE"}`)},
			[]string{"resolved configuration"}, []string{"SECRET_TOKEN_VALUE"}},
		{"not JSON", core.CmdResult{Stdout: []byte("SECRET_TOKEN_VALUE")}, []string{"resolved configuration"}, []string{"SECRET_TOKEN_VALUE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			h.results[strings.Join(composeArgs(p, nil, "config", "--format", "json"), " ")] = tc.res
			_, err := ccPrepare(h, "shop", nil)
			ccRefused(t, err, errcode.DockerComposeUnavailable, tc.detail...)
			d := errcode.From(err).Detail
			for _, a := range tc.absent {
				if strings.Contains(d, a) {
					t.Errorf("detail %q contains %q", d, a)
				}
			}
		})
	}
}
```

In `internal/modules/docker/compose_run_test.go`, delete `TestCUFailedRunNamesTheVerbTheStatusAndTheLastStderrLine` entirely (the new `TestV1r05FailedRunReportsOnlyTheVerbAndStatus` covers its two assertions about the code and the definite flag, and the unchanged run row). Then remove the `unicode/utf8` import, and any other import the compiler reports unused.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/5 && export TMPDIR=/root/.cache/w10b-tmp/5 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r05' ./internal/modules/docker/`
Expected: build failure `undefined: composeFailureDetail` (from `TestV1r05FailureDetailRepeatsTheFlagForEveryComposeFile`). To see the other two fail on their assertions, comment that one test out and rerun: `TestV1r05FailedConfigReportsOnlyTheExitStatus` and `TestV1r05FailedRunReportsOnlyTheVerbAndStatus` FAIL with details such as `docker compose config failed with exit status 1: service "web" has an invalid image "nginx:placeholder-secret"` and `compose up exited with status 1: ...`. Quote both outputs in the report, then restore the commented-out test.

- [ ] **Step 3: Implement**

1. `compose_check.go`: add `composeFailureDetail(verb string, p projectRow, exit int) string` with a doc comment that gives the why: Compose's diagnostics can quote values it resolved from env files, so a failure reports only its exit status and the command the operator can run on the host. It returns exactly `docker compose <verb> failed with exit status <exit>; run "docker compose -f <file> <verb>" on the host to see why` (Contracts "Compose failures"). `<file>` is the project's Compose files in their stored order: for each entry of `p.Files`, `-f <path>`, separated by single spaces, so a project with one file prints `-f /srv/shop/compose.yaml` and a project with two prints `-f a -f b`. The rendering of `<exit>` is `strconv.Itoa`.
2. `compose_check.go`, `prepareCompose`: where `res.ExitCode != 0` after the `config --format json` run, return `composeUnavailable("%s", composeFailureDetail("config", row, res.ExitCode))`. It stays a definite `docker.compose_unavailable` refusal, and `res.Stderr` is no longer read. Leave the `docker: run compose config: %w` error for a runner failure as it is.
3. `compose_run.go`: change `composeFailure` to take the project row and return `errcode.New(errcode.DockerComposeUnavailable, composeFailureDetail(word, p, res.ExitCode))`; `composeVerb.Apply` passes `r.Project`. The error stays non-definite, and the comment above the call in `Apply` that explains why stays. Update `composeFailure`'s doc comment: the verb and the exit status, never stderr.
4. Delete `composeLastLine`, the `composeStderrMax` constant and its comment, and the imports `bytes` and `unicode/utf8` of `compose_check.go` (`goimports`-clean; the compiler names any other unused import).
5. Nothing else in the module reads a Compose process's stderr: `rg -n 'Stderr' internal/modules/docker --glob '!*_test.go'` shows only the Docker logs options.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w10b-tmp/5 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r05|TestCCPrepare|TestCU' ./internal/modules/docker/ && ! rg -n 'composeLastLine|composeStderrMax' internal/modules/docker`
Expected: `ok` and no match from `rg`.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/modules/docker/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/5 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/docker/compose_check.go internal/modules/docker/compose_run.go internal/modules/docker/compose_failure_test.go internal/modules/docker/compose_check_test.go internal/modules/docker/compose_run_test.go
git commit -m "fix(docker): report Compose failures by status"
```

### Task 6: Roll back the Tailscale repository file on its own context

**Files:**
- Modify: `internal/modules/tailscale/package_pin.go` (`packagePinHandler.Task`: the two early-failure branches after `insertPin`, and `txFail`)
- Test: `internal/modules/tailscale/package_pin_test.go` (append; add `"io/fs"` to the imports)

**Interfaces:**
- Consumes: the existing constant `pinRollbackBudget` (10 minutes) in `package_pin.go`, and the test helpers `pnNewSetup`, `pnInput`, `pnPin`, `pnPinEffect`, `pnTarget` of `package_pin_test.go`, with `tsHost` (`s.h`) and its journal.
- Produces: nothing another task consumes. One new unexported method, `func (h packagePinHandler) rollbackContext(ctx context.Context) (context.Context, context.CancelFunc)`.

**Items:** 2026-10-21-v1-completion#435

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

The hazard (`docs/dforge/notes/v1-w10a-known-fixes-minors.md`, "Hazards discovered during execution", Task 10): the pin's forward steps already roll back on a detached context (`txFail`, `package_pin.go`), but `Task` still rolls back on the forward `ctx` when `h.t.ensureRepo` fails, and when journaling the pin's effect fails. If the forward context has ended (the 20-minute apply budget ran out, or the agent is stopping) while the repository files are written, `h.rollback` writes `rolled_back` into the pin row with a dead context, fails, and `env.Effects.Skipped` is never reached. The pin row stays in phase `recorded` and its effect stays intended. The branch that journals the effect has the same defect and is fixed with it.

The other files named for this row need no change. `ensureRepo` (`repo.go`) is only a writer: it has no rollback. `package.install` (`package_install.go`) journals its repository-file effects, and the engine settles an interrupted install through `Observe` and `Inverse`. `package.update` (`package_update.go`) has no rollback by design.

- [ ] **Step 1: Write the failing test**

In `internal/modules/tailscale/package_pin_test.go`, add `"io/fs"` to the standard-library imports, then append:

```go
// v1r06Files ends the pin's forward context as soon as it is asked to write a file, as the expiry of the apply budget
// would while the repository files are written.
type v1r06Files struct {
	core.FileWriter
	cancel context.CancelFunc
}

func (f v1r06Files) WriteFile(ctx context.Context, _, _ string, _ []byte, _ fs.FileMode) error {
	f.cancel()
	return ctx.Err()
}

// v1r06Journal ends the pin's forward context as soon as it is asked to journal an effect.
type v1r06Journal struct {
	core.EffectJournal
	cancel context.CancelFunc
}

func (j v1r06Journal) Intend(ctx context.Context, _ core.Effect, _ []byte) (int, error) {
	j.cancel()
	return 0, ctx.Err()
}

// TestV1r06PinRollsBackAnEarlyFailureAfterTheForwardContextEnds: a pin whose forward context ends while it writes the
// repository files, or while it journals its effect, still records the rollback and settles the effect.
func TestV1r06PinRollsBackAnEarlyFailureAfterTheForwardContextEnds(t *testing.T) {
	tests := []struct {
		name       string
		arm        func(s *pnSetup, cancel context.CancelFunc)
		wantEffect bool
	}{
		{"repository file write", func(s *pnSetup, cancel context.CancelFunc) {
			delete(s.h.reads, keyringPath)
			delete(s.h.reads, sourcesPath)
			s.env.Files = v1r06Files{FileWriter: s.h, cancel: cancel}
		}, true},
		{"effect journal", func(s *pnSetup, cancel context.CancelFunc) {
			s.env.Effects = v1r06Journal{EffectJournal: s.h.journal, cancel: cancel}
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tc.arm(s, cancel)

			_, err := s.t.packagePin().Handler.Task(ctx, s.env, pnInput(pnTarget))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Task = %v, want the cancellation that ended the forward context", err)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" {
				t.Errorf("pin phase %s, want rolled_back: the rollback must not run on the ended forward context", p.Phase)
			}
			if tc.wantEffect {
				if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
					t.Errorf("effect state %s, want skipped: nothing was installed", e.State)
				}
			} else if n := len(s.h.journal.effects()); n != 0 {
				t.Errorf("%d effects journaled, want none: the journal refused the pin's effect", n)
			}
			if s.apt.version != pnPrior || !s.apt.held {
				t.Errorf("host has version %s, held %v; want %s, held", s.apt.version, s.apt.held, pnPrior)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w10b-tmp/6 && export TMPDIR=/root/.cache/w10b-tmp/6 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r06' ./internal/modules/tailscale/`
Expected: FAIL for both rows with `pin phase recorded, want rolled_back: the rollback must not run on the ended forward context` (and, for the first row, `effect state intended, want skipped`). Quote the output verbatim in the report.

- [ ] **Step 3: Implement**

1. `package_pin.go`: add `func (h packagePinHandler) rollbackContext(ctx context.Context) (context.Context, context.CancelFunc)`. It returns `context.WithTimeout(context.WithoutCancel(ctx), pinRollbackBudget)`. Its doc comment gives the why: a pin's rollback must still run when the forward context has ended, and the budget keeps a stuck rollback from running forever. Make `txFail` call it in place of its inline `context.WithTimeout(context.WithoutCancel(ctx), pinRollbackBudget)`; its behaviour does not change.
2. `Task`, branch `env.Effects.Intend` failed: before `h.rollback`, call `h.rollbackContext(ctx)`, `defer` the returned cancel function, and pass the new context to `h.rollback`. The returned error is as it was (the Intend error, joined with the rollback error when the rollback fails).
3. `Task`, branch `h.t.ensureRepo` failed: the same. The new context goes to `h.rollback` and to `env.Effects.Skipped`; the returned errors (the repository error, joined with the rollback or Skipped error when either fails) are as they were. `ensureRepo`, `rollback` and every later step stay as they are, and the forward steps keep using the forward `ctx`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `export TMPDIR=/root/.cache/w10b-tmp/6 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r06|TestW10Tp|TestPn' ./internal/modules/tailscale/`
Expected: `ok`.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/modules/tailscale/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/6 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_pin.go internal/modules/tailscale/package_pin_test.go
git commit -m "fix(tailscale): detach early pin rollbacks"
```

### Task 7: Carry read-step outputs and the DERP grant warning

**Files:**
- Modify: `internal/core/lifecycle/composite_run.go` (`completeComposite`, about lines 965-1000, and its doc comment)
- Modify: `internal/core/contract.go` (the comment about composite outputs near line 309 only)
- Modify: `internal/modules/derp/wire.go` (`RegionChanged`, `ServerRemoved`)
- Modify: `internal/modules/derp/composite_region.go` (`regionCompositeOutput`; new helper `compositeEditWarnings`)
- Modify: `internal/modules/derp/composite_remove.go` (`serverRemoveComposer.Output`)
- Modify: `schemas/derp.region.set/v1.json`, `schemas/derp.region.remove/v1.json`, `schemas/derp.server.remove/v1.json`, `docs/api/**` (including `docs/api/openapi.json`), all by regeneration
- Test: `internal/core/lifecycle/composite_confirm_test.go` (`TestCompositeConfirmCompletes`)
- Test: `internal/modules/derp/composite_region_test.go` (`TestCGRegionOutput`)
- Test: `internal/modules/derp/composite_remove_test.go` (`TestCMOutput`)
- Test: `internal/modules/derp/wire_test.go` (two rows of the wire table)

**Interfaces:**
- Consumes: Contracts "Composite read-step outputs", which this task implements. It reads, and does not change, `derp.region.edits` in `region_edits.go` (class read, controller side): it already returns `warnings` as `[]`, never `null`, and its remove op already warns when the visibility grant of the last custom region's server stays in the policy.
- Produces: per Contracts "Composite read-step outputs", `completeComposite` hands the composer's `Output` the redacted output of every step, read steps included, keyed by step id. `RegionChanged` and `ServerRemoved` each gain `Warnings []string` with JSON name `warnings`, `[]` and never `null`. One new unexported helper in package `derp`, `compositeEditWarnings(steps map[string]json.RawMessage) ([]string, error)`.

**Items:** 2026-10-21-v1-completion#303

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

`derp.region.remove` and `derp.server.remove` compute their policy edits in a read step, `edits`, whose result carries the warning that the visibility grant of the removed server stays in the policy. `completeComposite` skips read steps when it builds the composer's outputs, so `Output` never sees the warning and the operator never learns that a grant stays. The region step cannot carry it, because it is another module's action, `tailnet.policy.patch`, whose output is only an ETag. The fix passes read-step outputs to every composer, then has the two DERP composers copy the edits step's `warnings` into their output. Wire types change, so the task regenerates `schemas/` and `docs/api/` (Global Constraints, "Generated files").

The spec's sentence "read steps are not among them" is outside this task's files; do not edit the spec.

- [ ] **Step 1: Write the failing tests**

1. `internal/core/lifecycle/composite_confirm_test.go`, `TestCompositeConfirmCompletes`: replace the block from `var out cfxOut` through the closing brace of the `if _, hasRead := out.Steps["r"]; ...` check with the block below. The composite `cfx.gate.add` reads item `web-a` in its read step `r` before step `a` creates it, so the read step's output is `{"name":"web-a","value":""}`.

```go
	var out cfxOut
	if err := json.Unmarshal(got.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Steps) != 3 {
		t.Fatalf("output steps = %v, want r, a and g", out.Steps)
	}
	var read cstView
	if err := json.Unmarshal(out.Steps["r"], &read); err != nil || read != (cstView{Name: "web-a"}) {
		t.Errorf("output of read step r = %s (%v), want the item web-a as the read saw it, absent", out.Steps["r"], err)
	}
```

2. `internal/modules/derp/composite_region_test.go`: replace the whole of `TestCGRegionOutput` with:

```go
func TestCGRegionOutput(t *testing.T) {
	h := newDPHost(t)
	region := json.RawMessage(`{"etag":"\"abc123\""}`)
	const grant = "the visibility grant of region 900's server stays in the policy: its tag could not be read"
	const setIn = `{"hostId":"h7","regionId":900,"regionCode":"dp-eu","regionName":"Frankfurt"}`
	const removeIn = `{"regionId":900}`
	const removeEdits = `[{"op":"delete","path":"/derpMap/Regions/900"}]`
	tests := []struct {
		name     string
		def      core.ActionDef
		in       string
		edits    string // the edits step's output; "" leaves the step out of the map
		warnings []any
	}{
		{"set without warnings", h.dp().regionSet(), setIn, `{"edits":[],"warnings":[]}`, []any{}},
		{"set with a warning", h.dp().regionSet(), setIn,
			`{"edits":[],"warnings":["the STUN probe of the server got no answer"]}`, []any{"the STUN probe of the server got no answer"}},
		{"remove without warnings", h.dp().regionRemove(), removeIn, `{"edits":` + removeEdits + `,"warnings":[]}`, []any{}},
		{"remove that leaves the grant", h.dp().regionRemove(), removeIn, `{"edits":` + removeEdits + `,"warnings":["` + grant + `"]}`, []any{grant}},
		{"remove whose edits step has a null output", h.dp().regionRemove(), removeIn, `null`, []any{}},
		{"remove without an edits step", h.dp().regionRemove(), removeIn, "", []any{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps := map[string]json.RawMessage{"region": region}
			if tc.edits != "" {
				steps["edits"] = json.RawMessage(tc.edits)
			}
			out, err := tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), steps)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cgJSON(t, out), map[string]any{"etag": `"abc123"`, "warnings": tc.warnings}; !reflect.DeepEqual(got, want) {
				t.Errorf("output %s, want %v", out, want)
			}
			_, err = tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), map[string]json.RawMessage{})
			if err == nil || !strings.Contains(err.Error(), "region") {
				t.Errorf("output without the region step: %v, want an error naming region", err)
			}
			steps["edits"] = json.RawMessage(`"not an object"`)
			_, err = tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), steps)
			if err == nil || !strings.Contains(err.Error(), "edits") {
				t.Errorf("output with an unreadable edits step: %v, want an error naming edits", err)
			}
		})
	}
}
```

3. `internal/modules/derp/composite_remove_test.go`: replace the whole of `TestCMOutput` with:

```go
func TestCMOutput(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().serverRemove()
	in := json.RawMessage(`{"hostId":"h7"}`)
	const grant = "the visibility grant of region 900's server stays in the policy: its tag could not be read"
	const removed = `["derp.unit:darkpanel-derper.service","derp.user:darkpanel-derp"]`
	const edits = `[{"op":"delete","path":"/derpMap/Regions/900"}]`
	tests := []struct {
		name      string
		uninstall string
		edits     string // the edits step's output; "" when the removal has no edits step
		want      string
	}{
		{"what was removed", `{"removed":` + removed + `}`, "", `{"removed":` + removed + `,"warnings":[]}`},
		{"nothing was installed", `{"removed":[]}`, "", `{"removed":[],"warnings":[]}`},
		{"no list at all is still a list", `{}`, "", `{"removed":[],"warnings":[]}`},
		{"the region was withdrawn cleanly", `{"removed":` + removed + `}`, `{"edits":` + edits + `,"warnings":[]}`,
			`{"removed":` + removed + `,"warnings":[]}`},
		{"the grant of the region's server stays in the policy", `{"removed":` + removed + `}`,
			`{"edits":` + edits + `,"warnings":["` + grant + `"]}`, `{"removed":` + removed + `,"warnings":["` + grant + `"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps := map[string]json.RawMessage{"uninstall": json.RawMessage(tc.uninstall), "unpin": json.RawMessage(`{"version":"1.104.1","held":false}`), "ports": json.RawMessage(`{}`)}
			if tc.edits != "" {
				steps["edits"] = json.RawMessage(tc.edits)
				steps["region"] = json.RawMessage(`{"etag":"x"}`)
			}
			out, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, steps)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cmJSON(t, out), cmJSON(t, json.RawMessage(tc.want)); !reflect.DeepEqual(got, want) {
				t.Errorf("output %s, want %s", out, tc.want)
			}
		})
	}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, map[string]json.RawMessage{"ports": json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "uninstall") {
		t.Errorf("output without the uninstall step: %v, want an error naming uninstall", err)
	}
}
```

4. `internal/modules/derp/wire_test.go`: in the table of wire documents, replace the rows `"region changed"` and `"server removed"` with these three rows (a nil `Warnings` would encode as `null`, which the contract forbids, so the rows set it):

```go
		{"region changed", RegionChanged{ETag: "e1", Warnings: []string{}}, `{"etag":"e1","warnings":[]}`},
		{"region changed with a warning", RegionChanged{ETag: "e1", Warnings: []string{"w"}}, `{"etag":"e1","warnings":["w"]}`},
		{"server removed", ServerRemoved{Removed: []string{"a", "b"}, Warnings: []string{}}, `{"removed":["a","b"],"warnings":[]}`},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/7 && export TMPDIR=/root/.cache/w10b-tmp/7 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestCompositeConfirmCompletes' ./internal/core/lifecycle/ && timeout 300 go test -count=1 -run 'TestCGRegionOutput|TestCMOutput|TestSdWire' ./internal/modules/derp/`
Expected: the lifecycle test FAILS with `output steps = map[a:... g:...], want r, a and g`. The derp package fails to build with `unknown field Warnings in struct literal of type RegionChanged` (from `wire_test.go`). To see the composer tests fail on their assertions, add only the two `Warnings` fields to `wire.go` (Step 3, item 3) and rerun `TestCGRegionOutput|TestCMOutput`: both FAIL with output lacking the edits warning (for example `output {"etag":"\"abc123\"","warnings":[]}, want map[...warnings:[the visibility grant ...]]`). Quote the three outputs in the report.

- [ ] **Step 3: Implement**

1. `internal/core/lifecycle/composite_run.go`, `completeComposite`: remove the `if sdef.Spec.Class == core.ClassRead { continue }` skip. Every step, read steps included, now contributes `outputs[s.ID] = e.cfg.Redact(sdef.OutputType, raw)`. A step with no sealed output, as a read step that never ran would have, keeps the `null` placeholder it already gets. Keep the registry lookup, since `Redact` needs `sdef.OutputType`. Change the function's doc comment from "every non-read step's redacted output" to "every step's redacted output". In `internal/core/contract.go`, change the comment near line 309 that says "read steps are not among them" so that it says read steps are included. Change no code in `contract.go`.
2. `internal/modules/derp/composite_region.go`: add `compositeEditWarnings(steps map[string]json.RawMessage) ([]string, error)`, with a doc comment. It returns the `warnings` of the step with id `edits`, decoded as a `RegionEdits`. A missing `edits` step, a JSON `null` output, or a `null` or absent `warnings` member all return an empty, non-nil slice. An output that is not a JSON object returns the error `derp: the edits step's output: <cause>`. Change `regionCompositeOutput` to decode the region step's output into a local struct holding only `etag` (so a member of that output named `warnings` can never leak in), and to return `RegionChanged{ETag: <etag>, Warnings: <compositeEditWarnings>}`. The existing error for a missing region step, `derp: the region change has no output of its region step`, stays. Both `regionSetComposer.Output` and `regionRemoveComposer.Output` already call it.
3. `internal/modules/derp/wire.go`: `RegionChanged` and `ServerRemoved` each gain `Warnings []string` with the JSON name `warnings` and a `jsonschema_description` (every wire field needs one; `TestSdWireDescriptions` checks). For `RegionChanged`: warnings of the step that computed the policy edits, such as a visibility grant that stays in the policy, empty when there are none. For `ServerRemoved`: the same, for the withdrawal of the server's region. Update the doc comment of `RegionChanged`, which names `derp.region.set` and `derp.region.remove`.
4. `internal/modules/derp/composite_remove.go`, `serverRemoveComposer.Output`: after decoding the uninstall step, set `Warnings` from `compositeEditWarnings(steps)`. A removal without an `edits` step (no region, or a disconnected tailnet) gets `[]`. The conversion `ServerRemoved(out)` no longer type-checks once the struct differs from `ServerUninstalled`, so build the value field by field. `Removed` is still `[]`, never `null`.
5. `region_edits.go` is unchanged.
6. Regenerate: `timeout 300 make schemas && timeout 300 make api-docs`. `git status` then shows exactly these changed beyond the Go files: `schemas/derp.region.set/v1.json`, `schemas/derp.region.remove/v1.json`, `schemas/derp.server.remove/v1.json`, and `docs/api/**` including `docs/api/openapi.json`. In each schema file the output gains a required `warnings` array of strings with the description from item 3. Any other changed generated file means another change leaked in: stop and report it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w10b-tmp/7 GOTOOLCHAIN=local && timeout 600 go test -count=1 ./internal/core/lifecycle/ ./internal/modules/derp/... ./internal/apidocs/... ./schemas/... ./internal/core/schema/... ./internal/modules/`
Expected: `ok` for every package, `TestAdAPIDocsCurrent` included.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/core/lifecycle/ ./internal/modules/derp/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/7 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

Two commits, one per concern:

```bash
git add internal/core/lifecycle/composite_run.go internal/core/contract.go internal/core/lifecycle/composite_confirm_test.go
git commit -m "fix(lifecycle): pass read outputs to composers"
git add internal/modules/derp/wire.go internal/modules/derp/composite_region.go internal/modules/derp/composite_remove.go internal/modules/derp/composite_region_test.go internal/modules/derp/composite_remove_test.go internal/modules/derp/wire_test.go schemas docs/api
git commit -m "feat(derp): carry edits warnings in outputs"
```

### Task 8: Reserve every port of NPM's own names and addresses

**Files:**
- Modify: `internal/modules/npm/proxy/upstream.go` (`CheckUpstream`, lines 38-59; the `npmAdminPort` constant, line 15, which is removed)
- Test: `internal/modules/npm/proxy/upstream_test.go` (new test, four flipped rows, one renamed test)
- Test: `internal/modules/npm/proxy/fuzz_test.go` (the "nothing reserved" property)
- Test: `internal/modules/npm/proxyhost_add_test.go` (`TestPaReservedUpstreams`, one flipped row and three new rows)
- Read only: `internal/modules/npm/composite_admin.go` (the `npm.admin.expose` composite is the only caller whose action name equals `adminOwner`)

**Interfaces:**
- Consumes: Contracts "NPM forward-host refusals", Task 8 bullet.
- Produces: `proxy.CheckUpstream(r Reserved, host string, port int, parent string) error` keeps its signature. Outside the `npm.admin.expose` parent it now refuses with `npm.reserved` every port of the NPM container's names (`darkpanel-npm`, `r.NPMNames`, cut at each dot) and of its addresses (`r.NPMAddrs`). Under that parent nothing changes. The constant `npmAdminPort` is removed. Task 9 calls `CheckUpstream` unchanged for the address branch.

**Items:** 2026-11-06-v1-security-review#1, 2026-10-21-v1-completion#330

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 0 - spec 2 - coupling 0 - risk 3 = 5
**Lenses:** security

The finding: `CheckUpstream` refuses NPM's own names and addresses only on port 81. NPM's backend API listens on port 3000 on every interface of its container, so a user with scope `operate` who adds a proxy host forwarding to `172.18.0.2:3000` or to `darkpanel-npm:3000` publishes NPM's admin API without `npm.admin.expose`, which needs scope `admin`. The fix drops the port condition.

- [ ] **Step 1: Write the failing test**

Add this test to `internal/modules/npm/proxy/upstream_test.go`. Add `"net"` and `"strconv"` to its imports, in sorted order (`strings`, `net/netip` and `errcode` are already imported).

```go
func TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	ports := []int{22, 80, 81, 443, 3000, 8080, 65535}

	t.Run("NPM's own identities are refused on every port", func(t *testing.T) {
		for _, host := range []string{
			"172.18.0.2", "fd00:18::2", "[fd00:18::2]", "172.18.2", "darkpanel-npm", "4fa6e0f0c678", "NPM",
			"NPM.apps.internal.", "darkpanel-npm.darkpanel-npm",
		} {
			for _, port := range ports {
				for _, parent := range []string{"", panel, "other.action"} {
					err := CheckUpstream(pxReserved(), host, port, parent)
					e := errcode.From(err)
					if e.Code != errcode.NPMReserved {
						t.Errorf("CheckUpstream(%q, %d, %q) = %v, want npm.reserved", host, port, parent, err)
						continue
					}
					want := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
					if !strings.Contains(e.Detail, want) {
						t.Errorf("detail %q does not name the upstream %s", e.Detail, want)
					}
				}
			}
		}
	})

	t.Run("the admin exposure's own composite may reach NPM on any port", func(t *testing.T) {
		for _, host := range []string{"172.18.0.2", "darkpanel-npm", "npm", "127.0.0.1"} {
			for _, port := range ports {
				if err := CheckUpstream(pxReserved(), host, port, admin); err != nil {
					t.Errorf("CheckUpstream(%q, %d, %q) = %v, want nil", host, port, admin, err)
				}
			}
		}
	})

	t.Run("other upstreams stay allowed on every port", func(t *testing.T) {
		for _, host := range []string{"web", "web.npm", "darkpanel-npm-data", "npm2", "10.0.0.5", "172.18.0.3", "app.example.com"} {
			for _, port := range ports {
				if err := CheckUpstream(pxReserved(), host, port, ""); err != nil {
					t.Errorf("CheckUpstream(%q, %d, %q) = %v, want nil", host, port, "", err)
				}
			}
		}
	})
}
```

The older rows of `TestPxCheckUpstream` assert the behaviour this task removes. In the same file, change `refuse` from `false` to `true` on these four rows and keep every other field:

- `{"an NPM address on another port", "172.18.0.2", 80, "", false}`
- `{"the NPM container's name on another port", "darkpanel-npm", 8080, "", false}`
- `{"an NPM alias on another port", "npm", 8080, "", false}`
- `{"the NPM container's name with a network on another port", "darkpanel-npm.darkpanel-npm", 8080, "", false}`

Replace the last test of the file with this one. Its old name and its second assertion described a rule that no longer holds for NPM's own container name.

```go
func TestPxCheckUpstreamWithNothingKnownRefusesTheLoopbackAndNPMsName(t *testing.T) {
	if err := CheckUpstream(Reserved{}, "172.31.255.2", 9443, ""); err != nil {
		t.Errorf("an empty Reserved refused a private address: %v", err)
	}
	if err := CheckUpstream(Reserved{}, "127.0.0.1", 81, ""); errcode.From(err).Code != errcode.NPMReserved {
		t.Errorf("an empty Reserved let the loopback through: %v", err)
	}
	for _, port := range []int{80, 3000, 8080} {
		if err := CheckUpstream(Reserved{}, "darkpanel-npm", port, ""); errcode.From(err).Code != errcode.NPMReserved {
			t.Errorf("an empty Reserved let NPM's container name through on port %d: %v", port, err)
		}
	}
}
```

In `internal/modules/npm/proxy/fuzz_test.go`, replace the whole `if port != npmAdminPort { ... }` block (it ends before `if want, err := netip.ParseAddr(host)`) with the block below. Also replace the sentence "With nothing reserved and a port other than NPM's admin port, the loopback rule is the only one that refuses." in the doc comment of `FuzzCheckUpstream` with "With nothing reserved, only the loopback rule and NPM's own container name refuse, on every port."

```go
		nothingKnown := loopback || !isAddr && isNPMName(nil, name)
		if err := CheckUpstream(Reserved{}, host, int(port), parent); (err != nil) != (nothingKnown && parent != adminOwner) {
			t.Fatalf("CheckUpstream(%q, %d, %q) with nothing reserved = %v, refusable %v", fz8Head(host), port, parent, err, nothingKnown)
		}
```

In `internal/modules/npm/proxyhost_add_test.go`, `TestPaReservedUpstreams` drives the whole add handler. In its table change `{"npm", 8080, "", false}` to `{"npm", 8080, "", true}` and add these rows next to it. In that test NPM's container has the address `172.18.0.2` and the alias `npm`.

```go
		{"npm", 3000, "", true},
		{"172.18.0.2", 3000, "", true},
		{"172.18.0.2", 3000, paPanel, true},
		{"172.18.0.2", 3000, paAdmin, false},
```

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10b-tmp/8 && export TMPDIR=/root/.cache/w10b-tmp/8 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r08|TestPxCheckUpstream' ./internal/modules/npm/proxy/
timeout 600 go test -count=1 -run 'TestPaReservedUpstreams' ./internal/modules/npm/
```

Expected: FAIL. The first command prints lines such as `CheckUpstream("172.18.0.2", 22, "") = <nil>, want npm.reserved` and a failed row of `TestPxCheckUpstream` for `an NPM address on another port`. The second fails on the rows `npm:3000` and `172.18.0.2:3000`. Quote both outputs in the report.

- [ ] **Step 3: Implement**

1. In `internal/modules/npm/proxy/upstream.go`, `CheckUpstream`: inside the `if parent != adminOwner` block, keep the loopback and unspecified-address check as it is. Replace the second `if`, whose condition begins `port == npmAdminPort &&`, by a refusal that has no port condition: when the host is a name for which `isNPMName(r.NPMNames, name)` holds, or an address for which `isNPMAddr(r.NPMAddrs, addr)` holds, return `reserved(host, port, "admin")`. `port` stays a parameter, because the error detail names it and the panel rule below is unchanged.
2. Delete the constant `npmAdminPort` from the `const` block. Nothing else uses it once the fuzz test no longer does.
3. Rewrite the doc comment of `CheckUpstream`: outside `npm.admin.expose` it refuses an upstream at the local host or at any port of NPM's own names and addresses; the panel network and gateway rule is unchanged. Add one comment line at the changed `if`, in full sentences, that says why the port does not matter: NPM's backend API listens on port 3000 on every interface of its container, so every port of NPM's names and addresses is reachable. Do not name a review, a row or a task.
4. Change nothing else: not the parser functions, not `isNPMName`, not the panel rule, not `internal/modules/npm/composite_admin.go`.

- [ ] **Step 4: Run test to verify it passes**

```sh
mkdir -p /root/.cache/w10b-tmp/8 && export TMPDIR=/root/.cache/w10b-tmp/8 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r08|TestPxCheckUpstream|FuzzCheckUpstream' ./internal/modules/npm/proxy/
timeout 120 go test -run '^$' -fuzz '^FuzzCheckUpstream$' -fuzztime 15s ./internal/modules/npm/proxy/
timeout 600 go test -count=1 -run 'TestPaReservedUpstreams' ./internal/modules/npm/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 2400 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/8 && go clean -testcache
```

Expected: every test `ok` (the fuzz run ends with `PASS`), `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and `git status --short` lists only the four files of this task (a fuzz run that found a failure would add a `testdata/fuzz` file: if it does, stop and report it).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/proxy/upstream.go internal/modules/npm/proxy/upstream_test.go internal/modules/npm/proxy/fuzz_test.go internal/modules/npm/proxyhost_add_test.go
git commit -m "fix(npm): reserve every port of NPM's own names"
```

### Task 9: Resolve NPM forward hosts at plan and at apply

**Files:**
- Modify: `internal/modules/npm/proxy/upstream.go` (add `LiteralAddr` and `CheckResolved` after `CheckUpstream`; adjust the doc comment of `CheckUpstream`)
- Modify: `internal/modules/npm/deps.go` (`deps` struct, `productionNM`, new `resolveVia` and `absentResolve`)
- Modify: `internal/modules/npm/proxyhost_add.go` (`checkUpstream`, lines 255-298, is split; `proxyhostAddFound` and `check`, lines 308-390, store the answers; the constants at line 37 and the validators at lines 87-101 are read only)
- Modify: `internal/modules/npm/proxyhost_edit.go` (`proxyHostUpdateHandler.check`, `Plan`, `Apply`)
- Test: `internal/modules/npm/proxy/upstream_test.go` (new tests)
- Test: `internal/modules/npm/proxyhost_resolve_test.go` (create)
- Test: `internal/modules/npm/testhost_test.go` (`thHost.nm`: a default fake resolver)

**Interfaces:**
- Consumes: Contracts "NPM forward-host refusals", Task 9 bullet. Task 8's `proxy.CheckUpstream`, whose address branch this task runs once per resolved answer.
- Produces, all unexported unless marked:
  - `proxy.LiteralAddr(host string) (netip.Addr, bool)`: whether nginx reads `host` as an address, by the same reading `CheckUpstream` applies, and that address (unmapped, no zone).
  - `proxy.CheckResolved(r Reserved, host string, answers []netip.Addr, port int, parent string) error`: runs `CheckUpstream` on each answer's text. A refusal is `npm.reserved` whose detail is `forward host <host> resolves to <answer>: ` followed by `CheckUpstream`'s detail.
  - `deps.resolve func(ctx context.Context, host string) ([]netip.Addr, error)`: the resolver seam. Production sets it with `resolveVia(net.DefaultResolver)`. A test binary gets `absentResolve`. Tests replace it with a fake.
  - `(nm).reservedSet(ctx, env) (proxy.Reserved, error)`: the first half of today's `checkUpstream`, which reads NPM's container and the panel network.
  - `(nm).checkUpstream(ctx, env, in) error`: unchanged behaviour (names and literals only, no resolution). `npm.proxyhost.remove`'s inverse keeps calling it.
  - `(nm).checkForward(ctx, env, in ProxyHostInput) ([]string, error)`: `checkUpstream`'s check, then the resolution. It returns the sorted answer set as text, `[]string{}` for a literal host. `npm.proxyhost.add` and `npm.proxyhost.update` call it.
  - `proxyhostAddFound.Resolved []string` (`json:"resolved"`), which the plan's state hash covers.

**Items:** 2026-11-06-v1-security-review#2, 2026-10-21-v1-completion#326

**Depends on:** Task 8
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

The finding: a forward host that is a DNS name is checked as a name only, so a name the caller controls and that resolves to `127.0.0.1`, to NPM's address or to the panel gateway passes with port 81 (and, after Task 8, with any port) and publishes NPM's admin UI or the panel. The fix resolves the name on the agent's host at plan and again at apply and runs every answer through the address branch. The sorted answer set is part of the plan's state hash. At apply the engine plans again and compares that hash (`checkReplan`, `internal/core/lifecycle/apply.go`), so an answer set that changed makes the engine refuse with `plan.state_changed`. This task writes no code for that code. Its test pins the hash, and the handler's own check at apply refuses a reserved answer.

- [ ] **Step 1: Write the failing tests**

Add these tests to `internal/modules/npm/proxy/upstream_test.go` (`net/netip`, `strings`, `testing` and `errcode` are already imported):

```go
func TestV1r09CheckResolved(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	addrs := func(s ...string) []netip.Addr {
		out := make([]netip.Addr, len(s))
		for i, a := range s {
			out[i] = netip.MustParseAddr(a)
		}
		return out
	}
	const host = "web.example.net"
	for _, tc := range []struct {
		name    string
		answers []netip.Addr
		port    int
		parent  string
		show    string // the offending answer, as the detail must show it; "" when nothing is refused
	}{
		{"a public answer", addrs("203.0.113.7"), 8080, "", ""},
		{"no answer", nil, 8080, "", ""},
		{"the loopback", addrs("127.0.0.1"), 8080, "", "127.0.0.1"},
		{"the loopback from the admin exposure", addrs("127.0.0.1"), 81, admin, ""},
		{"the IPv6 loopback", addrs("::1"), 8080, "", "::1"},
		{"an IPv4-mapped loopback", addrs("::ffff:127.0.0.1"), 8080, "", "127.0.0.1"},
		{"the unspecified address", addrs("0.0.0.0"), 8080, "", "0.0.0.0"},
		{"an NPM address on its API port", addrs("172.18.0.2"), 3000, "", "172.18.0.2"},
		{"an NPM IPv6 address", addrs("fd00:18::2"), 3000, "", "fd00:18::2"},
		{"the panel gateway", addrs("172.31.255.1"), 9443, "", "172.31.255.1"},
		{"the panel gateway from its owner", addrs("172.31.255.1"), 9443, panel, ""},
		{"a panel network address from the admin exposure", addrs("172.31.255.2"), 9443, admin, "172.31.255.2"},
		{"a reserved second answer", addrs("203.0.113.7", "127.0.0.1"), 8080, "", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckResolved(pxReserved(), host, tc.answers, tc.port, tc.parent)
			if tc.show == "" {
				if err != nil {
					t.Fatalf("CheckResolved = %v, want nil", err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.NPMReserved {
				t.Fatalf("CheckResolved = %v, want npm.reserved", err)
			}
			if want := "forward host " + host + " resolves to " + tc.show + ": "; !strings.HasPrefix(e.Detail, want) {
				t.Errorf("detail %q does not start with %q", e.Detail, want)
			}
		})
	}
}

func TestV1r09LiteralAddr(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string // "" when nginx reads the host as a name
	}{
		{"10.0.0.5", "10.0.0.5"},
		{"127.1", "127.0.0.1"},
		{"0x7f.1", "127.0.0.1"},
		{"2130706433", "127.0.0.1"},
		{"[::1]", "::1"},
		{"::ffff:10.0.0.5", "10.0.0.5"},
		{"web", ""},
		{"web.1", ""},
		{"app.example.com", ""},
		{"LocalHost.", ""},
	} {
		got, ok := LiteralAddr(tc.host)
		if tc.want == "" {
			if ok {
				t.Errorf("LiteralAddr(%q) = %v, true; want a name", tc.host, got)
			}
			continue
		}
		if !ok || got != netip.MustParseAddr(tc.want) {
			t.Errorf("LiteralAddr(%q) = %v, %v; want %s", tc.host, got, ok, tc.want)
		}
	}
}
```

Create `internal/modules/npm/proxyhost_resolve_test.go`. It uses the helpers `paHost`, `paInput`, `paApply`, `paEnv`, `paRefused`, `paJSON` (`proxyhost_add_test.go`) and `puSeed`, `puInput`, `puHosts`, `puEffects`, `puCoded`, `puJSON` (`proxyhost_edit_test.go`).

```go
package npm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// v1r09Resolver answers every name with answers.
func v1r09Resolver(answers ...string) func(context.Context, string) ([]netip.Addr, error) {
	out := make([]netip.Addr, len(answers))
	for i, a := range answers {
		out[i] = netip.MustParseAddr(a)
	}
	return func(context.Context, string) ([]netip.Addr, error) { return out, nil }
}

// v1r09Field asserts that err names the path among its invalid fields.
func v1r09Field(t *testing.T, err error, path string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == path }) {
		t.Errorf("error %v, want an invalid field at %s", err, path)
	}
}

func TestV1r09AddRefusesANameThatResolvesToAReservedAddress(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		answers []string
		port    int
		parent  string
		refuse  bool
	}{
		{"the loopback", []string{"127.0.0.1"}, 81, "", true},
		{"the loopback on another port", []string{"127.0.0.1"}, 8080, "", true},
		{"an IPv4-mapped loopback", []string{"::ffff:127.0.0.1"}, 81, "", true},
		{"the IPv6 loopback", []string{"::1"}, 81, "", true},
		{"NPM's address on its API port", []string{"172.18.0.2"}, 3000, "", true},
		{"the panel gateway", []string{"172.31.255.1"}, 9443, "", true},
		{"a reserved second answer", []string{"203.0.113.7", "127.0.0.1"}, 8080, "", true},
		{"a public answer", []string{"203.0.113.7"}, 8080, "", false},
		{"the loopback from the admin exposure", []string{"127.0.0.1"}, 81, paAdmin, false},
		{"the panel gateway from the panel exposure", []string{"172.31.255.1"}, 9443, paPanel, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, n := paHost(t)
			n.d.resolve = v1r09Resolver(tc.answers...)
			in := paInput("x.example.com")
			in.ForwardHost, in.ForwardPort = "web.example.net", tc.port

			_, planErr := n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", tc.parent), paJSON(t, in))
			_, applyErr := paApply(t, h, n, "c1", tc.parent, in)
			if !tc.refuse {
				if planErr != nil || applyErr != nil {
					t.Fatalf("plan error %v, apply error %v; want both to pass", planErr, applyErr)
				}
				return
			}
			_ = paRefused(t, planErr, errcode.NPMReserved)
			_ = paRefused(t, applyErr, errcode.NPMReserved)
			if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
				t.Error("a refused apply created a proxy host or journaled an effect")
			}
		})
	}
}

func TestV1r09AddRefusesANameWithNoAnswer(t *testing.T) {
	ctx := context.Background()
	for name, resolve := range map[string]func(context.Context, string) ([]netip.Addr, error){
		"a lookup error": func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("lookup web.example.net: no such host")
		},
		"an empty answer": func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			h, n := paHost(t)
			n.d.resolve = resolve
			in := paInput("x.example.com")
			in.ForwardHost = "web.example.net"

			_, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
			_ = paRefused(t, err, errcode.InvalidInput)
			v1r09Field(t, err, "/forwardHost")
			_, err = paApply(t, h, n, "c1", "", in)
			_ = paRefused(t, err, errcode.InvalidInput)
			v1r09Field(t, err, "/forwardHost")
			if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
				t.Error("a refused apply created a proxy host or journaled an effect")
			}
		})
	}
}

func TestV1r09AddResolvesAgainAtApply(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	calls := 0
	answers := v1r09Resolver("203.0.113.7")
	n.d.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		calls++
		return answers(ctx, host)
	}
	in := paInput("x.example.com")
	in.ForwardHost = "web.example.net"

	if _, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in)); err != nil {
		t.Fatal(err)
	}
	planned := calls
	if planned == 0 {
		t.Fatal("the plan did not resolve the forward host")
	}
	answers = v1r09Resolver("127.0.0.1")
	_, err := paApply(t, h, n, "c1", "", in)
	_ = paRefused(t, err, errcode.NPMReserved)
	if calls == planned {
		t.Error("the apply did not resolve the forward host again")
	}
	if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
		t.Error("an apply of a name re-pointed at the loopback created a proxy host or journaled an effect")
	}
}

func TestV1r09AddPlanHashCoversTheSortedAnswerSet(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	hash := func(host string, answers ...string) string {
		n.d.resolve = v1r09Resolver(answers...)
		in := paInput("x.example.com")
		in.ForwardHost = host
		out, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first := hash("web.example.net", "203.0.113.7")
	if again := hash("web.example.net", "203.0.113.7"); again != first {
		t.Errorf("the same answers hash %q, want %q", again, first)
	}
	if other := hash("web.example.net", "203.0.113.8"); other == first {
		t.Error("another answer left the plan hash unchanged")
	}
	both := hash("web.example.net", "203.0.113.7", "203.0.113.8")
	if both == first {
		t.Error("an extra answer left the plan hash unchanged")
	}
	if reversed := hash("web.example.net", "203.0.113.8", "203.0.113.7"); reversed != both {
		t.Errorf("the answers in another order hash %q, want %q", reversed, both)
	}
	if doubled := hash("web.example.net", "203.0.113.7", "203.0.113.7"); doubled != first {
		t.Errorf("a repeated answer hashes %q, want %q", doubled, first)
	}
}

func TestV1r09AddNeverResolvesAnAddressLiteral(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		t.Errorf("resolved %q, which nginx reads as an address", host)
		return nil, errors.New("unexpected lookup")
	}
	for _, host := range []string{"10.0.0.5", "127.1"} {
		in := paInput("x.example.com")
		in.ForwardHost, in.ForwardPort = host, 8080
		if _, err := n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", paAdmin), paJSON(t, in)); err != nil {
			t.Errorf("plan with literal host %q: %v", host, err)
		}
	}
}

func TestV1r09UpdateResolvesForwardHosts(t *testing.T) {
	ctx := context.Background()
	input := func(host string) ProxyHostInput {
		return puInput(func(in *ProxyHostInput) { in.ForwardHost = host })
	}

	t.Run("a reserved answer is refused at plan and at apply", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		h.addChange("c1", "npm.proxyhost.update")
		n := h.nm()
		n.d.resolve = v1r09Resolver("127.0.0.1")
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})
		before := puHosts(t, h)

		_, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
		puCoded(t, err, errcode.NPMReserved)
		_, err = n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), raw)
		puCoded(t, err, errcode.NPMReserved)
		if !core.IsDefinite(err) {
			t.Errorf("apply error %v is not definite, though nothing was committed", err)
		}
		if got := puHosts(t, h); !reflect.DeepEqual(got, before) || len(puEffects(t, h)) != 0 {
			t.Errorf("a refusal changed NPM to %+v or journaled %+v", got, puEffects(t, h))
		}
	})

	t.Run("a name with no answer is invalid input on forwardHost", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		h.addChange("c1", "npm.proxyhost.update")
		n := h.nm()
		n.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})

		_, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
		puCoded(t, err, errcode.InvalidInput)
		v1r09Field(t, err, "/forwardHost")
		_, err = n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), raw)
		puCoded(t, err, errcode.InvalidInput)
		v1r09Field(t, err, "/forwardHost")
	})

	t.Run("the plan hash covers the answer set", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		n := h.nm()
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})
		hash := func(answers ...string) string {
			n.d.resolve = v1r09Resolver(answers...)
			out, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			return out.StateHash
		}
		first := hash("203.0.113.7")
		if again := hash("203.0.113.7"); again != first {
			t.Errorf("the same answers hash %q, want %q", again, first)
		}
		if other := hash("203.0.113.8"); other == first {
			t.Error("another answer left the plan hash unchanged")
		}
		if both, reversed := hash("203.0.113.7", "203.0.113.8"), hash("203.0.113.8", "203.0.113.7"); both != reversed {
			t.Errorf("the answers in another order hash %q, want %q", reversed, both)
		}
	})
}

func TestV1r09ProductionResolverIsAbsentInATestBinary(t *testing.T) {
	got := reflect.ValueOf(productionNM().d.resolve)
	if !got.IsValid() || got.IsNil() {
		t.Fatal("productionNM wires no resolver")
	}
	if want := reflect.ValueOf(absentResolve); got.Pointer() != want.Pointer() {
		t.Error("a test binary's production module would resolve names through the host's resolver")
	}
}
```

In `internal/modules/npm/testhost_test.go`, `thHost.nm` builds the module's `deps`. Add the field `resolve: func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil },` and extend its doc comment with "and every name resolving to 203.0.113.7". That keeps every existing test that forwards to a name (`web`, `app`, `api`) off the network. `net/netip` is probably already imported there. Check.

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10b-tmp/9 && export TMPDIR=/root/.cache/w10b-tmp/9 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r09' ./internal/modules/npm/proxy/
timeout 600 go test -count=1 -run 'TestV1r09' ./internal/modules/npm/
```

Expected: FAIL at build. The first names `undefined: CheckResolved` and `undefined: LiteralAddr`. The second names `n.d.resolve undefined (type deps has no field or method resolve)` among its errors. Quote both.

- [ ] **Step 3: Implement**

1. `internal/modules/npm/proxy/upstream.go`:
   - `LiteralAddr(host)` returns `upstreamAddr(host, normaliseHost(host))`. Give it a doc comment: it reports whether nginx reads host as an address, so the caller knows a lookup would be pointless.
   - `CheckResolved(r, host, answers, port, parent)` walks `answers` in order. For each, it calls `CheckUpstream(r, plainAddr(a).String(), port, parent)`. On a refusal it returns `errcode.New(errcode.NPMReserved, "forward host "+host+" resolves to "+<that address text>+": "+<the refusal's Detail>)`. With no answers or no refusal it returns nil. It returns the first refusal only.
   - Adjust the last sentence of `CheckUpstream`'s doc comment: a name other than the loopback names and NPM's names passes here, and the caller resolves it and checks each answer with `CheckResolved`.
2. `internal/modules/npm/deps.go`:
   - Add the field `resolve func(ctx context.Context, host string) ([]netip.Addr, error)` to `deps`, with a trailing comment in the style of its neighbours ("the host's resolver"). The struct's comment alignment is gofmt's.
   - `resolveVia(r *net.Resolver)` returns a function that calls `r.LookupNetIP(ctx, "ip", host)` and returns its answer and error unchanged. Add `net` to the imports.
   - `absentResolve(ctx, host)` is a top-level function that returns `nil` and an error whose text starts `npm:` and says no resolver is available in a test binary. It never touches the network.
   - `productionNM`: next to the existing `testing.Testing()` branch that picks `absentSocket` and `absentURL`, choose `resolveVia(net.DefaultResolver)` normally and `absentResolve` when `testing.Testing()`. Set `resolve:` in the `deps` literal. Explain in a comment that a test binary must never reach the host's DNS.
3. `internal/modules/npm/proxyhost_add.go`:
   - Split today's `checkUpstream`. `reservedSet(ctx, env)` holds everything before the `proxy.CheckUpstream` call: `n.d.panel`, the container inspection, the clones and the collected addresses and names; it returns the `proxy.Reserved` and the same errors as before (a missing container is not an error; any other Engine error goes through `engineCoded`). `checkUpstream` becomes: `reservedSet`, then `proxy.CheckUpstream` with `env.Call.ParentAction`, a refusal wrapped by `core.Definite`. Its behaviour and doc comment stay.
   - Add `checkForward(ctx, env, in)`:
     1. `r` from `reservedSet`, then `proxy.CheckUpstream` as `checkUpstream` does (Definite on refusal), so a loopback name, an NPM name or a literal is refused exactly as before.
     2. When `proxy.LiteralAddr(in.ForwardHost)` is true, return `[]string{}` and no error. No lookup happens.
     3. Otherwise call `n.d.resolve` under a context with a 5 second timeout (a named constant with a comment). If the outer `ctx` has ended (`ctx.Err() != nil`), return `ctx.Err()` unchanged and not Definite. Any other error, or an empty answer, is `proxyhostAddInvalid` with one `errcode.FieldError{Path: "/forwardHost", Message: ...}` saying the name does not resolve to an address on this host. The message names the host but not the resolver's error text. It is Definite, as `proxyhostAddInvalid` already makes it.
     4. Normalise every answer with `Unmap()` and `WithZone("")`, sort with `netip.Addr.Compare`, remove duplicates with `slices.Compact`.
     5. `proxy.CheckResolved(r, in.ForwardHost, answers, in.ForwardPort, env.Call.ParentAction)`; a refusal is Definite.
     6. Return the answers as `[]string` (`Addr.String()`), never nil.
   - `proxyhostAddFound` gains `Resolved []string` tagged `json:"resolved"`. `check` calls `checkForward` instead of `checkUpstream` and stores the answers in `Resolved`. `Plan` already hashes the whole struct, and `Apply` already calls `check` again, which is the second resolution.
4. `internal/modules/npm/proxyhost_edit.go`, `proxyHostUpdateHandler`:
   - `check` calls `checkForward` where it called `checkUpstream` and returns the answer set as one more result.
   - `Plan` appends the answer set to the parts of `proxyHostEditSum`: a final part that is a newline followed by the answers joined by `,`. `Apply` ignores the extra result.
   - `proxyHostRemoveHandler.Inverse` keeps calling `checkUpstream`. A removal's undo recreates the host the operator removed and asks no resolver.
5. The wire types, the schemas and `docs/api` do not change. `make schemas` is not needed.

- [ ] **Step 4: Run test to verify it passes**

```sh
mkdir -p /root/.cache/w10b-tmp/9 && export TMPDIR=/root/.cache/w10b-tmp/9 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r09|TestPx|FuzzCheckUpstream' ./internal/modules/npm/proxy/
timeout 600 go test -count=1 -run 'TestV1r09|TestPa|TestPu' ./internal/modules/npm/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 2400 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/9 && go clean -testcache
```

Expected: every line `ok`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and `git status --short` lists only this task's files. No command of this step resolves a real name: `absentResolve` and the fakes answer every lookup.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/proxy/upstream.go internal/modules/npm/proxy/upstream_test.go internal/modules/npm/deps.go internal/modules/npm/proxyhost_add.go internal/modules/npm/proxyhost_edit.go internal/modules/npm/proxyhost_resolve_test.go internal/modules/npm/testhost_test.go
git commit -m "fix(npm): resolve forward hosts at plan and apply"
```

### Task 10: Fingerprint NPM configurations in effects and keep advanced config

**Files:**
- Modify: `internal/modules/npm/effects.go` (add the fingerprint helper and the secret-carrying journal wrapper next to `nm.run`, which stays as it is)
- Modify: `internal/modules/npm/proxyhost_edit.go` (`proxyHostEditState` at line 60, `proxyHostEditDoc`, `undoBody`, both `Plan` hashes, both `Apply` effects at lines 336 and 525, both `Inverse` functions, the update body)
- Modify: `internal/modules/npm/proxyhost_add.go` (`proxyHostState` and `proxyhostAddDoc`, lines 199-231, and their two callers in `Observe`)
- Test: `internal/modules/npm/proxyhost_effects_test.go` (create)
- Test: `internal/modules/npm/proxyhost_edit_test.go` (five existing assertions and one fabricated effect)
- Test: `internal/modules/npm/proxyhost_add_test.go` (`TestPaState` and four `proxyHostState` calls)
- Read only: `internal/core/lifecycle/effects.go` (an effect's sealed secret: `effectSecretAD` near line 47, `Secret` near line 229, and `setEffect`, which clears the secret when the effect ends), `internal/controller/api/changes.go` (line 201: `handleEffects` serves effect documents at scope `read`)

**Interfaces:**
- Consumes: Contracts "Keyed fingerprints", NPM label `npm.effect.fingerprint/v1` and the rule that a value needed to restore the before-state goes into the effect's sealed secret. `env.Sealer.Derive`. `core.EffectJournal.Intend(ctx, e, secret)` and `.Secret(ctx, seq)`.
- Produces, unexported:
  - `npmFingerprint(env core.Env, value string) string`: `hmac-sha256:` followed by the lower-case hex of `HMAC-SHA256(env.Sealer.Derive("npm.effect.fingerprint/v1"), value)`.
  - `withEffectSecret(env core.Env, secret []byte) core.Env`: a copy of `env` whose journal seals `secret` with the next effect intended through it. `nm.run` is unchanged.
  - `proxyHostEditState(env, body)`, `proxyHostEditDoc(env, host)`, `proxyHostState(env, host)` and `proxyhostAddDoc(env, host)`: every advanced configuration, the host's and each custom location's, is a fingerprint.
  - `undoBody(ctx, env, e)`: the body to restore, read from the effect's sealed secret.

**Items:** 2026-11-06-v1-security-review#4, 2026-11-06-v1-security-review#15

**Depends on:** Task 9
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

The findings:

- Row 4: `npm.proxyhost.update` and `npm.proxyhost.remove` store the host's complete body, advanced configuration and custom locations included, as the effect's `before_json`. `GET` of a change's effects serves it at scope `read`. An administrator who put `proxy_set_header Authorization "Bearer ..."` into NPM leaks that header to a `read` token. The observations (`current_json`, `post_json`) leak the same way.
- Row 15: `npm.proxyhost.update` sends a body with no host-level advanced configuration and only the module's own `/` location, so it silently erases what the operator wrote in NPM.

Decisions (rulings W10b-R5 and W10b-R7):

- An effect document never holds an advanced configuration. It holds its keyed fingerprint. The documents are the effect's `Before`, `After`, `Post` and `Current` and the observation's candidates, for `npm.proxyhost.update`, `.remove` and `.add`. Every document of one host goes through the same function, so the engine's equality checks (it compares `State`) still work.
- The body that an update's or removal's undo sends is sealed with the effect (`Intend`'s secret argument) and read back by the inverse. An inverse never restores from the document. Without a sealed body it refuses with a Definite error.
- `npm.proxyhost.update` keeps the host-level advanced configuration and every custom location whose path is not `/`, exactly as NPM holds them. The `/` location is the module's own: it is rebuilt from the input, as before. `ProxyHostInput` has no advanced-configuration field, so no wire type changes.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/npm/proxyhost_effects_test.go`. It uses `puSeed`-style fixtures from `proxyhost_edit_test.go` (`puInput`, `puHostOf`, `puBodyOf`, `puHosts`, `puEffects`, `puJSON`, `puStateBody`, `puSameJSON`) and `paHost`, `paInput`, `paApply` from `proxyhost_add_test.go`.

```go
package npm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const (
	v1r10HostSecret = "placeholder-secret-host"
	v1r10LocSecret  = "placeholder-secret-location"
)

func v1r10Env() core.Env { return core.Env{Sealer: coretest.Sealer{}} }

// v1r10Fingerprint computes the documented fingerprint without the module's helper.
func v1r10Fingerprint(value string) string {
	m := hmac.New(sha256.New, coretest.Sealer{}.Derive("npm.effect.fingerprint/v1"))
	m.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// v1r10Doc is b as an effect document holds it: every advanced configuration replaced by its fingerprint.
func v1r10Doc(b npmapi.ProxyHostBody) npmapi.ProxyHostBody {
	b.AdvancedConfig = v1r10Fingerprint(b.AdvancedConfig)
	b.Locations = append([]npmapi.Location(nil), b.Locations...)
	for i := range b.Locations {
		b.Locations[i].AdvancedConfig = v1r10Fingerprint(b.Locations[i].AdvancedConfig)
	}
	return b
}

// v1r10NoSecret fails when the JSON of v holds either placeholder secret.
func v1r10NoSecret(t *testing.T, what string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{v1r10HostSecret, v1r10LocSecret} {
		if strings.Contains(string(b), s) {
			t.Errorf("%s holds %q: %s", what, s, b)
		}
	}
}

// v1r10Seed is a host with a deployed instance and one proxy host whose NPM configuration carries a placeholder
// credential at the host level and in a custom location the operator wrote.
func v1r10Seed(t *testing.T) (*thHost, npmapi.ProxyHost) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.AdvancedConfig = `proxy_set_header Authorization "Bearer ` + v1r10HostSecret + `";`
	body.Locations = append(body.Locations, npmapi.Location{
		Path: "/api", ForwardScheme: "http", ForwardHost: "app", ForwardPort: 9000,
		AdvancedConfig: "proxy_set_header X-Api-Key " + v1r10LocSecret + ";",
	})
	return h, h.npm.SeedProxyHost(puHostOf(body))
}

// v1r10Sealed decodes the body sealed with effect seq.
func v1r10Sealed(t *testing.T, h *thHost, seq int) npmapi.ProxyHostBody {
	t.Helper()
	raw, err := h.journal.Secret(context.Background(), seq)
	if err != nil || len(raw) == 0 {
		t.Fatalf("effect %d has no sealed body: %q, %v", seq, raw, err)
	}
	var b npmapi.ProxyHostBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("the sealed body of effect %d is not a proxy host body: %v", seq, err)
	}
	return b
}

type v1r10OtherKey struct{ coretest.Sealer }

func (v1r10OtherKey) Derive(label string) []byte { return []byte("another role key for " + label) }

func TestV1r10Fingerprint(t *testing.T) {
	env := v1r10Env()
	got := npmFingerprint(env, "placeholder-secret")
	if want := v1r10Fingerprint("placeholder-secret"); got != want {
		t.Errorf("npmFingerprint = %q, want %q", got, want)
	}
	if digits, ok := strings.CutPrefix(got, "hmac-sha256:"); !ok || len(digits) != 64 || strings.ToLower(digits) != digits {
		t.Errorf("fingerprint %q is not hmac-sha256: and 64 lower-case hex digits", got)
	}
	if other := npmFingerprint(env, "another-secret"); other == got {
		t.Error("two values share a fingerprint")
	}
	other := core.Env{Sealer: v1r10OtherKey{}}
	if npmFingerprint(other, "placeholder-secret") == got {
		t.Error("the fingerprint does not depend on the role key")
	}
}

func TestV1r10UpdateEffectsHoldFingerprintsAndTheSealedBody(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	h.addChange("c1", "npm.proxyhost.update")
	def := h.nm().proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})

	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	if _, err := def.Handler.Apply(ctx, h.env("c1"), raw); err != nil {
		t.Fatal(err)
	}
	effects := puEffects(t, h)
	if len(effects) != 1 {
		t.Fatalf("journal %+v, want one effect", effects)
	}
	e := effects[0]
	v1r10NoSecret(t, "the effect", e)
	if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, v1r10Doc(puBodyOf(host))) {
		t.Errorf("before %+v, want the body the host had with every advanced configuration fingerprinted: %+v", got, v1r10Doc(puBodyOf(host)))
	}
	if got := v1r10Sealed(t, h, e.Seq); !reflect.DeepEqual(got, puBodyOf(host)) {
		t.Errorf("sealed body %+v, want the body the host had: %+v", got, puBodyOf(host))
	}

	e.UndoTo = &core.StateDoc{State: e.Before.State}
	if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
		t.Fatal(err)
	}
	if hosts := puHosts(t, h); len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) {
		t.Errorf("NPM holds %+v after the undo, want the original body with its advanced configuration: %+v", hosts, puBodyOf(host))
	}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	if !puSameJSON(obs.Current.State, e.UndoTo.State) {
		t.Errorf("observed %s after the undo, want the before document %s", obs.Current.State, e.UndoTo.State)
	}
}

func TestV1r10UpdateKeepsTheOperatorsAdvancedConfig(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	h.addChange("c1", "npm.proxyhost.update")
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	def := h.nm().proxyhostUpdate()
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	want := proxyHostBody(next, "")
	want.AdvancedConfig = host.AdvancedConfig
	want.Locations = append(want.Locations, host.Locations[1])
	hosts := puHosts(t, h)
	if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), want) {
		t.Errorf("NPM holds %+v, want the module's / location for the new port and the operator's advanced configuration and /api location kept: %+v", hosts, want)
	}
}

func TestV1r10RemoveEffectsHoldFingerprintsAndTheSealedBody(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	h.addChange("c1", "npm.proxyhost.remove")
	def := h.nm().proxyhostRemove()
	raw := puJSON(t, ProxyHostRemoveInput{ID: host.ID})

	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	if _, err := def.Handler.Apply(ctx, h.env("c1"), raw); err != nil {
		t.Fatal(err)
	}
	e := puEffects(t, h)[0]
	v1r10NoSecret(t, "the effect", e)
	if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, v1r10Doc(puBodyOf(host))) {
		t.Errorf("before %+v, want the body the host had with every advanced configuration fingerprinted: %+v", got, v1r10Doc(puBodyOf(host)))
	}
	if got := v1r10Sealed(t, h, e.Seq); !reflect.DeepEqual(got, puBodyOf(host)) {
		t.Errorf("sealed body %+v, want the body the host had: %+v", got, puBodyOf(host))
	}

	e.UndoTo = &core.StateDoc{State: e.Before.State}
	res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	hosts := puHosts(t, h)
	if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) || res.Resource != "npm.proxyhost:"+strconv.Itoa(hosts[0].ID) {
		t.Fatalf("inverse = %+v; NPM holds %+v, want one host with the removed body, advanced configuration included", res, hosts)
	}
	e.State, e.UndoResource = core.EffectUndoing, res.Resource
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	if !puSameJSON(obs.Current.State, e.UndoTo.State) {
		t.Errorf("observed %s after the undo, want the before document %s", obs.Current.State, e.UndoTo.State)
	}
}

func TestV1r10InverseNeverRestoresFromTheDocument(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		kind core.EffectKind
		def  func(nm) core.ActionDef
	}{
		{"update", core.EffectModify, func(n nm) core.ActionDef { return n.proxyhostUpdate() }},
		{"remove", core.EffectDelete, func(n nm) core.ActionDef { return n.proxyhostRemove() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, host := v1r10Seed(t)
			h.env("c1")
			body := puBodyOf(host)
			body.ForwardPort = 7777
			doc := core.StateDoc{State: puJSON(t, body)}
			e := core.Effect{
				ChangeID: "c1", Seq: 1, Kind: tc.kind, Resource: "npm.proxyhost:" + strconv.Itoa(host.ID), State: core.EffectMade,
				Before: doc, UndoTo: &doc,
			}
			before := puHosts(t, h)

			_, err := tc.def(h.nm()).Handler.Inverse(ctx, h.env("c1"), e)
			if err == nil || !core.IsDefinite(err) {
				t.Fatalf("inverse without a sealed body = %v, want a definite refusal", err)
			}
			if got := puHosts(t, h); !reflect.DeepEqual(got, before) {
				t.Errorf("a refused inverse changed NPM to %+v", got)
			}
		})
	}
}

func TestV1r10AddObservationHoldsFingerprints(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	view, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	api, err := n.d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	body := proxyHostBody(paInput("a.example.com"), "")
	body.AdvancedConfig = `proxy_set_header Authorization "Bearer ` + v1r10HostSecret + `";`
	if _, err := api.UpdateProxyHost(ctx, view.ID, body); err != nil {
		t.Fatal(err)
	}
	e := h.journal.effects()[0]
	obs, err := n.proxyhostAdd().Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	var doc struct {
		AdvancedConfig string `json:"advancedConfig"`
	}
	if err := json.Unmarshal(obs.Current.State, &doc); err != nil || doc.AdvancedConfig != v1r10Fingerprint(body.AdvancedConfig) {
		t.Errorf("observed advanced configuration %q (%v), want the fingerprint of what NPM holds", doc.AdvancedConfig, err)
	}
}
```

Fix the existing assertions that read effect documents as plain bodies. They now see fingerprints.

In `internal/modules/npm/proxyhost_edit_test.go`:

- `TestPuUpdateAppliesTheInput`: change `reflect.DeepEqual(got, puBodyOf(host))` (the `e.Before.State` check, the message too) to `reflect.DeepEqual(got, v1r10Doc(puBodyOf(host)))`, and `reflect.DeepEqual(got, wantBody)` (the `e.After.State` check) to `reflect.DeepEqual(got, v1r10Doc(wantBody))`.
- `TestPuObserveReadsTheHostById`: in the first `obs` condition replace `puBodyOf(host)` with `v1r10Doc(puBodyOf(host))`.
- `TestPuRemoveDeletesTheHost`: in the `e.Before.State` check replace `puBodyOf(host)` with `v1r10Doc(puBodyOf(host))`.
- `TestPuRemoveInverseRefusesAReservedUpstream`: the inverse now reads the sealed body, so journal the effect with its body instead of fabricating a document. Replace the construction of `e` (the `e := core.Effect{...}` statement of three lines) with:

```go
	h.env("c1")
	seq, err := h.journal.Intend(ctx, core.Effect{Kind: core.EffectDelete, Resource: "npm.proxyhost:4242"}, puJSON(t, body))
	if err != nil {
		t.Fatal(err)
	}
	e := core.Effect{ChangeID: "c1", Seq: seq, Kind: core.EffectDelete, Resource: "npm.proxyhost:4242", State: core.EffectMade}
```

In `internal/modules/npm/proxyhost_add_test.go`:

- `TestPaState`: call `proxyHostState(v1r10Env(), host)` in both places. In the expected map, the location's `"advancedConfig": "x"` becomes `"advancedConfig": v1r10Fingerprint("x")` and the host's `"advancedConfig": ""` becomes `"advancedConfig": v1r10Fingerprint("")`.
- In `TestPaApply` (the `e.Post` check), the observe-by-domain test (`obs.Current.State` check) and `TestPaObserveMade`, replace `proxyHostState(created)`, `proxyHostState(hosts[0])` and `proxyHostState(h.npm.ProxyHosts()[0])` by the same call with `h.env("")` as first argument: `proxyHostState(h.env(""), created)` and so on.

- [ ] **Step 2: Run test to verify it fails**

```sh
mkdir -p /root/.cache/w10b-tmp/10 && export TMPDIR=/root/.cache/w10b-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r10|TestPu|TestPa' ./internal/modules/npm/
```

Expected: FAIL at build, naming `undefined: npmFingerprint` and `too many arguments in call to proxyHostState` among its errors. Quote it.

- [ ] **Step 3: Implement**

1. `internal/modules/npm/effects.go`:
   - Add the constant `npmFingerprintLabel = "npm.effect.fingerprint/v1"`.
   - `npmFingerprint(env, value)` computes `HMAC-SHA256(env.Sealer.Derive(npmFingerprintLabel), value)` and returns `"hmac-sha256:"` plus the lower-case hex digest. Doc comment: why a keyed fingerprint (an effect document is readable at scope `read`, and a plain hash of a low-entropy value can be guessed offline).
   - `sealedJournal` embeds `core.EffectJournal` and holds `secret []byte`. Its `Intend(ctx, e, _ []byte)` calls the embedded `Intend` with the held secret. `withEffectSecret(env, secret)` returns a copy of `env` whose `Effects` is a `sealedJournal` over `env.Effects`. A comment says it exists so that `nm.run`, whose signature many actions use, seals a secret without changing.
   - Leave `nm.run`, `npmEffect` and every other function in the file as they are.
2. `internal/modules/npm/proxyhost_edit.go`:
   - `proxyHostEditState(env, b)`: copy `b`; sort the domains as today; replace `AdvancedConfig` of the body and of each location of a cloned `Locations` slice by `npmFingerprint(env, value)`; marshal. The caller's slices are not modified. Rewrite its doc comment: it is the effect document of a body, with fingerprints, never a body to restore.
   - `proxyHostEditDoc(env, h)` uses it; `Observe` passes its `env`. Its three call sites in `Observe` change.
   - `undoBody(ctx, env, e)` replaces the document reading. It calls `env.Effects.Secret(ctx, e.Seq)`. An error, an empty secret or a secret that is not a JSON proxy host body is a `core.Definite` error whose text names the effect's seq and change and never the body. It returns the decoded `npmapi.ProxyHostBody`. Both `Inverse` functions call it with `ctx` and `env`; nothing reads `e.Before.State` or `e.UndoTo` to restore any more.
   - Update `Apply`: the effect's `Before` is `proxyHostEditState(env, proxyHostEditBody(host))`, its `After` is `proxyHostEditState(env, body)`. The secret is `json.Marshal(proxyHostEditBody(host))` (marshal cannot fail on this type; ignore the error as the neighbouring code does). Journal through `h.n.run(ctx, withEffectSecret(env, secret), e, h.Observe, ...)`.
   - Remove `Apply`: the same for `Before` and the secret, `After` stays the null state.
   - Update `Plan` and Remove `Plan`: every `proxyHostEditState(...)` in the hash input becomes `proxyHostEditState(env, ...)`. Update's hash keeps the answer set Task 9 added as its last part.
   - Update body: after `proxyHostBody(in.ProxyHostInput, env.Call.ParentAction)`, a new unexported function `proxyHostEditKeep(body, host)` returns the body with `AdvancedConfig` set to `host.AdvancedConfig` and the host's locations whose `Path != "/"`, in NPM's order and unchanged, appended after the module's `/` location. `check` returns that body, so `Plan` (hash, diff) and `Apply` see it. The diff lines do not change.
3. `internal/modules/npm/proxyhost_add.go`: `proxyHostState(env, h)` and `proxyhostAddDoc(env, h)` take the env and fingerprint `AdvancedConfig` of the host and of each location with `npmFingerprint`. The two call sites in `Observe` pass `env`. Nothing else in the add action changes (its `Inverse` deletes the host and needs no sealed body).
4. Wire types, schemas, `docs/api`: unchanged.
5. Nothing logs or returns a configuration. Error texts name the effect and the host id only.

- [ ] **Step 4: Run test to verify it passes**

```sh
mkdir -p /root/.cache/w10b-tmp/10 && export TMPDIR=/root/.cache/w10b-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r10|TestPu|TestPa' ./internal/modules/npm/
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 2400 go test -race -count=1 ./internal/modules/npm/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/10 && go clean -testcache
```

Expected: every line `ok`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`, and `git status --short` lists only this task's files (`schemas/**` and `docs/api/**` unchanged, so `TestAdAPIDocsCurrent` stays green).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/effects.go internal/modules/npm/proxyhost_edit.go internal/modules/npm/proxyhost_add.go internal/modules/npm/proxyhost_effects_test.go internal/modules/npm/proxyhost_edit_test.go internal/modules/npm/proxyhost_add_test.go
git commit -m "fix(npm): protect proxy host config in effects"
```

### Task 11: Drop NPM's answer text from errors and logs

**Files:**
- Modify: `internal/modules/npm/npmapi/errors.go` (`APIError`, `Error`)
- Modify: `internal/modules/npm/npmapi/client.go` (`apiError`, the doc comment of `do`; `Token` and every other call keep their code)
- Modify: `internal/modules/npm/proxyhost_add.go` (`proxyhostAddAnswer` and its doc comment, found by the quoted string `Nginx Proxy Manager refused the request (HTTP %d): %s`)
- Read only: `internal/modules/npm/deps.go` (`apiVia`, where a failed `c.Token` becomes the cause of `npm.unhealthy`; it changes nothing once the client keeps no text), `internal/agent/api/handler.go` (`writeProblem` logs the whole cause chain of a 5xx)
- Test: `internal/modules/npm/npmapi/errors_test.go` (replace the first two tests), `internal/modules/npm/answer_text_test.go` (create)
- Test, one-line edits that delete the `Message` member: `internal/modules/npm/npmapi/client_test.go`, `internal/modules/npm/fake/objects_test.go`, `internal/modules/npm/effects_test.go`, `internal/modules/npm/certificate_test.go`, `internal/modules/npm/instance_deploy_test.go`, `internal/modules/npm/proxyhost_edit_test.go`, `internal/modules/npm/accesslist_test.go`. Find every such place with `rg -n 'APIError\{' --glob '*_test.go' internal/modules/npm`; a test file that line finds and this list lacks is edited the same way.

**Interfaces:**
- Consumes: nothing from Contracts. Task 9 edited `proxyhost_add.go` and `deps.go` earlier in this plan, so find the function by name and quoted text, not by line.
- Produces (no Contracts entry; nothing else in this plan reads it): `npmapi.APIError` becomes `struct { Status int; Code int }`. `Message` is removed. `Code` is NPM's `error.code` when it is a JSON number from 400 to 599, and `0` otherwise. `IsNotFound` and `IsClientError` keep their behaviour. `Error()` is `npmapi: NPM answered <Status>`, followed by ` (error code <Code>)` only when `Code` is not 0 and differs from `Status`. No `APIError` text, field or cause holds any part of NPM's `error.message` or body.

**Items:** 2026-11-06-v1-security-review#10
**Depends on:** Task 9
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

The failure this fixes (Codex F4): a compromised NPM receives the stored administrator password in `POST /api/tokens` and answers with that password inside `error.message`. The client kept the message as `APIError.Message`, `APIError.Error()` printed it, and `apiVia` wrapped the error as the cause of `npm.unhealthy`, which the agent logs for a 5xx. A 4xx message was also copied into the `invalid_input` detail of `proxyhostAddAnswer`, which reaches change records. NPM's bearer token leaks the same way through later errors.

- [ ] **Step 1: Write the failing tests**

Replace `TestApErrorsCarryNPMsMessageAndNeverTheBody` and `TestApAPIErrorText` in `internal/modules/npm/npmapi/errors_test.go` (the first two test functions of the file) with the two functions below. Keep the rest of the file, and change one literal in `TestApPredicatesSeeThroughWrappingAndIgnoreOtherErrors`: `&APIError{Status: 404, Message: "Not Found"}` becomes `&APIError{Status: 404}`. The imports of the file stay as they are, because the other tests still use `context`, `errors`, `fmt` and `strings`. `apStart` is the helper `client_test.go` already declares.

```go
func TestV1r11AnswersKeepOnlyTheStatusAndAnAllowlistedCode(t *testing.T) {
	const secret = "placeholder-secret"
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		code     int
		text     string
		notFound bool
		client   bool
	}{
		{"a refused duplicate", 400, `{"error":{"code":400,"message":"placeholder-secret is already in use"}}`, 400, "npmapi: NPM answered 400", false, true},
		{"a missing object", 404, `{"error":{"code":404,"message":"placeholder-secret"}}`, 404, "npmapi: NPM answered 404", true, true},
		{"an unauthorized call", 401, `{"error":{"code":401,"message":"placeholder-secret"}}`, 401, "npmapi: NPM answered 401", false, true},
		{"a server failure", 500, `{"error":{"code":500,"message":"placeholder-secret"}}`, 500, "npmapi: NPM answered 500", false, false},
		{"a code that differs from the status", 403, `{"error":{"code":400,"message":"placeholder-secret"}}`, 400, "npmapi: NPM answered 403 (error code 400)", false, true},
		{"a code outside the allowlist", 400, `{"error":{"code":12345,"message":"placeholder-secret"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"a code below the allowlist", 400, `{"error":{"code":200,"message":"placeholder-secret"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"a code that is text", 400, `{"error":{"code":"placeholder-secret","message":"x"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"an HTML body from a proxy", 502, `<html>token=placeholder-secret</html>`, 0, "npmapi: NPM answered 502", false, false},
		{"an empty body", 503, ``, 0, "npmapi: NPM answered 503", false, false},
		{"JSON without an error member", 400, `{"password":"placeholder-secret"}`, 0, "npmapi: NPM answered 400", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apStart(t, tc.status, tc.body)
			err := s.client().DeleteProxyHost(t.Context(), 3)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != tc.status || ae.Code != tc.code {
				t.Fatalf("error %#v, want an *APIError with status %d and code %d", err, tc.status, tc.code)
			}
			if got := err.Error(); got != tc.text {
				t.Errorf("error text %q, want %q", got, tc.text)
			}
			for _, shown := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(shown, secret) {
					t.Errorf("%q carries the answer text", shown)
				}
			}
			if IsNotFound(err) != tc.notFound || IsClientError(err) != tc.client {
				t.Errorf("IsNotFound %t and IsClientError %t, want %t and %t", IsNotFound(err), IsClientError(err), tc.notFound, tc.client)
			}
		})
	}
}

func TestV1r11APIErrorText(t *testing.T) {
	for _, tc := range []struct {
		err  APIError
		want string
	}{
		{APIError{Status: 400}, "npmapi: NPM answered 400"},
		{APIError{Status: 500, Code: 500}, "npmapi: NPM answered 500"},
		{APIError{Status: 403, Code: 400}, "npmapi: NPM answered 403 (error code 400)"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%+v: Error() = %q, want %q", tc.err, got, tc.want)
		}
	}
}
```

Edit three existing assertions that read `Message`, so the files compile with and without the change:

- `internal/modules/npm/npmapi/client_test.go`, `TestW10NcBodiesAreDrainedWithinALimit`: the condition `!errors.As(err, &ae) || ae.Status != tt.status || ae.Message != "boom"` becomes `!errors.As(err, &ae) || ae.Status != tt.status`, and its message becomes `error %#v, want an APIError %d`, with `tt.status` as the only argument.
- `internal/modules/npm/fake/objects_test.go`, `TestFkProxyHostDomainsAreUnique`: the condition `ae.Status != http.StatusBadRequest || ae.Message != "b.example.com is already in use" || !npmapi.IsClientError(err)` becomes `ae.Status != http.StatusBadRequest || !npmapi.IsClientError(err)`, and its message becomes `a create with a taken domain: %v, want 400`.
- In the five package-`npm` test files named under Files, delete the member `, Message: "<text>"` from every `npmapi.APIError` literal (`effects_test.go` `npmErr`, `certificate_test.go`, `instance_deploy_test.go` `dpHealth.Health`, `proxyhost_edit_test.go` three places, `accesslist_test.go`). Leave every `dockerapi.APIError` literal alone: its `Message` member stays.

Create `internal/modules/npm/answer_text_test.go`:

```go
package npm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// v1r11Reflecting starts an NPM that answers every request with status and puts the request's body and bearer token
// into error.message, as a compromised NPM would to leak what it was sent.
func v1r11Reflecting(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": status, "message": string(b) + " " + r.Header.Get("Authorization"),
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// v1r11Chain fails the test when err, or any error it wraps, names one of the secrets.
func v1r11Chain(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for e := err; e != nil; e = errors.Unwrap(e) {
		for _, s := range secrets {
			if strings.Contains(e.Error(), s) {
				t.Errorf("error %q carries %q", e.Error(), s)
			}
		}
	}
}

func TestV1r11LoginFailureCarriesNoNPMText(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		h := newTHHost(t)
		inst := h.deployed()
		srv := v1r11Reflecting(t, status)
		n := h.nm()
		n.d.api = apiVia(func() *npmapi.Client { return npmapi.New(srv.URL, nil) })
		_, err := n.d.api(ctx, h.env(""))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
			t.Fatalf("status %d: error %v, want %s", status, err, errcode.NPMUnhealthy)
		}
		v1r11Chain(t, err, inst.Password)
	}
}

func TestV1r11MutationFailureCarriesNoNPMText(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		status int
		code   errcode.Code
		detail string
	}{
		{http.StatusBadRequest, errcode.InvalidInput, "Nginx Proxy Manager refused the request (HTTP 400)"},
		{http.StatusInternalServerError, errcode.NPMUnhealthy, "Nginx Proxy Manager did not answer"},
	} {
		srv := v1r11Reflecting(t, tc.status)
		c := npmapi.New(srv.URL, nil).WithToken("placeholder-token")
		_, err := c.CreateProxyHost(ctx, npmapi.ProxyHostBody{
			DomainNames: []string{"app.example.com"}, ForwardHost: "203.0.113.10", ForwardPort: 8080,
			AdvancedConfig: "placeholder-secret",
		})
		answered := proxyhostAddAnswer(err)
		var coded *errcode.Error
		if !errors.As(answered, &coded) || coded.Code != tc.code {
			t.Fatalf("status %d: error %v, want %s", tc.status, answered, tc.code)
		}
		if coded.Detail != tc.detail {
			t.Errorf("status %d: detail %q, want %q", tc.status, coded.Detail, tc.detail)
		}
		v1r11Chain(t, answered, "placeholder-token", "placeholder-secret", "app.example.com")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/11 && export TMPDIR=/root/.cache/w10b-tmp/11 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r11' ./internal/modules/npm/npmapi/ ./internal/modules/npm/
```

Expected: `./internal/modules/npm/npmapi` fails to build with `ae.Code undefined (type *APIError has no field or method Code)`. Run the second package alone to see the assertion failures: `timeout 300 go test -count=1 -run 'TestV1r11' ./internal/modules/npm/` fails with `error "npm.unhealthy: ... NPM answered 500: ..."` carrying the stored password, and with the detail `Nginx Proxy Manager refused the request (HTTP 400): ...` carrying the reflected token. Quote both outputs.

- [ ] **Step 3: Implement**

1. `npmapi/errors.go`, `APIError`: replace the member `Message string` with `Code int`. Rewrite its doc comment: the type is NPM's refusal of a request; it holds the HTTP status and NPM's `error.code` when that is an allowlisted number, and never any text of the answer, because a compromised NPM can place a secret it was sent in the text. `Error()` returns `npmapi: NPM answered <Status>`, and appends ` (error code <Code>)` only when `Code` is not 0 and differs from `Status`. Use `fmt.Sprintf`. `IsNotFound` and `IsClientError` do not change.
2. `npmapi/client.go`, `apiError(res)`: decode the error body into an envelope whose `error.code` is a `json.RawMessage` and which has no `message` member, so the text is never decoded. Read at most `errorBodyLimit` bytes as today. Turn the raw code into an int with `strconv.Atoi` on the trimmed bytes: a string, a fraction, a missing member or a body that is not JSON gives 0. Keep the code only when it is from 400 to 599 inclusive, and store 0 otherwise. Remove the fallback to `http.StatusText`. Update the doc comments of `apiError` and `do` to say the answer's text is never kept.
3. `proxyhost_add.go`, `proxyhostAddAnswer`: the 4xx branch keeps `errcode.InvalidInput` and the wrapped cause, and its detail becomes `fmt.Sprintf("Nginx Proxy Manager refused the request (HTTP %d)", answer.Status)`. The other branch is unchanged. Rewrite the doc comment: a 4xx is `invalid_input` with NPM's status only.
4. Do not change `deps.go`. `apiVia` wraps the client's error as the cause of `npm.unhealthy`, and the cause now holds only a status and a code. `TestV1r11LoginFailureCarriesNoNPMText` pins that.
5. Run `rg -n '\.Message' internal/modules/npm --glob '!*_test.go' --glob '!fake/**'`. Every hit that is left must read a `dockerapi.APIError`, a `errcode` field error or another type, never `npmapi.APIError`.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/11 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r11' -v ./internal/modules/npm/npmapi/ ./internal/modules/npm/
```

Expected: every `TestV1r11` test and subtest `PASS`.

- [ ] **Step 5: Verify the task**

```sh
export TMPDIR=/root/.cache/w10b-tmp/11 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./internal/modules/npm/... ./internal/agent/...
timeout 900 go test -race -count=1 ./internal/modules/npm/ ./internal/modules/npm/npmapi/ ./internal/modules/npm/fake/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10b-tmp/11 && go clean -testcache
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and no `go mod tidy -diff` output. Quote each real output in the report.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm
git commit -m "fix(npm): drop NPM answer text from errors"
```

### Task 12: Remove the NPM instance after a bootstrap conflict

**Files:**
- Modify: `internal/modules/npm/instance_deploy.go` (`bootstrap`, the call to it in `deployHandler.Apply`, one new method `discardDeploy`)
- Modify: `docs/user/modules/npm.md` (the "Deploy" section's paragraph that starts "If the administrator cannot be created", and the `npm.setup_failed` row of the error table)
- Read only: `internal/modules/npm/deps.go` (`instanceCreate` publishes 81/tcp on `127.0.0.1` and stays as it is), `internal/modules/npm/npmapi/client.go` (`CreateAdmin`, `Health`; Task 11 edited this file first)
- Test: `internal/modules/npm/instance_deploy_conflict_test.go` (create). The fake NPM server (`internal/modules/npm/fake/server.go`) already answers 403 to a second `POST /api/users`, so it does not change.

**Interfaces:**
- Consumes: `npmapi.IsClientError`, `npmapi.APIError` as Task 11 left them (the status is read, no text). Existing test helpers `newTHHost`, `dpApply`, `dpPassword`, `dpMutated`, `thAPIError`, `thHost.deployed` do not change.
- Produces: nothing a later task reads. `deployHandler.bootstrap` gains a second result, `conflict bool`.

**Items:** 2026-11-06-v1-security-review#14
**Depends on:** Task 11
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

The failure this fixes (Codex F13): NPM's port 81 is published on `127.0.0.1` before Darkpanel creates the first administrator, and the deploy polls every two seconds. A local user who creates the first administrator first wins. The deploy then fails with `npm.setup_failed`, but the container keeps running under the other user's administrator. Ruling W10b-R9: when the first-administrator creation finds an administrator the deploy did not create, the deploy stops and removes the container and the data volumes it created before it fails. This task also removes the network the deploy created, because a network left behind makes the next deploy refuse with `docker.in_use`, and the network holds no data.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/npm/instance_deploy_conflict_test.go`:

```go
package npm

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
)

const (
	v1r12Intruder         = "intruder@example.com"
	v1r12IntruderPassword = "placeholder-secret-intruder"
)

// v1r12Racer is the unauthenticated NPM client of a deploy that loses the first-administrator race: another client
// creates an administrator after the deploy read NPM's status and before the deploy creates its own.
type v1r12Racer struct {
	npmAPI
	win func()
}

func (r v1r12Racer) CreateAdmin(ctx context.Context, email, password string) error {
	r.win()
	return r.npmAPI.CreateAdmin(ctx, email, password)
}

// v1r12Removals is the Engine changes that follow the start of the container.
func v1r12Removals(h *thHost) []string {
	got := dpMutated(h)
	return got[slices.Index(got, "StartContainer")+1:]
}

func TestV1r12BootstrapConflictRemovesTheInstance(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(h *thHost, n *nm)
		foreign bool // the administrator in NPM is the intruder's
		removed bool
	}{
		{"an administrator exists before the deploy creates one", func(h *thHost, _ *nm) {
			h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
		}, true, true},
		{"an administrator appears between the status read and the creation", func(h *thHost, n *nm) {
			live := n.d.anon()
			n.d.anon = func() npmAPI {
				return v1r12Racer{npmAPI: live, win: func() { h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword) }}
			}
		}, true, true},
		{"NPM refuses to create the administrator", func(h *thHost, _ *nm) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusForbidden})
		}, false, true},
		{"NPM fails while it creates the administrator", func(h *thHost, _ *nm) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusInternalServerError})
		}, false, false},
		{"the answer to the creation is lost", func(h *thHost, _ *nm) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{AfterCommit: true})
		}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			c.arrange(h, &n)
			_, err := dpApply(h, n, "c1")
			var coded *errcode.Error
			if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if core.IsDefinite(err) {
				t.Error("the failure is Definite: effects of the deploy were made")
			}
			for _, secret := range []string{dpPassword, v1r12IntruderPassword} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the error carries %q", secret)
				}
			}
			_, container := h.docker.container(containerName)
			_, network := h.docker.network(networkName)
			_, data := h.docker.volume(dataVolume)
			_, certs := h.docker.volume(certVolume)
			for name, present := range map[string]bool{"container": container, "network": network, "data volume": data, "certificate volume": certs} {
				if present == c.removed {
					t.Errorf("the %s is present: %t, want %t", name, present, !c.removed)
				}
			}
			if removed := strings.Contains(coded.Detail, "the deploy removed what it created"); removed != c.removed {
				t.Errorf("detail %q says the deploy removed its objects: %t, want %t", coded.Detail, removed, c.removed)
			}
			want := []string{"StopContainer", "RemoveContainer", "RemoveVolume", "RemoveVolume", "RemoveNetwork"}
			if !c.removed {
				want = nil
			}
			if got := v1r12Removals(h); !slices.Equal(got, want) {
				t.Errorf("Engine changes after the start %v, want %v", got, want)
			}
			if c.foreign {
				if email, password, ok := h.npm.Admin(); !ok || email != v1r12Intruder || password != v1r12IntruderPassword {
					t.Errorf("the other administrator is now %q: the deploy must not touch NPM's users", email)
				}
			}
			if _, ok, _ := loadInstance(context.Background(), h.db.Read()); ok {
				t.Error("an instance row was written")
			}
		})
	}
}

func TestV1r12BootstrapConflictReportsAFailedRemoval(t *testing.T) {
	h := newTHHost(t)
	h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
	h.docker.Fail("RemoveContainer", thAPIError(http.StatusInternalServerError, "daemon error"))
	_, err := dpApply(h, h.nm(), "c1")
	var coded *errcode.Error
	if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
		t.Fatalf("error %v, want npm.setup_failed", err)
	}
	if !strings.Contains(coded.Detail, "the deploy could not remove what it created") || strings.Contains(coded.Detail, "the deploy removed what it created") {
		t.Errorf("detail %q, want it to say the removal failed", coded.Detail)
	}
	if _, ok := h.docker.container(containerName); !ok {
		t.Error("the container is gone although its removal failed")
	}
	want := []string{"StopContainer", "RemoveContainer", "RemoveVolume", "RemoveVolume", "RemoveNetwork"}
	if got := v1r12Removals(h); !slices.Equal(got, want) {
		t.Errorf("Engine changes after the start %v, want every removal attempted: %v", got, want)
	}
	for _, secret := range []string{dpPassword, v1r12IntruderPassword} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error carries %q", secret)
		}
	}
}
```

The last three rows of the first table are guards: a failure that does not prove another administrator exists keeps the instance, as `TestDpBootstrap` expects. The first three rows and the second test fail on the current tree because nothing is removed.

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/12 && export TMPDIR=/root/.cache/w10b-tmp/12 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r12' ./internal/modules/npm/
```

Expected: the first three subtests of `TestV1r12BootstrapConflictRemovesTheInstance` and `TestV1r12BootstrapConflictReportsAFailedRemoval` fail with `the container is present: true, want false` and `Engine changes after the start [] , want [StopContainer ...]`. The two guard subtests pass. Quote the output.

- [ ] **Step 3: Implement**

1. `bootstrap` in `instance_deploy.go`: change the signature to `bootstrap(ctx context.Context, env core.Env, email, password string) (conflict bool, err error)`. It returns `conflict` true in exactly two places, with the same errors as today:
   - the status read reports `health.Setup` before the deploy created anything (`Nginx Proxy Manager already has an administrator`);
   - `api.CreateAdmin` fails with an error for which `npmapi.IsClientError` is true while `ctx.Err()` is nil. NPM answered a refusal, so it committed no user, and an administrator that exists was not made by this deploy.
   Every other return has `conflict` false: a poll that times out, a 5xx or a lost answer to the creation (an administrator may exist with the sealed password), the missing `setup` flag after the creation, and a refused login. Update the doc comment to say so.
2. `deployHandler.Apply`: replace the `if err := h.bootstrap(...)` block with a call that takes both results. On `conflict`, return `Deployed{}` and `h.discardDeploy(ctx, containerID, err)`. On another error, return it unchanged. The journal is not touched: the effects stay as they are today (five made, the instance effect intended with the sealed password).
3. Add `func (h deployHandler) discardDeploy(ctx context.Context, containerID string, cause error) error`:
   - It runs on `context.WithoutCancel(ctx)` bounded by `context.WithTimeout` of twice `deployStop`, so a cancelled apply still removes the instance.
   - It attempts, in this order and always all of them, on `h.n.d.docker`: `StopContainer(containerID, deployStop)`, `RemoveContainer(containerID, dockerapi.RemoveContainerOptions{Force: true})`, `RemoveVolume(dataVolume, false)`, `RemoveVolume(certVolume, false)`, `RemoveNetwork(networkName)`. An error for which `dockerapi.IsNotFound` is true counts as removed. Any other error is collected as `fmt.Errorf("npm: <what>: %w", err)` with `<what>` the words `Inverse` uses (`stop container <id>`, `remove container <id>`, `remove volume <name>`, `remove network <name>`).
   - It returns `errcode.Wrap(errcode.NPMSetupFailed, <cause, joined with the collected errors when there are any>, <detail>)`, never Definite. `<detail>` is the `Detail` of the `*errcode.Error` in `cause` (find it with `errors.As`), then `; the deploy removed what it created` when nothing failed, or `; the deploy could not remove what it created: remove the Docker container, volumes and network whose names start with darkpanel-npm` when something did.
4. Leave `instanceCreate` in `deps.go`, the journal, the `Inverse` method and `Verify` as they are. State in the report that a later `darkpanel change undo` of this failed change observes objects that are already gone and stops on the first effect as a conflict, which the owner accepted in ruling W10b-R9, and that the host is ready for a new deploy at once.
5. `docs/user/modules/npm.md`, section "Deploy": change the sentence `If the administrator cannot be created, the change fails with` to `If the administrator cannot be created for another reason, the change fails with`, and insert this paragraph after that one (the inner code span is `npm.setup_failed`, as in the neighbouring paragraphs):

   ````text
   If NPM already has an administrator when the deploy reaches that step, or refuses to create the first one, someone else created it first. The deploy then removes the container, both volumes and the network it created, so that no instance keeps running under another administrator, and the change fails with `npm.setup_failed`. The message says whether the removal succeeded. Find out who can reach port 81 on the host, then deploy again.
   ````

   In the error table, the `npm.setup_failed` row: append to its second cell the sentence `A deploy that found another administrator has already removed what it created.` Keep the row's other cells.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/12 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r12|TestDpBootstrap|TestDpApply' -v ./internal/modules/npm/
```

Expected: every `TestV1r12` subtest and every `TestDpBootstrap` and `TestDpApply` subtest `PASS`.

- [ ] **Step 5: Verify the task**

```sh
export TMPDIR=/root/.cache/w10b-tmp/12 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./internal/modules/npm/... ./internal/docs/...
timeout 900 go test -race -count=1 ./internal/modules/npm/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10b-tmp/12 && go clean -testcache
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and no `go mod tidy -diff` output. The deploy's host behaviour changes, so the plan's gate 7 runs the harness on the branch; this task needs no harness edit. Quote each real output in the report.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/npm docs/user/modules/npm.md
git commit -m "fix(npm): remove instance on bootstrap conflict"
```

### Task 13: Mask every command argument in container inspection

**Files:**
- Modify: `internal/modules/docker/inspect.go` (`redactArgs` doc comment, new `redactCmd`, `containerDetailOf`)
- Modify: `docs/user/modules/docker.md` (the "Secrets" section's second sentence)
- Modify: `docs/dforge/notes/v1-w3-final-review-followups.md` (the bullet that starts `- inspect residual`)
- Read only: `internal/modules/docker/wire.go` (`ContainerDetail.Cmd` and `.Entrypoint` carry no description, so no schema or API document changes; run no `make schemas`)
- Test: `internal/modules/docker/inspect_cmd_test.go` (create), `internal/modules/docker/inspect_test.go` (one expectation), `internal/modules/docker/fuzz_test.go` (`fz6Scrub` and the oracle of `FuzzContainerDetail`)

**Interfaces:**
- Consumes: nothing from Contracts.
- Produces: `redactCmd(args []string) []string`, unexported in package `docker`: every element is the mask `logfilter.Mask`. `redactArgs` keeps its rule for an entrypoint: the first element, then one mask per argument. Nothing else in this plan reads either.

**Items:** 2026-11-06-v1-security-review#3
**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

The failure this fixes (Codex F1): `docker.container.inspect` has scope `read`. It kept element 0 of `Config.Cmd` as the "program". When an image has an `Entrypoint`, Docker passes `Cmd` to it as arguments, so element 0 is an argument, and a credential passed as the first command argument (for example `docker run image <token> --port 80`) reaches every `read` user. Ruling W10b-R4: every element of `Cmd` is masked, and an element of `Entrypoint` keeps its existing rule.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/docker/inspect_cmd_test.go`:

```go
package docker

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r13Mask = "[redacted]"

func TestV1r13InspectMasksEveryCommandElement(t *testing.T) {
	for _, tc := range []struct {
		name           string
		entrypoint     []string
		cmd            []string
		wantEntrypoint []string
		wantCmd        []string
	}{
		{"the command is the arguments of an entrypoint", []string{"/entry"}, []string{"placeholder-secret", "--port", "80"},
			[]string{"/entry"}, []string{v1r13Mask, v1r13Mask, v1r13Mask}},
		{"a command that starts with a program", nil, []string{"redis-cli", "-a", "placeholder-secret"},
			nil, []string{v1r13Mask, v1r13Mask, v1r13Mask}},
		{"a command of one element", []string{"/entry"}, []string{"placeholder-secret"},
			[]string{"/entry"}, []string{v1r13Mask}},
		{"an entrypoint keeps only its first element", []string{"/entry", "--dsn=placeholder-secret"}, []string{"serve"},
			[]string{"/entry", v1r13Mask}, []string{v1r13Mask}},
		{"an empty command", []string{"/entry"}, []string{}, []string{"/entry"}, []string{}},
		{"no command", []string{"/entry"}, nil, []string{"/entry"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := rdWeb(func(c *dockerapi.ContainerJSON) {
				c.Config.Entrypoint, c.Config.Cmd = tc.entrypoint, tc.cmd
			})
			d, err := containerDetailOf(c)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.Cmd, tc.wantCmd) {
				t.Errorf("cmd %q, want %q", d.Cmd, tc.wantCmd)
			}
			if !reflect.DeepEqual(d.Entrypoint, tc.wantEntrypoint) {
				t.Errorf("entrypoint %q, want %q", d.Entrypoint, tc.wantEntrypoint)
			}
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "placeholder-secret") {
				t.Errorf("the answer carries the secret:\n%s", raw)
			}
		})
	}
}

func TestV1r13InspectActionShowsNoCommandElement(t *testing.T) {
	h := newDKHost(t)
	h.docker.put(rdWeb(func(c *dockerapi.ContainerJSON) {
		c.Config.Entrypoint = []string{"/entry"}
		c.Config.Cmd = []string{"placeholder-secret", "--port", "80"}
	}))
	raw, err := rdRead(t, h.dk().containerInspect(), h, `{"container":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Cmd, Entrypoint []string }
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, raw)
	}
	if want := []string{v1r13Mask, v1r13Mask, v1r13Mask}; !reflect.DeepEqual(out.Cmd, want) {
		t.Errorf("cmd %q, want %q", out.Cmd, want)
	}
	if want := []string{"/entry"}; !reflect.DeepEqual(out.Entrypoint, want) {
		t.Errorf("entrypoint %q, want %q", out.Entrypoint, want)
	}
	if strings.Contains(string(raw), "placeholder-secret") {
		t.Errorf("inspect leaks the first command argument:\n%s", raw)
	}
}
```

Edit the existing tests to the new rule, so they pass after the change and fail before it:

- `inspect_test.go`, `rdExpectedDetail`: the comment `// Owned or not, only the program and the listed labels are shown.` becomes `// Owned or not, no command argument and only the listed labels are shown.`, and the replacement of the `cmd` line becomes `strings.Replace(s, `"cmd": ["nginx", "-g", "daemon off;"]`, `"cmd": ["[redacted]", "[redacted]", "[redacted]"]`, 1)`. `TestRdRedactArgs` stays as it is: `redactArgs` still serves the entrypoint.
- `fuzz_test.go`, `fz6Scrub`: the closure `args := func(in []string) []string` becomes `args := func(in []string, from int) []string`, its loop starts at `i := from`, and the line that applies it becomes `out.Config.Cmd, out.Config.Entrypoint = args(c.Config.Cmd, 0), args(c.Config.Entrypoint, 1)`.
- `fuzz_test.go`, `FuzzContainerDetail`: replace the loop `for _, p := range [][2][]string{{d.Cmd, c.Config.Cmd}, {d.Entrypoint, c.Config.Entrypoint}} { ... }` with the loop below, and change the function's doc comment from "every argument after the first is the mask" to "every command element and every entrypoint element after the first is the mask".

```go
		for _, p := range []struct {
			got, want   []string
			keepProgram bool
		}{{d.Cmd, c.Config.Cmd, false}, {d.Entrypoint, c.Config.Entrypoint, true}} {
			if len(p.got) != len(p.want) {
				fail("an argument list changed length")
			}
			for i := range p.got {
				if keep := p.keepProgram && i == 0; (keep && p.got[i] != p.want[i]) || (!keep && p.got[i] != logfilter.Mask) {
					fail("an argument is shown or masked wrongly")
				}
			}
		}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/13 && export TMPDIR=/root/.cache/w10b-tmp/13 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r13|TestRdInspectReportsTheContainer|FuzzContainerDetail' ./internal/modules/docker/
```

Expected: `TestV1r13InspectMasksEveryCommandElement` fails with `cmd ["placeholder-secret" "[redacted]" "[redacted]"], want ["[redacted]" "[redacted]" "[redacted]"]` and `the answer carries the secret`; `TestV1r13InspectActionShowsNoCommandElement` fails the same way; `TestRdInspectReportsTheContainer` fails on the `cmd` member; `FuzzContainerDetail` fails on its seed corpus with `an argument is shown or masked wrongly`. Quote the output.

- [ ] **Step 3: Implement**

1. `inspect.go`: add `redactCmd(args []string) []string`. A command line is free text that may hold a secret anywhere, and Docker hands `Cmd` to the entrypoint as arguments, so its first element is not known to be a program. The function returns `args` itself when it is empty (so `nil` stays `nil` and an empty list stays empty) and otherwise a new slice of the same length with `logfilter.Mask` at every index. Give it a doc comment that says why no element is kept.
2. `redactArgs`: keep the behaviour and rewrite its doc comment, which has a broken line wrap today, to say it renders an entrypoint: the first element, which is the program, then one mask per argument.
3. `containerDetailOf`: `Cmd: redactCmd(c.Config.Cmd)`; `Entrypoint` stays `redactArgs(c.Config.Entrypoint)`. Update the function's doc comment so it says the environment values, the command, the entrypoint's arguments and the labels outside `shownLabels` are redacted.
4. `docs/user/modules/docker.md`, section "Secrets": replace the sentence that starts `It also replaces every argument of the command and the entrypoint after the first` (it ends with `[redacted]`) with the sentence below. Keep the line wrap close to the neighbouring lines.

   ````text
   It also replaces every element of the command and every argument of the entrypoint after its first element, and the value of every label other than the ones Compose and Darkpanel set, with `[redacted]`.
   ````
5. `docs/dforge/notes/v1-w3-final-review-followups.md`: in the bullet that starts `- inspect residual`, change `argv[0],` to `the first element of an entrypoint,` and add at the end of the bullet: ` The first element of a command is masked as well: an image with an entrypoint makes it an argument, not a program.`
6. `wire.go` and `schemas/**` stay unchanged. Check with `git diff --stat -- schemas docs/api`, which prints nothing.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/13 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r13|TestRd|FuzzContainerDetail' -v ./internal/modules/docker/
```

Expected: every `TestV1r13`, `TestRd` and `FuzzContainerDetail` test `PASS`.

- [ ] **Step 5: Verify the task**

```sh
export TMPDIR=/root/.cache/w10b-tmp/13 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./internal/modules/docker/... ./internal/docs/...
timeout 900 go test -race -count=1 ./internal/modules/docker/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git diff --stat -- schemas docs/api
rm -rf /root/.cache/w10b-tmp/13 && go clean -testcache
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, no `go mod tidy -diff` output, and an empty `git diff --stat`. Quote each real output in the report.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/docker docs/user/modules/docker.md docs/dforge/notes/v1-w3-final-review-followups.md
git commit -m "fix(docker): mask every inspect command argument"
```

### Task 14: Keep Docker label values out of plans, inputs and effects

**Files:**
- Modify: `internal/modules/docker/dstate/object.go:57-112` (`labelDigests`, `networkDoc`, `NetworkCreated`, `volumeDoc`, `VolumeCreated`)
- Modify: `internal/modules/docker/observe.go` (the observer renders network and volume documents)
- Modify: `internal/modules/docker/network_manage.go:44-50, 169-179, 296-335` (`manageLabelLines`, create `Plan`, remove `Plan` and `Apply`)
- Modify: `internal/modules/docker/volume_manage.go:46-87, 191-230` (create `Plan`, remove `Plan` and `Apply`)
- Modify: `internal/modules/docker/wire.go:265-278` (the `Labels` field of `NetworkCreateInput` and `VolumeCreateInput`)
- Create: `internal/modules/docker/label_secrets_test.go`
- Create: `internal/modules/docker/dstate/label_fingerprint_test.go`
- Test (modify): `internal/modules/docker/dstate/dstate_test.go`, `internal/modules/docker/network_manage_test.go`, `internal/modules/docker/volume_manage_test.go`, `internal/modules/docker/observe_test.go`
- Regenerate: `schemas/docker.network.create/**`, `schemas/docker.volume.create/**` and `docs/api/**` (`make schemas`, `make api-docs`)
- Read only: `internal/controller/coreactions/audit.go:78` (`core.audit.list` returns the recorded input, so a redacted input is all it can show)

**Interfaces:**
- Consumes: Contracts "Keyed fingerprints" (format `hmac-sha256:<hex>`, label `docker.effect.fingerprint/v1`, key `env.Sealer.Derive(label)`). Docker label values are never restored from an effect: network and volume removal are irreversible, and a created object's undo only removes it. So no sealed secret is needed, and the effect document holds fingerprints only.
- Produces, in package `dstate` (`internal/modules/docker/dstate`):
  - `func LabelFingerprints(key []byte, labels map[string]string) map[string]string` returns nil for no labels. Otherwise it returns each label key with `hmac-sha256:` plus the lower-case hex of `HMAC-SHA256(key, value)`. With an empty `key` every value is `""`, because an HMAC under an empty key is as guessable as a plain digest.
  - `func NetworkCreatedKeyed(n *dockerapi.Network, key []byte) (json.RawMessage, error)` and `func VolumeCreatedKeyed(v *dockerapi.Volume, unused bool, key []byte) (json.RawMessage, error)` render the documents with `LabelFingerprints(key, …)`.
  - `NetworkCreated(n)` and `VolumeCreated(v, unused)` keep their signatures, which the `npm` and `panel` modules call for objects that carry only the fixed `darkpanel.module` label. They become `NetworkCreatedKeyed(n, nil)` and `VolumeCreatedKeyed(v, unused, nil)`: label keys are recorded, label values are not.
- Produces, in package `docker`: `const labelFingerprintLabel = "docker.effect.fingerprint/v1"` and `func labelFingerprintKey(env core.Env) []byte` (`env.Sealer.Derive(labelFingerprintLabel)`), both in `observe.go`.
- Produces: the `Labels` field of both create inputs is tagged `secret:"true"`. The recorded input then holds `"[redacted]"` for `labels`. This changes a wire type, so the Generated files rule applies.

**Items:** 2026-11-06-v1-security-review#5, 2026-10-21-v1-completion#384

**Depends on:** Task 13
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/docker/label_secrets_test.go`:

```go
package docker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r14Secret = "placeholder-secret"

// v1r14Key is the key effect documents fingerprint label values under on the test host.
func v1r14Key() []byte { return coretest.Sealer{}.Derive("docker.effect.fingerprint/v1") }

// v1r14Fingerprint is how a label value appears in an effect document.
func v1r14Fingerprint(value string) string {
	m := hmac.New(sha256.New, v1r14Key())
	m.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// v1r14Unsalted is the digest effect documents used to hold, which a guessed value confirms offline.
func v1r14Unsalted(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// v1r14CheckDoc requires the document to hold the keyed fingerprint of the secret under the key token, and no
// trace of the value or of its unsalted digest.
func v1r14CheckDoc(t *testing.T, doc json.RawMessage) {
	t.Helper()
	text := string(doc)
	if strings.Contains(text, v1r14Secret) {
		t.Errorf("the document %s holds the label value", text)
	}
	if strings.Contains(text, v1r14Unsalted(v1r14Secret)) {
		t.Errorf("the document %s holds the unsalted digest of the label value", text)
	}
	var d struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"token": v1r14Fingerprint(v1r14Secret)}; !reflect.DeepEqual(d.Labels, want) {
		t.Errorf("labels %v, want %v", d.Labels, want)
	}
}

// TestV1r14PlanDiffsShowLabelKeysOnly: a plan's diff names each label it adds and never its value, and the plan's
// state hash is not the unkeyed SHA-256 of a request that holds the values.
func TestV1r14PlanDiffsShowLabelKeysOnly(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	tests := []struct {
		name  string
		def   core.ActionDef
		input func(token string) string
		diff  []core.DiffLine
		plain func(token string) (string, error)
	}{
		{
			name: "network", def: h.dk().networkCreate(),
			input: func(token string) string {
				return `{"name":"app","labels":{"team":"billing-placeholder","token":"` + token + `"}}`
			},
			diff: []core.DiffLine{
				{Op: "+", Text: "network app"}, {Op: " ", Text: "driver bridge"}, {Op: "+", Text: "label team"}, {Op: "+", Text: "label token"},
			},
			plain: func(token string) (string, error) {
				return manageHash(dockerapi.NetworkCreate{Name: "app", Driver: "bridge", Labels: map[string]string{"team": "billing-placeholder", "token": token}})
			},
		},
		{
			name: "volume", def: h.dk().volumeCreate(),
			input: func(token string) string {
				return `{"name":"data","labels":{"team":"billing-placeholder","token":"` + token + `"}}`
			},
			diff: []core.DiffLine{
				{Op: "+", Text: "volume data"}, {Op: " ", Text: "driver local"}, {Op: "+", Text: "label team"}, {Op: "+", Text: "label token"},
			},
			plain: func(token string) (string, error) {
				return manageHash(dockerapi.VolumeCreate{Name: "data", Driver: "local", Labels: map[string]string{"team": "billing-placeholder", "token": token}})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := func(token string) core.PlanOutcome {
				t.Helper()
				out, err := tc.def.Handler.Plan(ctx, h.env(""), json.RawMessage(tc.input(token)))
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			out := plan(v1r14Secret)
			if !slices.Equal(out.Diff, tc.diff) {
				t.Errorf("diff %v, want %v", out.Diff, tc.diff)
			}
			for _, l := range out.Diff {
				if strings.Contains(l.Text, v1r14Secret) || strings.Contains(l.Text, "billing-placeholder") {
					t.Errorf("diff line %q shows a label value", l.Text)
				}
			}
			plain, err := tc.plain(v1r14Secret)
			if err != nil {
				t.Fatal(err)
			}
			if out.StateHash == plain {
				t.Error("the state hash is the unkeyed SHA-256 of the request, which a guessed label value confirms")
			}
			if other := plan("another-placeholder"); other.StateHash == out.StateHash {
				t.Error("a changed label value leaves the state hash unchanged")
			}
			if again := plan(v1r14Secret); again.StateHash != out.StateHash {
				t.Errorf("one input hashes as %s and as %s", out.StateHash, again.StateHash)
			}
		})
	}
}

// TestV1r14LabelInputsAreRedacted: the input the engine records, and core.audit.list reads back, holds no label
// value.
func TestV1r14LabelInputsAreRedacted(t *testing.T) {
	tests := map[string]struct {
		typ   reflect.Type
		input string
		name  string
	}{
		"network": {reflect.TypeFor[NetworkCreateInput](), `{"name":"app","labels":{"token":"` + v1r14Secret + `"}}`, `"name":"app"`},
		"volume":  {reflect.TypeFor[VolumeCreateInput](), `{"name":"data","labels":{"token":"` + v1r14Secret + `"}}`, `"name":"data"`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := string(schema.Redact(tc.typ, json.RawMessage(tc.input)))
			if strings.Contains(got, v1r14Secret) {
				t.Errorf("the recorded input %s holds the label value", got)
			}
			if !strings.Contains(got, tc.name) {
				t.Errorf("the recorded input %s lost %s, which is no secret", got, tc.name)
			}
		})
	}
}

// TestV1r14EffectsHoldKeyedFingerprints: the effect documents of the four label-bearing effects hold the keyed
// fingerprint of a label value, an observation of the made effect agrees with its recorded post-state, and neither
// carries the value or its unsalted digest.
func TestV1r14EffectsHoldKeyedFingerprints(t *testing.T) {
	ctx := context.Background()
	withLabel := json.RawMessage(`"labels":{"token":"` + v1r14Secret + `"}`)
	t.Run("network create", func(t *testing.T) {
		h := newDKHost(t)
		def := h.dk().networkCreate()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"app",`+string(withLabel)+`}`)); err != nil {
			t.Fatal(err)
		}
		e := h.journal.effects()[0]
		v1r14CheckDoc(t, e.Post.State)
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil {
			t.Fatal(err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("observation %s differs from the recorded post-state %s", obs.Current.State, e.Post.State)
		}
	})
	t.Run("volume create", func(t *testing.T) {
		h := newDKHost(t)
		def := h.dk().volumeCreate()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data",`+string(withLabel)+`}`)); err != nil {
			t.Fatal(err)
		}
		e := h.journal.effects()[0]
		v1r14CheckDoc(t, e.Post.State)
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil {
			t.Fatal(err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("observation %s differs from the recorded post-state %s", obs.Current.State, e.Post.State)
		}
	})
	t.Run("network remove", func(t *testing.T) {
		h := newDKHost(t)
		old := dkNetwork("old", func(n *dockerapi.Network) { n.Labels = map[string]string{"token": v1r14Secret} })
		h.docker.putNetwork(old)
		if _, err := h.dk().networkRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"network":"`+old.ID+`"}`)); err != nil {
			t.Fatal(err)
		}
		v1r14CheckDoc(t, h.journal.effects()[0].Before.State)
	})
	t.Run("volume remove", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("old", map[string]string{"token": v1r14Secret}))
		if _, err := h.dk().volumeRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"volume":"old"}`)); err != nil {
			t.Fatal(err)
		}
		v1r14CheckDoc(t, h.journal.effects()[0].Before.State)
	})
}
```

Create `internal/modules/docker/dstate/label_fingerprint_test.go`:

```go
package dstate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r14Secret = "placeholder-secret"

// v1r14Want is the document form of the secret under key.
func v1r14Want(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(v1r14Secret))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

func v1r14Labels(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var d struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d.Labels
}

func v1r14Renderers() map[string]func(key []byte) (json.RawMessage, error) {
	network := &dockerapi.Network{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{"token": v1r14Secret}}
	volume := &dockerapi.Volume{Name: "data", Labels: map[string]string{"token": v1r14Secret}}
	return map[string]func(key []byte) (json.RawMessage, error){
		"network": func(key []byte) (json.RawMessage, error) { return NetworkCreatedKeyed(network, key) },
		"volume":  func(key []byte) (json.RawMessage, error) { return VolumeCreatedKeyed(volume, true, key) },
	}
}

// TestV1r14KeyedLabelFingerprints: a label value is recorded as the HMAC-SHA256 of the value under the given key, so
// the same value gives another document under another key and no value or plain digest is in the document.
func TestV1r14KeyedLabelFingerprints(t *testing.T) {
	keyA, keyB := []byte("placeholder-key-a"), []byte("placeholder-key-b")
	unsalted := sha256.Sum256([]byte(v1r14Secret))
	for name, render := range v1r14Renderers() {
		t.Run(name, func(t *testing.T) {
			raw, err := render(keyA)
			if err != nil {
				t.Fatal(err)
			}
			if got := v1r14Labels(t, raw); !reflect.DeepEqual(got, map[string]string{"token": v1r14Want(keyA)}) {
				t.Errorf("labels %v, want the keyed fingerprint %s", got, v1r14Want(keyA))
			}
			if text := string(raw); strings.Contains(text, v1r14Secret) || strings.Contains(text, hex.EncodeToString(unsalted[:])) {
				t.Errorf("the document %s holds the label value or its unsalted digest", text)
			}
			other, err := render(keyB)
			if err != nil {
				t.Fatal(err)
			}
			if string(other) == string(raw) {
				t.Errorf("two keys render one document %s", raw)
			}
			again, err := render(keyA)
			if err != nil || string(again) != string(raw) {
				t.Errorf("one key rendered %s and then %s (error %v)", raw, again, err)
			}
		})
	}
}

// TestV1r14UnkeyedRenderingRecordsNoLabelValue: without a key a document keeps the label keys and records no
// value, not even a digest, and the two-argument forms behave as an empty key.
func TestV1r14UnkeyedRenderingRecordsNoLabelValue(t *testing.T) {
	unsalted := sha256.Sum256([]byte(v1r14Secret))
	network := &dockerapi.Network{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{"token": v1r14Secret}}
	volume := &dockerapi.Volume{Name: "data", Labels: map[string]string{"token": v1r14Secret}}
	renders := map[string]func() (json.RawMessage, error){
		"NetworkCreated":            func() (json.RawMessage, error) { return NetworkCreated(network) },
		"NetworkCreatedKeyed nil":   func() (json.RawMessage, error) { return NetworkCreatedKeyed(network, nil) },
		"NetworkCreatedKeyed empty": func() (json.RawMessage, error) { return NetworkCreatedKeyed(network, []byte{}) },
		"VolumeCreated":             func() (json.RawMessage, error) { return VolumeCreated(volume, true) },
		"VolumeCreatedKeyed nil":    func() (json.RawMessage, error) { return VolumeCreatedKeyed(volume, true, nil) },
	}
	for name, render := range renders {
		t.Run(name, func(t *testing.T) {
			raw, err := render()
			if err != nil {
				t.Fatal(err)
			}
			if got := v1r14Labels(t, raw); !reflect.DeepEqual(got, map[string]string{"token": ""}) {
				t.Errorf("labels %v, want the key token with no value", got)
			}
			if text := string(raw); strings.Contains(text, v1r14Secret) || strings.Contains(text, hex.EncodeToString(unsalted[:])) {
				t.Errorf("the document %s holds the label value or a digest of it", text)
			}
		})
	}
}

func TestV1r14LabelFingerprintsOfNoLabels(t *testing.T) {
	for _, labels := range []map[string]string{nil, {}} {
		if got := LabelFingerprints([]byte("placeholder-key-a"), labels); got != nil {
			t.Errorf("LabelFingerprints of %v = %v, want nil", labels, got)
		}
	}
}
```

Update the tests that pinned the old behaviour, in the same step:

1. `internal/modules/docker/dstate/dstate_test.go`: delete `dsDigest` and `TestDsLabelValuesAreDigestedNotRecorded`, then remove the imports that become unused (`crypto/sha256`, `encoding/hex`, `fmt`, `strings`, and any other the compiler names). In `TestDsNetworkCreated` the `want` for the labelled network becomes `{"id":"n1","created":"2026-10-23T09:15:00Z","labels":{"a":"","b":""},"unused":false}` (a plain string, no `fmt.Sprintf`). In `TestDsVolumeCreated` it becomes `{"name":"data","labels":{"x":""},"unused":true}`.
2. `internal/modules/docker/network_manage_test.go`, test `TestObNetworkCreatePlan`, row `everything`: the last diff line pair `{Op: " ", Text: "label env=x"}, {Op: " ", Text: "label team=a"}` becomes `{Op: "+", Text: "label env"}, {Op: "+", Text: "label team"}`. In `TestObNetworkCreateApply`, subtest `creates and journals`, `dstate.NetworkCreated(&made)` becomes `dstate.NetworkCreatedKeyed(&made, v1r14Key())`.
3. `internal/modules/docker/volume_manage_test.go`: in the create plan row `labels` the diff pair becomes `{Op: "+", Text: "label env"}, {Op: "+", Text: "label team"}`. At line 87, `dstate.VolumeCreated(&made, true)` becomes `dstate.VolumeCreatedKeyed(&made, true, v1r14Key())`.
4. `internal/modules/docker/observe_test.go`, `TestEFObserveVolume`: the two `dstate.VolumeCreated(&v, …)` comparisons (`v` carries the label `team`) become `dstate.VolumeCreatedKeyed(&v, true, v1r14Key())` and `dstate.VolumeCreatedKeyed(&v, false, v1r14Key())`.
5. Every other comparison of a document against `dstate.NetworkCreated` or `dstate.VolumeCreated` in the docker package renders an object with no labels, whose document is the same under both forms; leave it. If the compiler or a test run shows another one failing, it renders a labelled object, and takes the keyed form with `v1r14Key()`.

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/14 && export TMPDIR=/root/.cache/w10b-tmp/14 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r14' ./internal/modules/docker/ ./internal/modules/docker/dstate/
```

Expected: both packages fail to build, the first error naming `undefined: dstate.NetworkCreatedKeyed` (docker) and `undefined: NetworkCreatedKeyed` (dstate). Record it verbatim. After you add only the dstate functions (Step 3, items 1), `TestV1r14PlanDiffsShowLabelKeysOnly`, `TestV1r14LabelInputsAreRedacted` and `TestV1r14EffectsHoldKeyedFingerprints` still fail on their assertions: the diff shows `label token=placeholder-secret`, the recorded input holds `placeholder-secret`, and the effect document holds the unsalted `sha256:` digest. Record that output too.

- [ ] **Step 3: Implement**

1. `dstate/object.go`: replace `labelDigests` with the exported `LabelFingerprints(key, labels)` of the Interfaces block, built on `crypto/hmac` and `crypto/sha256.New`. Add `NetworkCreatedKeyed` and `VolumeCreatedKeyed`, which hold the current bodies of `NetworkCreated` and `VolumeCreated` with `Labels: LabelFingerprints(key, …)`. Turn `NetworkCreated(n)` and `VolumeCreated(v, unused)` into one-line calls with a nil key. Rewrite the doc comments of both document types and all four functions: label values are fingerprints under a key, or absent without one, and the document is only compared, never used to recreate an object. Do not change the field names or the JSON of any other member.
2. `observe.go`: add `labelFingerprintLabel` and `labelFingerprintKey(env)`. `dk.observe` stops ignoring its `env`: it passes `labelFingerprintKey(env)` to `observeNetwork`, `findNetworks`, `networkState` and `observeVolume`, which call `dstate.NetworkCreatedKeyed` and `dstate.VolumeCreatedKeyed`. The container and endpoint paths do not change.
3. `network_manage.go` and `volume_manage.go`:
   - `manageLabelLines` returns one `{Op: "+", Text: "label <key>"}` line per label, keys sorted, and never reads a value. A create adds every label, so `+` is the right operation. Its doc comment says the diff never shows a label value.
   - The create `Plan` handlers stop ignoring their `env`. They hash a copy of the request whose `Labels` is `dstate.LabelFingerprints(labelFingerprintKey(env), req.Labels)`. `request` still returns the real labels, because `Apply` sends them to the daemon.
   - The remove `Plan` and `Apply` handlers render the before-document with `NetworkCreatedKeyed` and `VolumeCreatedKeyed` and the key from their `env`. Their plan hash covers that document, so it stays sensitive to a label change.
4. `wire.go`: add `secret:"true"` to the `Labels` struct tag of `NetworkCreateInput` and `VolumeCreateInput`, keeping the `// never darkpanel.module` comment.
5. Regenerate the generated files and commit them with the task:

```sh
make schemas
make api-docs
```

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/14 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r14' -v ./internal/modules/docker/ ./internal/modules/docker/dstate/
timeout 600 go test -count=1 ./internal/modules/docker/... ./internal/modules/npm/... ./internal/modules/panel/... ./internal/apidocs/...
```

Expected: PASS. `git status --short schemas docs/api` lists only files under `schemas/docker.network.create`, `schemas/docker.volume.create` and `docs/api`; any other changed schema file means something else drifted, so stop and report it.

- [ ] **Step 5: Run the task's checks**

```sh
export TMPDIR=/root/.cache/w10b-tmp/14 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 600 go test -race -count=1 ./internal/modules/docker/ ./internal/modules/docker/dstate/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10b-tmp/14 && go clean -testcache
```

Expected: no vet or `go mod tidy -diff` output, every test line `ok` or `?`, and `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/modules/docker schemas docs/api
git commit -m "fix(docker): keep label values out of plans and effects"
```

### Task 15: Read DERP service files without following links

**Files:**
- Modify: `internal/platform/files.go` (add `ReadRegularFile` next to `openBeneath`; `files_managed.go` is read only, its reads stay as they are)
- Modify: `internal/modules/derp/deps.go:50-70, 105-122` (the `deps` struct, `productionDP`, the `certFileLimit` constant)
- Modify: `internal/modules/derp/server_status.go:107` (`serverStatusHandler.certificate`)
- Modify: `internal/modules/derp/server_install.go:315-330` (`installHandler.readCert`)
- Modify: `internal/modules/derp/cert_renew.go:73, 107, 199` (`certRenewRead`, `certRenewCheck`, the key check of the renewal wait loop)
- Create: `internal/platform/files_read_test.go`
- Create: `internal/modules/derp/cert_files_test.go`
- Test (modify): `internal/modules/derp/testhost_test.go:179`, `internal/modules/derp/deps_test.go:43`, `internal/modules/derp/server_install_test.go:588, 883`

**Interfaces:**
- Consumes: nothing from another task. Task 16 edits `deps.go` after this task.
- Produces: `func ReadRegularFile(path string, limit int64) ([]byte, error)` in package `platform`. It opens `path` with `openat2` and `RESOLVE_NO_SYMLINKS`, so a symlink in any component, the last included, is refused. It accepts a regular file of at most `limit` bytes and reads no more than `limit+1` bytes. Every failure wraps the OS error with `%w`, so a missing file still satisfies `errors.Is(err, fs.ErrNotExist)`. No error carries file content.
- Produces: the `deps` field `readCertFile func(name string) ([]byte, error)` of package `derp`. Production is `platform.ReadRegularFile(name, certFileLimit)`. The module reads DERP certificate and key files through it and through nothing else. `deps.readFile` stays `os.ReadFile` for the other callers: the binary, the unit link and directory existence checks, which read paths a plain read must keep following or must not bound.
- Constant: `certFileLimit = 1 << 20`, the 1 MiB of W10b-R10.

**Items:** 2026-11-06-v1-security-review#7

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/platform/files_read_test.go`:

```go
package platform

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

// v1r15Dir is a temporary directory whose own path holds no symlink, because ReadRegularFile refuses one anywhere.
func v1r15Dir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestV1r15ReadRegularFile(t *testing.T) {
	const secret = "placeholder-secret-content"
	const content = "certificate bytes"
	dir := v1r15Dir(t)
	put := func(path, data string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	regular := put(filepath.Join(dir, "regular"), content)
	outside := put(filepath.Join(dir, "outside"), secret)
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(realDir, "crt"), secret)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(dir, "dirlink")
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		path     string
		limit    int64
		want     string
		wantErr  string
		notExist bool
	}{
		{name: "a regular file", path: regular, limit: 1024, want: content},
		{name: "exactly the limit", path: regular, limit: int64(len(content)), want: content},
		{name: "one byte over the limit", path: regular, limit: int64(len(content)) - 1, wantErr: "larger than"},
		{name: "a symlink to a file", path: link, limit: 1024, wantErr: "symbolic link"},
		{name: "a symlinked directory in the path", path: filepath.Join(dirLink, "crt"), limit: 1024, wantErr: "symbolic link"},
		{name: "a directory", path: realDir, limit: 1024, wantErr: "not a regular file"},
		{name: "a FIFO", path: fifo, limit: 1024, wantErr: "not a regular file"},
		{name: "a missing file", path: filepath.Join(dir, "missing"), limit: 1024, notExist: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			type result struct {
				data []byte
				err  error
			}
			done := make(chan result, 1)
			go func() {
				data, err := ReadRegularFile(tc.path, tc.limit)
				done <- result{data, err}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("ReadRegularFile blocked")
			}
			switch {
			case tc.notExist:
				if !errors.Is(got.err, fs.ErrNotExist) {
					t.Errorf("error %v, want one that satisfies fs.ErrNotExist", got.err)
				}
			case tc.wantErr != "":
				if got.err == nil || !strings.Contains(got.err.Error(), tc.wantErr) {
					t.Errorf("error %v, want one containing %q", got.err, tc.wantErr)
				}
			case got.err != nil || string(got.data) != tc.want:
				t.Errorf("got %q, %v; want %q", got.data, got.err, tc.want)
			}
			if got.err != nil {
				if len(got.data) != 0 {
					t.Errorf("a failed read returned %d bytes", len(got.data))
				}
				if strings.Contains(got.err.Error(), secret) {
					t.Errorf("error %v carries file content", got.err)
				}
			}
		})
	}
}
```

Create `internal/modules/derp/cert_files_test.go`:

```go
package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// v1r15Split returns h's deps with two changes: a plain read of a path in the certificate directory fails the test,
// and the certificate reads go to the host's files.
func v1r15Split(t *testing.T, h *dpHost) dp {
	t.Helper()
	d := h.dp()
	plain := d.d.readFile
	d.d.readFile = func(name string) ([]byte, error) {
		if strings.HasPrefix(name, server.CertDir+"/") {
			t.Errorf("readFile opened %s; the certificate directory is read through readCertFile", name)
		}
		return plain(name)
	}
	d.d.readCertFile = h.readFile
	return d
}

// TestV1r15ProductionCertReadRefusesPlantedFiles: the production read follows no link, blocks on no FIFO, refuses
// a file over 1 MiB and reads one of exactly 1 MiB.
func TestV1r15ProductionCertReadRefusesPlantedFiles(t *testing.T) {
	const secret = "placeholder-secret-content"
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	crt := put("crt", []byte("CERT"))
	outside := put("outside", []byte(secret))
	exact := put("exact", bytes.Repeat([]byte("a"), 1<<20))
	over := put("over", bytes.Repeat([]byte("a"), 1<<20+1))
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join("real", "crt"), []byte(secret))
	link := filepath.Join(dir, "link")
	dirLink := filepath.Join(dir, "dirlink")
	fifo := filepath.Join(dir, "fifo")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	read := productionDP().d.readCertFile
	if read == nil {
		t.Fatal("productionDP leaves deps.readCertFile unset")
	}
	for _, tc := range []struct {
		name    string
		path    string
		wantLen int
		refused bool
	}{
		{"a regular certificate", crt, 4, false},
		{"exactly 1 MiB", exact, 1 << 20, false},
		{"one byte over 1 MiB", over, 0, true},
		{"a symlink to another file", link, 0, true},
		{"a symlinked directory", filepath.Join(dirLink, "crt"), 0, true},
		{"a FIFO", fifo, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type result struct {
				data []byte
				err  error
			}
			done := make(chan result, 1)
			go func() {
				data, err := read(tc.path)
				done <- result{data, err}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the read blocked")
			}
			if tc.refused != (got.err != nil) || len(got.data) != tc.wantLen {
				t.Errorf("read %d bytes, error %v; want refused=%v and %d bytes", len(got.data), got.err, tc.refused, tc.wantLen)
			}
			if strings.Contains(string(got.data), secret) || (got.err != nil && strings.Contains(got.err.Error(), secret)) {
				t.Error("the read returned the content of a file behind a link")
			}
		})
	}
}

// TestV1r15StatusReadsTheCertificateThroughReadCertFile: derp.server.status reads derper's certificate through the
// descriptor read, and a certificate that read refuses is derp.not_ready, as an unreadable one always was.
func TestV1r15StatusReadsTheCertificateThroughReadCertFile(t *testing.T) {
	livePEM, livePin := ssCert(t, ssIP, ssLiveAfter)
	crt := server.CertDir + "/" + ssIP + ".crt"
	setup := func(t *testing.T) *dpHost {
		t.Helper()
		h := newDPHost(t)
		ssMaps(h)
		ssInstall(t, h, ssRow(server.ModeIP, ssIP))
		h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
		h.files[crt] = livePEM
		return h
	}
	t.Run("a readable certificate", func(t *testing.T) {
		h := setup(t)
		got, _, err := ssRead(t, v1r15Split(t, h), h.env(""), `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if got.CertName != livePin {
			t.Errorf("certificate %q, want %q", got.CertName, livePin)
		}
	})
	t.Run("a certificate the read refuses", func(t *testing.T) {
		h := setup(t)
		d := v1r15Split(t, h)
		d.d.readCertFile = func(string) ([]byte, error) { return nil, errors.New("platform: a symbolic link in the path") }
		_, _, err := ssRead(t, d, h.env(""), `{}`)
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady {
			t.Errorf("err = %v, want derp.not_ready", err)
		}
	})
}

// TestV1r15CertRenewReadsCertificateAndKeyThroughReadCertFile: the renewal's plan, its wait for the new pair and
// its key check go through the descriptor read, and a key the read refuses refuses the renewal before anything moves.
func TestV1r15CertRenewReadsCertificateAndKeyThroughReadCertFile(t *testing.T) {
	ctx := context.Background()
	t.Run("plan and apply", func(t *testing.T) {
		h := crInstalled(t)
		d := v1r15Split(t, h.dpHost)
		if _, err := d.certRenew().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		var err error
		crDrive(t, h.dpHost, h.derperRestarts(), func() {
			_, err = d.certRenew().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a key the read refuses", func(t *testing.T) {
		h := crInstalled(t)
		d := v1r15Split(t, h.dpHost)
		d.d.readCertFile = func(name string) ([]byte, error) {
			if name == crKey {
				return nil, errors.New("platform: a symbolic link in the path")
			}
			return h.readFile(name)
		}
		_, err := d.certRenew().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) || !strings.Contains(coded.Detail, "key") {
			t.Fatalf("err = %v, want a definite derp.not_ready naming the key file", err)
		}
		if len(h.units.Calls) != 0 || len(crEffects(t, h.dpHost)) != 0 {
			t.Errorf("unit calls %q and %d effects, want none", h.units.Calls, len(crEffects(t, h.dpHost)))
		}
	})
}

// TestV1r15InstallReadsTheCertificateThroughReadCertFile: the install task's wait for derper's certificate goes
// through the descriptor read.
func TestV1r15InstallReadsTheCertificateThroughReadCertFile(t *testing.T) {
	s := inNewSetup(t)
	s.active()
	der, _ := s.certFile(t)
	plain := s.d.d.readFile
	s.d.d.readFile = func(name string) ([]byte, error) {
		if strings.HasPrefix(name, server.CertDir+"/") {
			t.Errorf("readFile opened %s; the certificate directory is read through readCertFile", name)
		}
		return plain(name)
	}
	s.d.d.readCertFile = s.h.readFile
	var out json.RawMessage
	var err error
	inDrive(t, s.h, func() {
		out, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
	})
	if err != nil {
		t.Fatal(err)
	}
	var got ServerInstalled
	if err := json.Unmarshal(out, &got); err != nil || got.CertName != server.Pin(der) {
		t.Errorf("output %s (error %v), want the pin of the certificate that was read", out, err)
	}
}
```

Update the existing tests that need the new dependency, in the same step:

1. `testhost_test.go`, the `dp()` literal: add `readCertFile: h.readFile,` after `readFile: h.readFile,`.
2. `deps_test.go`, `TestSdProductionDP`: add `"readCertFile": d.readCertFile != nil,` to the map of deps.
3. `server_install_test.go`, `TestInTaskWaitsForTheCertificate` (line 588) and the subtest `a half-written certificate that parses later` (line 883): each wraps `s.d.d.readFile` to fake the certificate appearing late. Both wrap `s.d.d.readCertFile` instead: `readFile := s.d.d.readFile` becomes `readCertFile := s.d.d.readCertFile`, the assignment target becomes `s.d.d.readCertFile`, and the final `return readFile(name)` becomes `return readCertFile(name)`. Their bodies are otherwise unchanged.

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/15 && export TMPDIR=/root/.cache/w10b-tmp/15 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r15' ./internal/platform/ ./internal/modules/derp/
```

Expected: both packages fail to build, the first errors naming `undefined: ReadRegularFile` (platform) and `d.d.readCertFile undefined (type deps has no field or method readCertFile)` (derp). Record them verbatim. After you add `ReadRegularFile` and the `readCertFile` field with its production value, but before you change the call sites, the call-site tests fail with `readFile opened /var/lib/darkpanel/derp/certs/…; the certificate directory is read through readCertFile`. That output shows the finding: today a plain `os.ReadFile` reads the certificate. Record it too.

- [ ] **Step 3: Implement**

1. `platform/files.go`: add `ReadRegularFile(path string, limit int64) ([]byte, error)` with a doc comment that says why it exists: a service account can write the directory it reads (ADR 0009). It calls `unix.Openat2(unix.AT_FDCWD, path, …)` with flags `O_RDONLY|O_NONBLOCK|O_CLOEXEC` (O_NONBLOCK keeps a FIFO from blocking the open, as `parent.read` does) and `Resolve: unix.RESOLVE_NO_SYMLINKS`. Wrap the file in `os.NewFile` and close it on return.
   - `ELOOP` becomes `platform: <path> has a symbolic link in its path`. Every other open error becomes `platform: open <path>: %w`, so `ENOENT` still matches `fs.ErrNotExist`.
   - `Fstat` the descriptor. A mode other than a regular file is `platform: <path> is not a regular file`. A size above `limit` is `platform: <path> is larger than <limit> bytes`.
   - Read through `io.LimitReader(file, limit+1)`. A result longer than `limit` gets the same "larger than" error, which covers a file that grew after the `Fstat`. On any failure return no data, and put no file content in an error.
2. `derp/deps.go`: add `certFileLimit = 1 << 20` to the constants with a comment that it is the most a certificate or key file may hold. Add the `readCertFile` field to `deps` with a comment in the style of its neighbours (production: `platform.ReadRegularFile` with `certFileLimit`; certificate and key files under `CertDir`). Set it in `productionDP` to a closure that calls `platform.ReadRegularFile(name, certFileLimit)`. Leave `readFile: os.ReadFile` as it is, change its field comment to say that it serves the binary, unit link and existence checks (it no longer reads certificate files), and import the `platform` package.
3. Change these five reads from `readFile` to `readCertFile`: `server_status.go` `certificate`; `server_install.go` `readCert`; `cert_renew.go` `certRenewRead`, the key check in `certRenewCheck`, and the key check inside the wait loop of the renewal's apply. Their error handling stays: an absent certificate is still `fs.ErrNotExist`, and an unreadable one is still `derp.not_ready` or the existing wrapped error. The comment "Only the key's presence matters" stays true: the key's bytes are dropped. Do not touch the other `readFile` callers (binary, unit link, `exists`, `current`).

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/15 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r15' -v ./internal/platform/ ./internal/modules/derp/
timeout 600 go test -count=1 ./internal/platform/... ./internal/modules/derp/...
```

Expected: PASS.

- [ ] **Step 5: Run the task's checks**

```sh
export TMPDIR=/root/.cache/w10b-tmp/15 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 600 go test -race -count=1 ./internal/platform/ ./internal/modules/derp/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short schemas docs/api
rm -rf /root/.cache/w10b-tmp/15 && go clean -testcache
```

Expected: no vet, `go mod tidy -diff` or `git status` output, every test line `ok` or `?`, and `0 issues.` The task changes no wire type, so `schemas/` and `docs/api/` stay unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/platform/files.go internal/platform/files_read_test.go internal/modules/derp
git commit -m "fix(derp): read certificate files without links"
```

### Task 16: Refuse private DERP probe targets and classify probe errors

**Files:**
- Modify: `internal/modules/derp/node_probe.go:27-35, 40-65, 67-112, 162-196, 257-280` (the action spec, `Read`, `nodeProbeCheck`, `nodeProbeAllowedAddr`, `nodeProbeResolve`, `probeOnce`, `nodeProbeTLS`, `nodeProbeSTUN`, `nodeProbeResult`)
- Modify: `internal/modules/derp/wire.go` (`ProbeResult`, the `jsonschema_description` of its `Error` field)
- Modify: `internal/modules/derp/deps.go` (`probeTLS` and `pinnedLeaf`, which Task 15 edits first: their errors gain the sentinels of item 4 below)
- Create: `internal/modules/derp/node_probe_private_test.go`
- Test (modify): `internal/modules/derp/node_probe_test.go`, `internal/modules/derp/register_test.go:114`
- Regenerate: `schemas/derp.node.probe/**`, `schemas/derp.server.probe/**` and `docs/api/**` (`make schemas`, `make api-docs`)
- Read only: `internal/core/scope.go` (`RequiredScope` is the higher of the class default and `MinScope`; `MinScope` only raises)

**Interfaces:**
- Consumes: Contracts "Probe errors", exactly: `""` when the probe succeeded, otherwise one of `timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`, `certificate_not_trusted`, `pin_mismatch`, `bad_answer`, `no_stun_answer`, `failed`. The raw error is logged on the agent at debug level only, and never returned.
- Consumes: Task 7's `Warnings` fields on `RegionChanged` and `ServerRemoved` in `wire.go`. This task edits `ProbeResult` in the same file by type name and leaves those types alone.
- Produces: `derp.node.probe` declares `MinScope: core.ScopeOperate`, so `core.RequiredScope` of its spec is `operate`. The class stays `read`. `derp.server.probe` stays `read`: it builds its probe inputs from server records, never from caller text.
- Produces: `nodeProbeAllowedAddr` also refuses private addresses (RFC 1918 and ULA `fc00::/7`, `netip.Addr.IsPrivate` after unmapping) and CGNAT `100.64.0.0/10`. `nodeProbeCheck` and `nodeProbeResolve` use it for literals, for `address` and for resolved answers, which also serve `derp.server.probe` through `dp.target`.
- Produces: `ProbeResult.Error` carries only the vocabulary above, for `derp.node.probe` and `derp.server.probe`. `probeOnce(ctx, t, stunAddr)` keeps its signature and logs nothing; `Read` logs through a new variant that takes the agent's logger.
- Produces: the unexported sentinels `errProbePinMismatch`, `errProbeBadAnswer` and `errProbeCertInvalid` in `node_probe.go`.
- This task changes a wire type description and an action's required scope, so the Generated files rule applies.

**Items:** 2026-11-06-v1-security-review#9, 2026-10-21-v1-completion#306

**Depends on:** Task 7, Task 15
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/derp/node_probe_private_test.go`:

```go
package derp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// v1r16Reaching makes d's probes succeed at once and records what they were asked.
func v1r16Reaching(d dp) (dp, *[]string) {
	probed := &[]string{}
	d.d.tlsProbe = func(_ context.Context, target probeTarget) (time.Duration, error) {
		*probed = append(*probed, "tls "+target.HostName+" "+target.Address)
		return time.Millisecond, nil
	}
	d.d.stun = func(_ context.Context, addr netip.AddrPort) (time.Duration, error) {
		*probed = append(*probed, "stun "+addr.String())
		return time.Millisecond, nil
	}
	return d, probed
}

// v1r16Leaf is the DER of a self-signed certificate valid between the two times.
func v1r16Leaf(t *testing.T, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "203.0.113.9"},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestV1r16NodeProbeNeedsOperate(t *testing.T) {
	h := newDPHost(t)
	probe := h.dp().nodeProbe().Spec
	if got := core.RequiredScope(probe); got != core.ScopeOperate {
		t.Errorf("derp.node.probe needs scope %s, want %s", got, core.ScopeOperate)
	}
	if probe.Class != core.ClassRead || probe.Side != core.SideAgent {
		t.Errorf("derp.node.probe is class %s on the %s, want a read on the agent", probe.Class, probe.Side)
	}
	if got := core.RequiredScope(h.dp().serverProbe().Spec); got != core.ScopeRead {
		t.Errorf("derp.server.probe needs scope %s, want %s: it builds its targets from server records", got, core.ScopeRead)
	}
}

// TestV1r16ProbeRefusesPrivateLiterals: RFC 1918, CGNAT and ULA targets, as host name or address and in their
// IPv4-mapped forms, are refused before anything is probed or resolved.
func TestV1r16ProbeRefusesPrivateLiterals(t *testing.T) {
	private := []string{
		"10.0.0.1", "10.255.255.254", "172.16.0.1", "172.31.255.254", "192.168.1.1", "192.168.255.254",
		"100.64.0.1", "100.127.255.254", "fc00::1", "fd12:3456:789a::1",
		"::ffff:10.0.0.1", "::ffff:192.168.0.1", "::ffff:100.64.0.1", "::ffff:172.16.0.1",
	}
	for _, ip := range private {
		for _, tc := range []struct{ field, input string }{
			{"/hostName", `{"hostName":"` + ip + `"}`},
			{"/address", `{"hostName":"derp.example.com","address":"` + ip + `"}`},
		} {
			t.Run(tc.field+" "+ip, func(t *testing.T) {
				h := newDPHost(t)
				_, err := npRead(t, npNoProbe(t, h.dp()), h.env(""), tc.input)
				coded := errcode.From(err)
				if err == nil || coded.Code != errcode.InvalidInput || !core.IsDefinite(err) || len(coded.Fields) == 0 || coded.Fields[0].Path != tc.field {
					t.Errorf("err = %v (fields %+v), want a definite request.invalid_input at %s", err, coded.Fields, tc.field)
				}
			})
		}
	}
	for _, ip := range []string{"172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "192.167.255.255", "192.169.0.1", "9.255.255.255", "11.0.0.1", "2001:db8::1", "fbff:ffff::1"} {
		t.Run("public "+ip, func(t *testing.T) {
			h := newDPHost(t)
			d, probed := v1r16Reaching(h.dp())
			got, err := npRead(t, d, h.env(""), `{"hostName":"`+ip+`"}`)
			if err != nil || !got.TLS.OK || !got.STUN.OK || len(*probed) != 2 {
				t.Errorf("probe %+v, err %v, probed %v; want both probes to run", got, err, *probed)
			}
		})
	}
}

// TestV1r16ProbeResolveDropsPrivateAnswers: private, CGNAT, ULA and mapped answers are dropped before the first
// public one is chosen, and a name with only such answers is probed at nothing and reported as a plain failure.
func TestV1r16ProbeResolveDropsPrivateAnswers(t *testing.T) {
	addrs := func(ss ...string) []netip.Addr {
		var out []netip.Addr
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
	t.Run("a public answer among private ones", func(t *testing.T) {
		h := newDPHost(t)
		d, probed := v1r16Reaching(h.dp())
		d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			return addrs("10.0.0.7", "192.168.1.5", "100.64.0.9", "fd00::1", "::ffff:172.16.0.4", "::ffff:10.1.1.1", "203.0.113.7"), nil
		}
		got, err := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
		if err != nil || !got.TLS.OK || !got.STUN.OK {
			t.Fatalf("probe %+v, err %v, want both probes to run", got, err)
		}
		if want := []string{"tls derp.example.com 203.0.113.7", "stun 203.0.113.7:3478"}; !slices.Equal(*probed, want) {
			t.Errorf("probed %v, want %v", *probed, want)
		}
	})
	t.Run("only private answers", func(t *testing.T) {
		h := newDPHost(t)
		d, probed := v1r16Reaching(h.dp())
		d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			return addrs("10.0.0.7", "192.168.1.5", "fd00::1", "::ffff:100.64.0.9"), nil
		}
		got, err := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := ProbeResult{Error: "failed"}
		if got.TLS != want || got.STUN != want {
			t.Errorf("tls %+v and stun %+v, want both %+v", got.TLS, got.STUN, want)
		}
		if len(*probed) != 0 {
			t.Errorf("probed %v, want nothing", *probed)
		}
		out, _ := json.Marshal(got)
		for _, leak := range []string{"10.0.0.7", "192.168.1.5", "derp.example.com"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("the answer %s names %s", out, leak)
			}
		}
		_, _, err = d.target(context.Background(), ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"})
		if err == nil {
			t.Error("the controller's own target accepted a name with only private answers")
		}
	})
}

// TestV1r16ProbeErrorsUseTheFixedVocabulary: every failure of a TLS or STUN measurement is exactly one value of the
// vocabulary, wrapped as the probes wrap them, and no raw text, such as the internal name a certificate carries,
// reaches the result.
func TestV1r16ProbeErrorsUseTheFixedVocabulary(t *testing.T) {
	const internal = "intranet.corp.example"
	wrap := func(err error) error { return fmt.Errorf("derp: probe derp.example.com: %w", err) }
	hostErr := x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{internal}}, Host: "derp.example.com"}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	stunAddr := npAddrPort("203.0.113.9:3478")
	run := func(t *testing.T, tlsErr, stunErr error) (ProbeResult, ProbeResult) {
		t.Helper()
		h := newDPHost(t)
		h.tls = map[string]dpProbe{"203.0.113.9": {d: time.Millisecond, err: tlsErr}}
		h.stun = map[netip.AddrPort]dpProbe{stunAddr: {d: time.Millisecond, err: stunErr}}
		return h.dp().probeOnce(context.Background(), probeTarget{HostName: "203.0.113.9", Port: 443}, stunAddr)
	}
	check := func(t *testing.T, got ProbeResult, want string) {
		t.Helper()
		if got.OK || got.LatencyMs != 0 || got.Error != want {
			t.Errorf("result %+v, want a failure %q", got, want)
		}
		out, _ := json.Marshal(got)
		for _, leak := range []string{internal, "derp.example.com", "boom"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("the result %s carries %q", out, leak)
			}
		}
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a deadline", context.DeadlineExceeded, "timeout"},
		{"a dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, "timeout"},
		{"a refused connection", refused, "connection_refused"},
		{"no route to the network", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}, "unreachable"},
		{"no route to the host", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}, "unreachable"},
		{"a verification that names an internal certificate", &tls.CertificateVerificationError{Err: hostErr}, "certificate_not_trusted"},
		{"an unknown authority", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "certificate_not_trusted"},
		{"a TLS alert", tls.AlertError(40), "tls_handshake_failed"},
		{"a server that does not speak TLS", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "tls_handshake_failed"},
		{"a plain HTTP answer to the handshake", http.ErrSchemeMismatch, "tls_handshake_failed"},
		{"anything else", errors.New("boom " + internal), "failed"},
	} {
		t.Run("tls "+tc.name, func(t *testing.T) {
			got, stun := run(t, wrap(tc.err), nil)
			check(t, got, tc.want)
			if !stun.OK {
				t.Errorf("STUN %+v, want OK", stun)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no answer", fmt.Errorf("stun: no answer from %v: %w", stunAddr, context.DeadlineExceeded), "no_stun_answer"},
		{"a refused port", fmt.Errorf("stun: read from %v: %w", stunAddr, os.NewSyscallError("read", syscall.ECONNREFUSED)), "connection_refused"},
		{"no route", fmt.Errorf("stun: send to %v: %w", stunAddr, os.NewSyscallError("sendto", syscall.ENETUNREACH)), "unreachable"},
		{"anything else", errors.New("stun: open socket: boom " + internal), "failed"},
	} {
		t.Run("stun "+tc.name, func(t *testing.T) {
			tlsRes, got := run(t, nil, tc.err)
			check(t, got, tc.want)
			if !tlsRes.OK {
				t.Errorf("TLS %+v, want OK", tlsRes)
			}
		})
	}
}

// TestV1r16RealTLSFailuresUseTheVocabulary: the errors the production TLS probe produces against real local servers
// map to the vocabulary, so the classification does not rest on the shapes the test above builds by hand.
func TestV1r16RealTLSFailuresUseTheVocabulary(t *testing.T) {
	stun := func(context.Context, netip.AddrPort) (time.Duration, error) { return time.Millisecond, nil }
	d := dp{d: deps{tlsProbe: probeTLS, stun: stun}}
	probe := func(ctx context.Context, hostPort, pin string) ProbeResult {
		t.Helper()
		host, portText, err := net.SplitHostPort(hostPort)
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := d.probeOnce(ctx, probeTarget{HostName: host, Port: port, CertName: pin}, netip.AddrPortFrom(netip.MustParseAddr(host), server.STUNPort))
		return got
	}
	tlsServer := func(status int) (hostPort, pin string) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(srv.Close)
		return strings.TrimPrefix(srv.URL, "https://"), server.Pin(srv.Certificate().Raw)
	}
	ctx := context.Background()

	okAddr, okPin := tlsServer(http.StatusOK)
	if got := probe(ctx, okAddr, okPin); !got.OK || got.Error != "" {
		t.Errorf("a pinned server that answers 200: %+v, want OK", got)
	}
	failAddr, failPin := tlsServer(http.StatusInternalServerError)
	for _, tc := range []struct {
		name, hostPort, pin, want string
	}{
		{"an answer other than 200", failAddr, failPin, "bad_answer"},
		{"another pin", okAddr, server.PinPrefix + strings.Repeat("0", 64), "pin_mismatch"},
		{"a certificate no root signed", okAddr, "", "certificate_not_trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probe(ctx, tc.hostPort, tc.pin); got.OK || got.Error != tc.want {
				t.Errorf("result %+v, want a failure %q", got, tc.want)
			}
		})
	}

	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(plain.Close)
	if got := probe(ctx, strings.TrimPrefix(plain.URL, "http://"), ""); got.OK || got.Error != "tls_handshake_failed" {
		t.Errorf("a server that does not speak TLS: %+v, want tls_handshake_failed", got)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()
	if got := probe(ctx, closedAddr, ""); got.OK || got.Error != "connection_refused" {
		t.Errorf("a port nobody listens on: %+v, want connection_refused", got)
	}

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = silent.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if got := probe(short, silent.Addr().String(), ""); got.OK || got.Error != "timeout" {
		t.Errorf("a server that never answers: %+v, want timeout", got)
	}
}

// TestV1r16PinnedLeafFailuresUseTheVocabulary: the errors of the pin check itself.
func TestV1r16PinnedLeafFailuresUseTheVocabulary(t *testing.T) {
	now := time.Now()
	valid := v1r16Leaf(t, now.Add(-time.Hour), now.Add(time.Hour))
	expired := v1r16Leaf(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	stunAddr := npAddrPort("203.0.113.9:3478")
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"a leaf with another pin", func() error {
			return pinnedLeaf(server.PinPrefix+strings.Repeat("0", 64))([][]byte{valid}, nil)
		}, "pin_mismatch"},
		{"an expired leaf with the right pin", func() error { return pinnedLeaf(server.Pin(expired))([][]byte{expired}, nil) }, "certificate_not_trusted"},
		{"no certificate", func() error { return pinnedLeaf(server.Pin(valid))(nil, nil) }, "certificate_not_trusted"},
		{"bytes that are no certificate", func() error { return pinnedLeaf(server.Pin(valid))([][]byte{[]byte("junk")}, nil) }, "certificate_not_trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("the pin check accepted the leaf")
			}
			h := newDPHost(t)
			h.tls = map[string]dpProbe{"203.0.113.9": {err: fmt.Errorf("derp: probe 203.0.113.9: %w", err)}}
			h.stun = map[netip.AddrPort]dpProbe{stunAddr: {d: time.Millisecond}}
			got, _ := h.dp().probeOnce(context.Background(), probeTarget{HostName: "203.0.113.9", Port: 443}, stunAddr)
			if got.OK || got.Error != tc.want {
				t.Errorf("result %+v, want a failure %q", got, tc.want)
			}
		})
	}
}

// TestV1r16RawProbeErrorsAreLoggedAtDebugOnly: the agent keeps the raw error of a failed probe in its debug log, and
// in no log of a higher level and no answer.
func TestV1r16RawProbeErrorsAreLoggedAtDebugOnly(t *testing.T) {
	const internal = "intranet.corp.example"
	raw := errors.New("derp: probe: certificate is valid for " + internal)
	for _, tc := range []struct {
		name   string
		level  slog.Level
		logged bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			h.tls = map[string]dpProbe{"203.0.113.9": {err: raw}}
			h.stun = map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {err: raw}}
			var buf bytes.Buffer
			env := h.env("")
			env.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: tc.level}))
			got, err := npRead(t, h.dp(), env, `{"hostName":"203.0.113.9"}`)
			if err != nil {
				t.Fatal(err)
			}
			if got.TLS.Error != "failed" || got.STUN.Error != "failed" {
				t.Errorf("tls %+v and stun %+v, want both failing as \"failed\"", got.TLS, got.STUN)
			}
			if out, _ := json.Marshal(got); strings.Contains(string(out), internal) {
				t.Errorf("the answer %s carries the raw error", out)
			}
			if logged := strings.Contains(buf.String(), internal); logged != tc.logged {
				t.Errorf("the raw error is in the %s-level log: %v, want %v (log: %s)", tc.name, logged, tc.logged, buf.String())
			}
		})
	}
}
```

Update the existing tests that pinned the old behaviour, in the same step:

1. `register_test.go`, `TestRegManifestDeclaresTheActions`: before the long `if s.Version != 1 || …` condition add `wantMin := core.Scope("")` and `if want.name == "derp.node.probe" { wantMin = core.ScopeOperate }`, and replace `s.MinScope != ""` in the condition by `s.MinScope != wantMin`.
2. `node_probe_test.go`, `TestNpProbeOnce`: the two expected failures `ProbeResult{Error: "np: boom"}` (row `the TLS probe fails, keyed by the address`, field `wantTLS`; row `the STUN probe fails`, field `wantSTUN`) become `ProbeResult{Error: "failed"}`.
3. `node_probe_test.go`, `TestNpResolveKeepsOnlyAddressesTheProbeMayReach`, second loop: the `err` check against `d.target` stays, since the controller's own error still names the name. The `got.TLS.Error` and `got.STUN.Error` check becomes `if got.TLS != (ProbeResult{Error: "failed"}) || got.STUN != (ProbeResult{Error: "failed"}) { t.Errorf("read: tls %+v and stun %+v, want both failing as \"failed\"", got.TLS, got.STUN) }`, once, outside the `for _, part := range tc.named` loop and not inside it.
4. `node_probe_test.go`, `TestNpReadProbeFailures`: in the first part `!strings.Contains(r.Error, "derp.example.com")` becomes `r.Error != "failed"`, and the message reads `want a failure "failed"`. In the second part `want` becomes `NodeProbe{TLS: ProbeResult{Error: "failed"}, STUN: ProbeResult{Error: "failed"}}`.

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/16 && export TMPDIR=/root/.cache/w10b-tmp/16 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r16|TestNp|TestRegManifest' ./internal/modules/derp/
```

Expected: the package builds, and these fail on assertions. Record the output verbatim.
- `TestV1r16NodeProbeNeedsOperate`: `derp.node.probe needs scope read`.
- `TestV1r16ProbeRefusesPrivateLiterals`: `err = <nil>` for every private literal.
- `TestV1r16ProbeResolveDropsPrivateAnswers`: the probe runs at `10.0.0.7`.
- `TestV1r16ProbeErrorsUseTheFixedVocabulary`: results such as `Error:"derp: probe derp.example.com: x509: certificate is valid for intranet.corp.example, not derp.example.com"`.
- `TestV1r16RawProbeErrorsAreLoggedAtDebugOnly`: the answer carries the raw error.

- [ ] **Step 3: Implement**

1. `node_probe.go`, the action spec of `nodeProbe`: add `MinScope: core.ScopeOperate`. Extend the doc comment: the action reaches the network from the agent's host, so the lowest scope may not call it.
2. `nodeProbeAllowedAddr`: after the zone check and `Unmap`, also refuse `IsPrivate()` (RFC 1918 and ULA `fc00::/7`) and the CGNAT prefix `100.64.0.0/10`, which `IsPrivate` does not cover. Keep the loopback, unspecified, multicast and link-local refusals. Update its doc comment and the doc comment of `nodeProbeCheck`.
3. `nodeProbeCheck` and `nodeProbeResolve`: the refusal text of a literal host name and of an address now ends `…that is not loopback, unspecified, multicast, link-local, private (RFC 1918 or ULA) or shared (CGNAT)`. `nodeProbeResolve` already filters answers through `nodeProbeAllowedAddr` and unmaps the survivors, so it needs only the new rule and its error text to say "private or shared addresses" next to the classes it already names. Its error still names the host and the refused answers, because it goes to the debug log and to the controller's own source line, which comes from a server record.
4. Error classification, in `node_probe.go`:
   - Declare the sentinels `errProbePinMismatch`, `errProbeBadAnswer` and `errProbeCertInvalid`.
   - In `deps.go`, `pinnedLeaf` wraps its pin failure with `errProbePinMismatch` and every other failure (no certificate, unparseable leaf, not valid now) with `errProbeCertInvalid`, using `%w` and keeping the existing message text. `probeTLS` wraps the non-200 status error and the error of reading the body with `errProbeBadAnswer`.
   - Add `nodeProbeFailure(err error, stun bool) string`. It returns the first match of these checks in this order, each with `errors.Is` or `errors.As` so that `url.Error`, `net.OpError` and the `fmt.Errorf("…: %w")` wrappers are seen through:
     1. `errProbePinMismatch` gives `pin_mismatch`.
     2. `errProbeBadAnswer` gives `bad_answer`.
     3. `errProbeCertInvalid`, `*tls.CertificateVerificationError`, `x509.UnknownAuthorityError`, `x509.HostnameError` or `x509.CertificateInvalidError` gives `certificate_not_trusted`.
     4. `tls.AlertError`, `tls.RecordHeaderError` or `http.ErrSchemeMismatch` (which `net/http` returns in place of a `tls.RecordHeaderError` when a plain HTTP server answers the handshake) gives `tls_handshake_failed`.
     5. `syscall.ECONNREFUSED` gives `connection_refused`.
     6. `syscall.ENETUNREACH` or `syscall.EHOSTUNREACH` gives `unreachable`.
     7. `context.DeadlineExceeded` or a `net.Error` whose `Timeout()` is true gives `no_stun_answer` when `stun` is true and `timeout` otherwise.
     8. Anything else gives `failed`.
   - Replace `nodeProbeResult(latency, err)` by a form that takes the failure code from `nodeProbeFailure`. A success is unchanged: `OK` and `LatencyMs`. A failure is `ProbeResult{Error: <code>}` and never `err.Error()`.
5. Logging: add `probeOnceLogged(ctx, log *slog.Logger, t, stunAddr)`, with `nodeProbeTLS` and `nodeProbeSTUN` taking the logger too. A failed measurement logs one record at `slog.LevelDebug` with the probe kind (`tls` or `stun`), the target host and the raw error. A nil logger logs nothing. `probeOnce(ctx, t, stunAddr)` keeps its signature and calls `probeOnceLogged` with a nil logger, because `server_probe.go` and the existing tests call it. `Read` passes `env.Log`, and when `nodeProbeAddr` fails it logs that error at debug the same way and sets both results to `ProbeResult{Error: "failed"}` instead of `err.Error()`.
6. `wire.go`: change the `jsonschema_description` of `ProbeResult.Error` to `Why the probe failed: timeout, connection_refused, unreachable, tls_handshake_failed, certificate_not_trusted, pin_mismatch, bad_answer, no_stun_answer or failed; empty when OK.`
7. Regenerate the generated files and commit them with the task:

```sh
make schemas
make api-docs
```

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/16 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r16|TestNp|TestRegManifest' -v ./internal/modules/derp/
timeout 600 go test -count=1 ./internal/modules/derp/... ./internal/apidocs/...
```

Expected: PASS. `git status --short schemas docs/api` lists only `schemas/derp.node.probe`, `schemas/derp.server.probe` and `docs/api` files; the `docs/api` change includes the `operate` scope of `derp.node.probe`. Any other changed file means something else drifted: stop and report it.

- [ ] **Step 5: Run the task's checks**

```sh
export TMPDIR=/root/.cache/w10b-tmp/16 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 600 go test -race -count=1 ./internal/modules/derp/
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w10b-tmp/16 && go clean -testcache
```

Expected: no vet or `go mod tidy -diff` output, every test line `ok` or `?`, and `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/modules/derp schemas docs/api
git commit -m "fix(derp): refuse private probe targets and raw errors"
```

### Task 17: Key login locks by user and source prefix

**Files:**
- Modify: `internal/controller/auth/ratelimit.go:14-20, 68, 140-148, 182-199` (`limiter` doc comment, `usernameKey`, `blocked`, `fail`)
- Modify: `internal/controller/auth/login.go:66-72, 107-116` (`Login`, `evaluate`, and a new `limitSource`)
- Modify: `internal/controller/api/server.go:204-208` (the doc comment of `clientAddr`; no code changes in the `api` package)
- Create: `internal/controller/auth/loginlock_test.go`
- Create: `internal/controller/api/proxied_login_test.go`
- Test (modify): `internal/controller/auth/ratelimit_test.go`, `internal/controller/auth/login_test.go`
- Read only: `internal/core/peer.go` (`core.Proxied`, `core.ProxiedFrom`), `internal/controller/api/auth.go:48` (`handleLogin` passes `r.Context()` to `Login`, which is how the `core.Proxied` of a runtime listener reaches the limiter)

**Interfaces:**
- Consumes: Contracts "Login locks": the per-source lock is unchanged, the per-username lock becomes a lock on the pair (username, source key), and behind a runtime listener the source key comes from `core.Proxied.Client`, never from the audit source text `<client> via <proxy>`.
- Produces, in package `auth` (both unexported, no later task consumes them): `pairKey(username, source string) string`, the limiter key of one username from one source (it replaces `usernameKey`, which is deleted), and `limitSource(ctx context.Context, source string) string`.
- No exported name changes. `LoginRequest.Source` keeps its meaning: the audit and session source, `<client> via <proxy>` behind a runtime listener.

**Items:** 2026-11-06-v1-security-review#8, 2026-11-06-v1-security-review#13

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/auth/loginlock_test.go`. The first test pins the pair lock (review row 8: five failed logins for `admin` from anywhere lock `admin` for everyone). The third pins the source key behind a listener (row 13: the source text `<client> via <proxy>` is not an address, so an IPv6 /64 is not grouped):

```go
package auth

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// v1r17Burst is count failed logins of one username from one source.
type v1r17Burst struct {
	source string
	count  int
}

func TestV1r17LimiterLocksTheUserAndSourcePair(t *testing.T) {
	fiveV6 := []v1r17Burst{{"2001:db8:1:2::1", 1}, {"2001:db8:1:2::2", 1}, {"2001:db8:1:2::3", 1}, {"2001:db8:1:2::4", 1}, {"2001:db8:1:2::5", 1}}
	tests := []struct {
		name        string
		failures    []v1r17Burst
		user        string
		source      string
		wantBlocked bool
	}{
		{"same user and source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "10.0.0.1", true},
		{"same user, another IPv4 source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "10.0.0.2", false},
		{"another user, same source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "bob", "10.0.0.1", false},
		{"same user, IPv4-mapped form of the source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "::ffff:10.0.0.1", true},
		{"failures split over two sources", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit - 1}, {"10.0.0.2", UsernameFailureLimit - 1}}, "alice", "10.0.0.1", false},
		{"same user, another address of the /64", fiveV6, "alice", "2001:db8:1:2:ffff::9", true},
		{"same user, another /64", fiveV6, "alice", "2001:db8:1:3::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLimiter(coretest.NewFakeClock(t0))
			for _, b := range tt.failures {
				for range b.count {
					l.fail("alice", b.source)
				}
			}
			got := l.blocked(tt.user, tt.source)
			switch {
			case tt.wantBlocked && got != LockDuration:
				t.Errorf("%s from %s blocked for %s, want %s", tt.user, tt.source, got, LockDuration)
			case !tt.wantBlocked && got != 0:
				t.Errorf("%s from %s blocked for %s, want allowed", tt.user, tt.source, got)
			}
		})
	}
}

func TestV1r17LoginLockIsPerSource(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for range UsernameFailureLimit {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "10.0.0.1"})
		wantCode(t, err, errcode.InvalidCredentials)
	}
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	wantCode(t, err, errcode.RateLimited)
	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"}); err != nil {
		t.Errorf("alice from another source after five failures from the first: %v, want a session", err)
	}
}

// v1r17Via is an attempt that came through a runtime listener from client:
// the context carries the client the listener parsed and Source holds the
// audit text "<client> via <proxy>".
func v1r17Via(client, username string) (context.Context, LoginRequest) {
	ctx := core.WithProxied(context.Background(), core.Proxied{
		ListenerID: "L1",
		Client:     netip.MustParseAddr(client),
		Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
	})
	return ctx, LoginRequest{Username: username, Password: "wrong horse battery", Code: "000000", Source: client + " via 172.31.255.2"}
}

func TestV1r17LoginKeysTheProxiedClient(t *testing.T) {
	s, _, audit := newTestService(t)
	for i := range SourceFailureLimit {
		ctx, req := v1r17Via(fmt.Sprintf("2001:db8:1:2::%x", i+1), fmt.Sprintf("probe%d", i))
		_, err := s.Login(ctx, req)
		wantCode(t, err, errcode.InvalidCredentials)
	}
	ctx, req := v1r17Via("2001:db8:1:2:ffff::9", "probe99")
	_, err := s.Login(ctx, req)
	wantCode(t, err, errcode.RateLimited)

	ctx, req = v1r17Via("2001:db8:1:3::1", "probe98")
	_, err = s.Login(ctx, req)
	wantCode(t, err, errcode.InvalidCredentials)
	entries, _, _ := audit.List(context.Background(), core.AuditQuery{Limit: 1})
	if len(entries) != 1 || entries[0].Source != "2001:db8:1:3::1 via 172.31.255.2" {
		t.Errorf("audit entries %+v, want the source text %q", entries, "2001:db8:1:3::1 via 172.31.255.2")
	}
}
```

Create `internal/controller/api/proxied_login_test.go`. It drives the real handler and the real `auth.Service` of the `api` test harness, with the `core.Proxied` a runtime listener sets, so it proves the context reaches the limiter:

```go
package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// v1r17Login posts the admin's login through the runtime listener of
// pxProxied, as the client at address.
func v1r17Login(h *harness, client, password, code string) *httptest.ResponseRecorder {
	px := pxProxied(nil)
	px.Client = netip.MustParseAddr(client)
	r := request("POST", loginPath, loginBody("admin", password, code))
	return h.serve(r.WithContext(core.WithProxied(r.Context(), px)))
}

func TestV1r17ProxiedLoginLockFollowsTheClientPrefix(t *testing.T) {
	h := newHarness(t)
	for i := range auth.UsernameFailureLimit {
		rec := v1r17Login(h, fmt.Sprintf("2001:db8:1:2::%x", i+1), "wrong password!", "000000")
		decodeProblem(t, rec, 401, errcode.InvalidCredentials)
	}
	decodeProblem(t, v1r17Login(h, "2001:db8:1:2:ffff::9", testPassword, h.code()), 429, errcode.RateLimited)
	if rec := v1r17Login(h, "2001:db8:1:3::1", testPassword, h.code()); rec.Code != http.StatusOK {
		t.Errorf("admin from another /64 got %d %s, want 200", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/17 && export TMPDIR=/root/.cache/w10b-tmp/17 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r17' ./internal/controller/auth/ ./internal/controller/api/
```

Expected: FAIL, every failure by assertion on the current tree (the packages build):

- `TestV1r17LimiterLocksTheUserAndSourcePair`: three subtests fail, `same user, another IPv4 source`, `failures split over two sources` and `same user, another /64`, each with `blocked for 15m0s, want allowed`.
- `TestV1r17LoginLockIsPerSource`: `want a session` with `auth.rate_limited`.
- `TestV1r17LoginKeysTheProxiedClient`: `error = auth.invalid_credentials …, want auth.rate_limited`.
- `TestV1r17ProxiedLoginLockFollowsTheClientPrefix`: `admin from another /64 got 429 … want 200`.

- [ ] **Step 3: Implement**

1. `internal/controller/auth/ratelimit.go`, key function: replace `usernameKey(username string) string` with `pairKey(username, source string) string`. It returns `"u:"` + the username + `"@"` + the text `sourceKey(source)` returns without its `"s:"` prefix, so the IPv4 address, the IPv4 form of an IPv4-mapped address, or the /64 of an IPv6 address stays the source part. A username matches `^[a-z0-9][a-z0-9._-]{0,63}$` and holds no `@`, so the key is unambiguous. Delete `usernameKey`.
2. `blocked` and `fail`: wherever they used `usernameKey(username)`, use `pairKey(username, source)`. `UsernameFailureLimit` (5) now counts the failures of one pair, `SourceFailureLimit` (20) and the source key are unchanged, and `record`, `prune`, `allLocked`, `dropOldestUnlocked` and the key cap are unchanged. Update the doc comment of `limiter` ("per username ("u:" keys)") to say a "u:" key is one username from one source key.
3. `internal/controller/auth/login.go`: add `limitSource(ctx context.Context, source string) string`. When `core.ProxiedFrom(ctx)` reports a `core.Proxied`, it returns `p.Client.String()`. Otherwise it returns `source`. Call it in the four places that pass `r.Source` to the limiter: `s.limiter.blocked` and `s.limiter.fail` in `Login`, and the same two calls in `evaluate`, so each passes `limitSource(ctx, r.Source)`. `r.Source` still goes unchanged to `createSession` and `recordLogin`, so audit rows and sessions keep `<client> via <proxy>`.
4. `internal/controller/api/server.go`, doc comment of `clientAddr`: it says login rate limiting keys on `<client> via <proxy>`. Reword that last clause: the text is what the audit log and the principal record, and the login limits key on the client address in `core.Proxied`. Change no code in the `api` package.
5. Update the existing tests that encoded the old username lock. Make exactly these edits.

   In `internal/controller/auth/ratelimit_test.go`:

   - `TestUsernameLock`: replace the function with:

     ```go
     func TestUsernameLock(t *testing.T) {
     	clock := coretest.NewFakeClock(t0)
     	l := newLimiter(clock)
     	for range UsernameFailureLimit - 1 {
     		l.fail("alice", "10.0.0.1")
     	}
     	if d := l.blocked("alice", "10.0.0.1"); d != 0 {
     		t.Fatalf("blocked after %d failures for %s, want allowed", UsernameFailureLimit-1, d)
     	}
     	clock.Advance(time.Minute)
     	l.fail("alice", "10.0.0.1")
     	if d := l.blocked("alice", "10.0.0.1"); d != LockDuration {
     		t.Fatalf("after %d failures blocked for %s, want %s from the same source", UsernameFailureLimit, d, LockDuration)
     	}
     	if d := l.blocked("bob", "10.0.0.1"); d != 0 {
     		t.Errorf("another username is blocked for %s", d)
     	}
     	clock.Advance(LockDuration - time.Second)
     	if d := l.blocked("alice", "10.0.0.1"); d != time.Second {
     		t.Errorf("one second before the lock ends: blocked for %s, want 1s", d)
     	}
     	clock.Advance(time.Second)
     	if d := l.blocked("alice", "10.0.0.1"); d != 0 {
     		t.Errorf("lock outlived %s: blocked for %s", LockDuration, d)
     	}
     }
     ```

   - `TestBlockedReturnsTheLongerWait`: in the loop `for range UsernameFailureLimit` change `l.fail("alice", "10.0.0.2")` to `l.fail("alice", "10.0.0.1")`, and the message `want the username's %s` to `want the pair's %s`.
   - `TestStaleKeysArePruned`: replace `"u:stale"` with `pairKey("stale", "10.0.0.2")`, `"u:other"` with `pairKey("other", "10.0.0.3")`, `"u:new"` with `pairKey("new", "10.0.0.4")` and `"u:locked"` with `pairKey("locked", "10.0.0.1")`.
   - `TestLockedKeyIsKeptAfterItsFailuresAge`: change `l.blocked("alice", "10.0.0.3")` to `l.blocked("alice", "10.0.0.1")`.
   - `TestKeyCapDropsTheOldest`: replace the function with:

     ```go
     func TestKeyCapDropsTheOldest(t *testing.T) {
     	clock := coretest.NewFakeClock(t0)
     	l := newLimiter(clock)
     	l.max = 3
     	l.fail("a", "x") // the pair (a, x) and s:x at t0
     	clock.Advance(time.Second)
     	l.fail("b", "x") // the pair (b, x) is new and fits; s:x is refreshed
     	clock.Advance(time.Second)
     	l.fail("c", "x") // the pair (c, x) is new: the oldest, the pair (a, x), is dropped
     	if len(l.keys) != 3 {
     		t.Fatalf("%d keys kept, want the cap of 3", len(l.keys))
     	}
     	if _, ok := l.keys[pairKey("a", "x")]; ok {
     		t.Error("the oldest key was kept")
     	}
     	for _, k := range []string{pairKey("b", "x"), "s:x", pairKey("c", "x")} {
     		if _, ok := l.keys[k]; !ok {
     			t.Errorf("key %s was dropped", k)
     		}
     	}
     	if MaxLimiterKeys != 10000 || newLimiter(clock).max != MaxLimiterKeys {
     		t.Errorf("default cap is not %d", 10000)
     	}
     }
     ```

   - `TestLockSurvivesAFloodOfNewKeys`: change `l.blocked("alice", "192.0.2.1")` to `l.blocked("alice", "10.0.0.1")`.
   - `TestFullOfLockedKeysFailsClosed`: replace the `blocked("bob", "10.0.0.9")` check with `l.blocked("bob", "10.0.0.1")` (message `known locked username from its source: blocked for %s, want %s`), and the `l.keys["u:carol"]` check after `l.fail("carol", "10.0.0.9")` with:

     ```go
     	l.fail("carol", "10.0.0.9")
     	if carol := l.keys[pairKey("carol", "10.0.0.9")]; len(l.keys) != 3 || carol != nil {
     		t.Errorf("a failure displaced a locked key: %d keys, carol recorded %v", len(l.keys), carol != nil)
     	}
     ```

   - `TestOneFailureKeepsItsOwnKeys`: replace `l.keys["u:carol"]` with `l.keys[pairKey("carol", "10.0.0.9")]`.

   In `internal/controller/auth/login_test.go`:

   - `TestLoginRateLimit`: the first blocked attempt after the five failures uses `Source: "10.0.0.1"`, the source of the failures, instead of `"10.0.0.2"`.
   - `TestLoginFaultAfterPasswordCounts` and `TestLoginAuditFaultCounts`: replace each `usernameKey("alice")` with `pairKey("alice", "10.0.0.1")`.

6. Run `gofmt -l internal/controller/auth internal/controller/api`; it prints nothing.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/17 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r17' ./internal/controller/auth/ ./internal/controller/api/
timeout 900 go test -race -count=1 ./internal/controller/auth/ ./internal/controller/api/
```

Expected: `ok` for both packages, with no other test of either package changed beyond Step 3.5.

- [ ] **Step 5: Commit**

```sh
git add internal/controller/auth internal/controller/api
git commit -m "fix(auth): lock a username per source prefix"
```

- [ ] **Step 6: Run the task's gates**

```sh
export TMPDIR=/root/.cache/w10b-tmp/17 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/17 && go clean -testcache
```

Expected: every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go vet` and `go mod tidy -diff` print nothing, and `git status --short` prints nothing.

### Task 18: Record the public binding of an exposure's listener

**Files:**
- Modify: `internal/core/listeners.go:35-41` (`ListenerSpec` gains `PublicBinding`)
- Create: `internal/core/store/migrations/controller/0016_listener_public_binding.sql`
- Modify: `internal/controller/listen/runtime_store.go:22, 31-45, 102-127` (`listenerColumns`, `insertListener`, `scanListener`)
- Modify: `internal/controller/listen/runtime.go:410-435` (`validListenerSpec`)
- Create: `internal/core/store/migrations_listener_binding_test.go`
- Create: `internal/controller/listen/runtime_public_test.go`
- Read only: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section 6, Deploy: "80 and 443 published on all addresses")

**Interfaces:**
- Consumes: nothing from Contracts.
- Produces `core.ListenerSpec.PublicBinding netip.AddrPort`: the host binding NPM publishes its HTTPS port on, recorded with the exposure's runtime listener; the zero value for a tunnel. Task 19 sets it in the spec the panel module opens and reads it in `RuntimeHandler`. It is not a slice, so `ListenerSpec` stays comparable, as `listener_test.go` and `runtime_store_test.go` need. The spec's definition of `ListenerSpec` (1.12) is not edited here, because only Task 1 edits the spec.
- Produces the controller column `core_listeners.public_binding` (migration `0016_listener_public_binding`).

**Items:** 2026-11-06-v1-security-review#6

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/core/store/migrations_listener_binding_test.go`. It follows `TestW10AuServicePhasesMigration`: migrate to 0015, put two listeners in, migrate on. A listener recorded before the column existed through NPM gets the binding NPM publishes (443 on every address), a tunnel listener gets none:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestV1r18ListenerPublicBindingMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "w10a", time.Now()), coreSetUpTo(t, SideController, 15), nil); err != nil {
		t.Fatal(err)
	}
	const at = "2026-11-06T00:00:00.000000000Z"
	const insert = `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header, public_host, change_id, state,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)`
	for _, row := range [][]any{
		{"L1", "172.31.255.1", 9443, "172.31.255.2", "X-Darkpanel-Client", "panel.example.com", "C1", at, at},
		{"L2", "172.31.255.1", 9444, "172.31.255.2", "Cf-Connecting-IP", "panel.example.net", "C2", at, at},
	} {
		if _, err := db.Writer().ExecContext(ctx, insert, row...); err != nil {
			t.Fatalf("listener %v: %v", row[0], err)
		}
	}
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "w10b", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 16 || res.Applied[0].Name != "listener_public_binding" {
		t.Fatalf("applied %+v, want 0016_listener_public_binding first", res.Applied)
	}
	const rows = `SELECT json_array(id, public_binding) FROM core_listeners ORDER BY id`
	got := surfacesRows(t, db, rows)
	if want := []string{`["L1","0.0.0.0:443"]`, `["L2",""]`}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("rows after the migration = %v, want %v: an npm listener keeps the binding NPM publishes, a tunnel listener has none", got, want)
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header,
		public_host, change_id, state, created_at, updated_at) VALUES ('L3', '172.31.255.5', 9443, '172.31.255.6',
		'Cf-Connecting-IP', 'panel.example.org', 'C3', 'open', ?, ?)`, at, at); err != nil {
		t.Fatalf("a listener row without a public binding: %v", err)
	}
	if got := surfacesRows(t, db, `SELECT json_array(public_binding) FROM core_listeners WHERE id = 'L3'`); len(got) != 1 || got[0] != `[""]` {
		t.Errorf("a row inserted without the column reads %v, want an empty binding", got)
	}
}
```

Create `internal/controller/listen/runtime_public_test.go`. It reuses `rsSpec`, `rsInsert`, `openControllerDB` and `testStart` of the package's tests:

```go
package listen

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestV1r18StoreKeepsThePublicBinding(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	npm := rsSpec
	npm.PublicBinding = netip.MustParseAddrPort("0.0.0.0:443")
	rsInsert(t, db, "L1", npm, testStart)
	rsInsert(t, db, "L2", rsSpec, testStart.Add(time.Second))
	got, err := getListener(ctx, db, "L1")
	if err != nil || got.Spec != npm {
		t.Fatalf("getListener(L1) = %+v, %v; want spec %+v", got, err, npm)
	}
	if got, err := getListener(ctx, db, "L2"); err != nil || got.Spec.PublicBinding.IsValid() {
		t.Fatalf("getListener(L2) = %+v, %v; want no public binding", got, err)
	}
	open, err := listListeners(ctx, db, core.ListenerOpen)
	if err != nil || len(open) != 2 || open[0].Spec.PublicBinding != npm.PublicBinding || open[1].Spec.PublicBinding.IsValid() {
		t.Fatalf("listListeners = %+v, %v; want L1 with the binding and L2 without", open, err)
	}
}

func TestV1r18ValidListenerSpecChecksThePublicBinding(t *testing.T) {
	for _, tt := range []struct {
		name    string
		binding string
		want    string // the recorded binding, "" for none
		refused bool
	}{
		{"none", "", "", false},
		{"every address", "0.0.0.0:443", "0.0.0.0:443", false},
		{"IPv4-mapped", "[::ffff:192.0.2.1]:443", "192.0.2.1:443", false},
		{"port 0", "0.0.0.0:0", "", true},
		{"zone", "[fe80::1%eth0]:443", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := rsSpec
			if tt.binding != "" {
				spec.PublicBinding = netip.MustParseAddrPort(tt.binding)
			}
			got, err := validListenerSpec(spec)
			if tt.refused {
				if err == nil || errcode.From(err).Code != errcode.InvalidInput {
					t.Fatalf("validListenerSpec = %+v, %v; want %s", got, err, errcode.InvalidInput)
				}
				return
			}
			text := ""
			if got.PublicBinding.IsValid() {
				text = got.PublicBinding.String()
			}
			if err != nil || text != tt.want {
				t.Fatalf("validListenerSpec binding = %q, %v; want %q", text, err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/18 && export TMPDIR=/root/.cache/w10b-tmp/18 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r18' ./internal/core/store/ ./internal/controller/listen/
```

Expected: FAIL.

- `internal/core/store`: `applied [] want 0016_listener_public_binding first`.
- `internal/controller/listen`: build failure, `npm.PublicBinding undefined (type core.ListenerSpec has no field or method PublicBinding)`.

- [ ] **Step 3: Implement**

1. `internal/core/listeners.go`: add `PublicBinding netip.AddrPort` to `ListenerSpec`, after `ChangeID`, with a doc comment: the host binding NPM publishes its HTTPS port on, which is the public leg of an NPM exposure; the zero value for a tunnel. Keep every field a comparable type.
2. Create `internal/core/store/migrations/controller/0016_listener_public_binding.sql` with this content (a migration, so the SQL is the deliverable). The backfill gives a listener recorded before the column existed the binding NPM publishes, so an upgrade keeps its guard protection:

   ```sql
   -- public_binding is the host binding an NPM exposure's proxy publishes its
   -- HTTPS port on, "" for a listener that serves a tunnel. NPM publishes 443 on
   -- every address, so a listener recorded before the column existed gets that.

   ALTER TABLE core_listeners ADD COLUMN public_binding TEXT NOT NULL DEFAULT '';

   UPDATE core_listeners SET public_binding = '0.0.0.0:443' WHERE client_header = 'X-Darkpanel-Client';
   ```

3. `internal/controller/listen/runtime_store.go`:
   - `listenerColumns` gains `public_binding` as its last column, after `state`.
   - `insertListener` writes `""` when `spec.PublicBinding` is not valid and `spec.PublicBinding.String()` otherwise, between `state` and `created_at`, with one more placeholder. A zero `netip.AddrPort` prints `invalid AddrPort`, which is why the guard on `IsValid` matters.
   - `scanListener` scans the column into a string. An empty string leaves the zero value. Any other text goes through `netip.ParseAddrPort`, and a failure returns `listen: runtime listener <id> has a bad public binding "<text>"`.
4. `internal/controller/listen/runtime.go`, `validListenerSpec`: a valid `PublicBinding` with port 0 or a zone is refused with `errcode.InvalidInput` through the existing `bad` helper, detail `the public binding needs an IP address without a zone and a port`. A valid binding is returned with its address unmapped, as `Addr` and `AllowedPeer` are. The zero value stays allowed.
5. Run `gofmt -l internal` and fix what it lists in the files this task touched.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/18 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r18' ./internal/core/store/ ./internal/controller/listen/
timeout 1200 go test -race -count=1 ./internal/core/store/ ./internal/controller/listen/
```

Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```sh
git add internal/core internal/controller/listen
git commit -m "feat(listen): record a listener's public binding"
```

- [ ] **Step 6: Run the task's gates**

```sh
export TMPDIR=/root/.cache/w10b-tmp/18 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/18 && go clean -testcache
```

Expected: every `go test` line is `ok` or `?` (the backup, server and dispatch packages read `core_listeners`, so they must stay green), `golangci-lint` prints `0 issues.`, `go vet` and `go mod tidy -diff` print nothing, and `git status --short` prints nothing.

### Task 19: Protect the public leg of an NPM panel exposure

**Files:**
- Modify: `internal/modules/panel/listener.go` (the `core.ListenerSpec` that `Apply` passes to `env.Listeners.Open`, and a new constant)
- Modify: `internal/controller/api/runtime.go:25-31` (`RuntimeHandler`: the `core.ProxyPath` is built without `Public`)
- Modify: `internal/modules/firewall/guard/flows.go:238-279` (`viaFlows`, kind `npm`)
- Create: `internal/controller/api/runtime_public_test.go`
- Create: `internal/modules/firewall/guard/exposure_test.go`
- Test (modify): `internal/modules/panel/listener_test.go` (the `lsSpec` helper, which `TestLsOpenApply` compares with the spec the fake listeners recorded)
- Read only: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section 6, Deploy: "80 and 443 published on all addresses"; 2.3 flow 3: "for NPM, from an internet address to each `Via.Public` port through `nat` and `FORWARD` to the NPM container"), `internal/core/guard.go:47` (`ProxyPath.Public` exists), `internal/controller/dispatch/guard.go:51-56` (dispatch already copies `Public` into the guard context)

**Interfaces:**
- Consumes `core.ListenerSpec.PublicBinding netip.AddrPort` (produced by Task 18): the host binding NPM publishes its HTTPS port on, the zero value for a tunnel. Also the existing `core.ProxyPath.Public []netip.AddrPort`, copied by dispatch into `GuardContext.Via`.
- Produces the `core.ListenerSpec` that the panel module opens for an NPM exposure, with `PublicBinding` `0.0.0.0:443`; the unknown-flow reason `the exposure has no public bindings` of the firewall guard; and a `viaFlows` that always carries a public flow for kind `npm`. Task 21 edits the same function and does not change that branch.

**Items:** 2026-11-06-v1-security-review#6

**Depends on:** Task 18
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/api/runtime_public_test.go`. It reuses `rtInfo`, `rtServe`, `rtRequest` and `rtNotRevoked` of `runtime_test.go`:

```go
package api

import (
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestV1r19RuntimeHandlerCarriesThePublicBinding(t *testing.T) {
	binding := netip.MustParseAddrPort("0.0.0.0:443")
	for _, tt := range []struct {
		name    string
		header  string
		binding netip.AddrPort
		want    []netip.AddrPort
	}{
		{"npm with its published binding", core.ClientHeaderNPM, binding, []netip.AddrPort{binding}},
		{"npm recorded without one", core.ClientHeaderNPM, netip.AddrPort{}, nil},
		{"tunnel", core.ClientHeaderTunnel, netip.AddrPort{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := rtInfo(tt.header)
			info.Spec.PublicBinding = tt.binding
			rec, _, p := rtServe(t, info, make(chan struct{}), rtNotRevoked, rtRequest(map[string][]string{
				tt.header: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"},
			}))
			if rec.Code != http.StatusNoContent || !p.found {
				t.Fatalf("answer %d %s, Proxied found %v", rec.Code, rec.Body.String(), p.found)
			}
			if !slices.Equal(p.proxied.Path.Public, tt.want) {
				t.Errorf("Path.Public = %v, want %v", p.proxied.Path.Public, tt.want)
			}
		})
	}
}
```

Create `internal/modules/firewall/guard/exposure_test.go`. The first test is the unit for the flow list, the second the guard's answer (review row 6: removing NPM's HTTPS allowance passed without an override because the public flow was never built, so a path without bindings must fail closed):

```go
package guard

import (
	"context"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// v1r19Path is an NPM exposure whose record names no public binding.
func v1r19Path() *core.ProxyPath {
	return &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}
}

func TestV1r19NPMExposureWithoutPublicBindingIsUnknown(t *testing.T) {
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}}
	got := buildFlows(in, facts{ifaces: gIfaces(), controller: 4242})
	want := []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
		{label: "exposure exp1 public", unknown: "the exposure has no public bindings"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flows\n%+v\nwant\n%+v", got, want)
	}
}

func TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}, Eval: ckEval(t, nil)}
	g, _, err := Checker{Host: &gHost{ifaces: ckIfaces()}}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := core.Guard{ID: GuardID, Message: "the change would cut exposure exp1 public: unknown (the exposure has no public bindings)"}
	if g != want {
		t.Errorf("guard = %+v, want %+v", g, want)
	}
}
```

Extend the test `internal/modules/panel/listener_test.go::TestLsOpenApply` by changing its helper `lsSpec`, which it compares with the spec the fake listeners recorded. Replace the function with:

```go
func lsSpec(via string) core.ListenerSpec {
	header := core.ClientHeaderNPM
	public := netip.MustParseAddrPort("0.0.0.0:443")
	if via == ViaTunnel {
		header = core.ClientHeaderTunnel
		public = netip.AddrPort{}
	}
	return core.ListenerSpec{
		Addr: netip.AddrPortFrom(lsGateway, DefaultPort), AllowedPeer: lsProxy, ClientHeader: header, PublicHost: lsName, ChangeID: lsAdd,
		PublicBinding: public,
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/19 && export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r19|TestLsOpenApply' ./internal/modules/panel/ ./internal/controller/api/ ./internal/modules/firewall/guard/
```

Expected: FAIL.

- `internal/modules/panel`: `TestLsOpenApply/npm` fails with `listeners … want l1 open with …`, where the recorded spec has no `PublicBinding` and the wanted one holds `0.0.0.0:443`. The `tunnel` row passes.
- `internal/controller/api`: `TestV1r19RuntimeHandlerCarriesThePublicBinding/npm_with_its_published_binding` reports `Path.Public = [], want [0.0.0.0:443]`. The other two rows pass.
- `internal/modules/firewall/guard`: `TestV1r19NPMExposureWithoutPublicBindingIsUnknown` fails on its flow list (only the proxy flow is built) and `TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding` reports `OK:true Message:every protected flow passes (1 checked)`.

- [ ] **Step 3: Implement**

1. `internal/modules/panel/listener.go`:
   - Add `const npmHTTPSPort = 443` with a doc comment that NPM publishes 443/tcp on every address of the host (spec section 6, Deploy).
   - In `listenerOpenHandler.Apply`, the `core.ListenerSpec` passed to `env.Listeners.Open` gets `PublicBinding` set to `netip.AddrPortFrom(netip.IPv4Unspecified(), npmHTTPSPort)` for `ViaNPM`, and left zero for `ViaTunnel`.
   - The binding is IPv4 only on purpose: whether Docker also publishes `[::]:443` depends on the daemon's `ip6tables` setting, which the controller cannot see, and an IPv6 public flow on a host without IPv6 routes would be unknown and refuse every change made through the exposure.
2. `internal/controller/api/runtime.go`, `RuntimeHandler`: after building `path`, when `info.Spec.PublicBinding.IsValid()` set `path.Public` to a one-element slice holding it. Otherwise leave `Public` nil. Nothing else in the handler changes, and dispatch already copies `Public` into the guard context.
3. `internal/modules/firewall/guard/flows.go`, `viaFlows`, case `"npm"`: when `len(v.Public) == 0`, add `flow{label: label + " public", unknown: "the exposure has no public bindings"}`. The proxy flow before the switch stays, and the loop over `v.Public` stays.
4. Run `gofmt -l internal` and fix what it lists in the files this task touched.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestV1r19|TestLsOpenApply' ./internal/modules/panel/ ./internal/controller/api/ ./internal/modules/firewall/guard/
timeout 1200 go test -race -count=1 ./internal/modules/panel/ ./internal/controller/api/ ./internal/modules/firewall/guard/
```

Expected: `ok` for all three packages.

- [ ] **Step 5: Commit**

```sh
git add internal/controller/api internal/modules/panel internal/modules/firewall/guard
git commit -m "fix(firewall): guard an NPM exposure's public leg"
```

- [ ] **Step 6: Run the task's gates**

```sh
export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/19 && go clean -testcache
```

Expected: every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go vet` and `go mod tidy -diff` print nothing, and `git status --short` prints nothing. Spec 9 re-reviews this row, so the controller records the harness run of Gate 7 for the branch.

### Task 20: Partition the internet address space by the ruleset's prefixes

**Files:**
- Create: `internal/modules/firewall/eval/cells.go`
- Create: `internal/modules/firewall/eval/cells_test.go`
- Read only: `internal/platform/netfilter/parse.go:59-93` (`Rule`, `AddrMatch`, `Match`, `Option`), `internal/modules/firewall/eval/eval.go:279` (`addrMatch` stays unchanged: it matches one concrete address, and every public address of a cell behaves the same under every prefix match, so evaluating the cell's `Rep` decides the whole cell), `internal/modules/firewall/eval/match.go:189-201` (the `ctorigdst` option, a prefix or one address, is the other place a rule matches on an address range)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: Contracts "Internet cells", exactly: in package `eval`, `type Cell struct { From, To netip.Addr; Rep netip.Addr }` and `func InternetCells(rs *netfilter.Ruleset, v6 bool) []Cell`, with the doc comments that Contracts gives. Task 21 consumes both and nothing else.

**Items:** 2026-11-06-v1-security-review#11

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 0 - spec 2 - coupling 2 - risk 2 = 6

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/firewall/eval/cells_test.go`. The table pins exact cells for each way a rule names an address range (`-s`, `-d`, a negated match, `ctorigdst` as an address and as a prefix), for a prefix inside a private range, for prefixes at the ends of the space, for the other family and for IPv6. The second test checks the partition's properties on a larger ruleset:

```go
package eval

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func v1r20Src(prefix string, neg bool) netfilter.Rule {
	return netfilter.Rule{Source: netfilter.AddrMatch{Prefix: netip.MustParsePrefix(prefix), Neg: neg}}
}

func v1r20Dst(prefix string) netfilter.Rule {
	return netfilter.Rule{Dest: netfilter.AddrMatch{Prefix: netip.MustParsePrefix(prefix)}}
}

// v1r20Orig is a rule with a conntrack --ctorigdst match of value, a
// prefix or one address.
func v1r20Orig(value string) netfilter.Rule {
	return netfilter.Rule{Matches: []netfilter.Match{{Module: "conntrack",
		Options: []netfilter.Option{{Name: "ctorigdst", Values: []string{value}}}}}}
}

// v1r20Set puts the first half of rules in filter INPUT and the rest in nat
// PREROUTING, so a ruleset's rules are found in every table.
func v1r20Set(rules ...netfilter.Rule) *netfilter.Ruleset {
	k := (len(rules) + 1) / 2
	return &netfilter.Ruleset{Tables: []netfilter.Table{
		{Name: "filter", Chains: []netfilter.Chain{{Name: "INPUT", Builtin: true, Policy: "ACCEPT", Rules: rules[:k]}}},
		{Name: "nat", Chains: []netfilter.Chain{{Name: "PREROUTING", Builtin: true, Policy: "ACCEPT", Rules: rules[k:]}}},
	}}
}

// v1r20Cells builds the wanted cells from "<from>-<to>" texts; Rep is From.
func v1r20Cells(spans ...string) []Cell {
	out := make([]Cell, len(spans))
	for i, s := range spans {
		from, to, _ := strings.Cut(s, "-")
		out[i] = Cell{From: netip.MustParseAddr(from), To: netip.MustParseAddr(to), Rep: netip.MustParseAddr(from)}
	}
	return out
}

const (
	v1r20Whole4 = "0.0.0.1-255.255.255.254"
	v1r20Whole6 = "2000::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"
)

func TestV1r20InternetCells(t *testing.T) {
	around8 := []string{"0.0.0.1-8.8.7.255", "8.8.8.0-8.8.8.255", "8.8.9.0-255.255.255.254"}
	tests := []struct {
		name string
		rs   *netfilter.Ruleset
		v6   bool
		want []string
	}{
		{"no ruleset", nil, false, []string{v1r20Whole4}},
		{"no rules, IPv6", &netfilter.Ruleset{}, true, []string{v1r20Whole6}},
		{"a destination prefix", v1r20Set(v1r20Dst("8.8.8.0/24")), false, around8},
		{"a source prefix with host bits set", v1r20Set(v1r20Src("8.8.8.9/24", false)), false, around8},
		{"a negated address", v1r20Set(v1r20Src("1.1.1.1/32", true)), false,
			[]string{"0.0.0.1-1.1.1.0", "1.1.1.1-1.1.1.1", "1.1.1.2-255.255.255.254"}},
		{"a conntrack original destination address", v1r20Set(v1r20Orig("9.9.9.9")), false,
			[]string{"0.0.0.1-9.9.9.8", "9.9.9.9-9.9.9.9", "9.9.9.10-255.255.255.254"}},
		{"a conntrack original destination prefix", v1r20Set(v1r20Orig("9.9.9.0/24")), false,
			[]string{"0.0.0.1-9.9.8.255", "9.9.9.0-9.9.9.255", "9.9.10.0-255.255.255.254"}},
		{"two prefixes in two tables", v1r20Set(v1r20Dst("198.41.192.0/24"), v1r20Dst("104.16.0.0/13")), false,
			[]string{"0.0.0.1-104.15.255.255", "104.16.0.0-104.23.255.255", "104.24.0.0-198.41.191.255",
				"198.41.192.0-198.41.192.255", "198.41.193.0-255.255.255.254"}},
		{"a prefix inside a private range", v1r20Set(v1r20Dst("10.1.0.0/16")), false,
			[]string{"0.0.0.1-9.255.255.255", "11.0.0.0-255.255.255.254"}},
		{"a prefix of the whole space", v1r20Set(v1r20Dst("0.0.0.0/0")), false, []string{v1r20Whole4}},
		{"a prefix at the top of the space", v1r20Set(v1r20Dst("255.255.255.254/31")), false,
			[]string{"0.0.0.1-255.255.255.253", "255.255.255.254-255.255.255.254"}},
		{"an IPv6 prefix in an IPv4 call", v1r20Set(v1r20Dst("2001:db8:1::/48")), false, []string{v1r20Whole4}},
		{"an IPv4 prefix in an IPv6 call", v1r20Set(v1r20Dst("8.8.8.0/24")), true, []string{v1r20Whole6}},
		{"an IPv6 prefix", v1r20Set(v1r20Dst("2606:4700::/32")), true,
			[]string{"2000::-2606:46ff:ffff:ffff:ffff:ffff:ffff:ffff", "2606:4700::-2606:4700:ffff:ffff:ffff:ffff:ffff:ffff",
				"2606:4701::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
		{"an IPv6 prefix inside the documentation range", v1r20Set(v1r20Dst("2001:db8:1::/48")), true,
			[]string{"2000::-2001:db7:ffff:ffff:ffff:ffff:ffff:ffff", "2001:db9::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
		{"an IPv6 prefix of the whole space", v1r20Set(v1r20Dst("::/0")), true, []string{v1r20Whole6}},
		{"an IPv6 prefix at the top of the space", v1r20Set(v1r20Dst("3fff::/16")), true,
			[]string{"2000::-3ffe:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "3fff::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InternetCells(tt.rs, tt.v6)
			if want := v1r20Cells(tt.want...); !slices.Equal(got, want) {
				t.Errorf("InternetCells =\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// v1r20Public is the Contracts definition of a public unicast address.
func v1r20Public(a netip.Addr) bool {
	for _, p := range []string{"100.64.0.0/10", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return a.IsGlobalUnicast() && !a.IsPrivate()
}

// v1r20Last is the last address of p.
func v1r20Last(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func TestV1r20CellsPartitionThePublicSpace(t *testing.T) {
	prefixes := map[bool][]string{
		false: {"8.8.8.0/24", "1.1.1.1/32", "104.16.0.0/13", "10.1.0.0/16", "198.41.192.0/24", "172.16.0.0/12", "224.0.0.0/4", "0.0.0.0/0"},
		true:  {"2606:4700::/32", "2001:db8:1::/48", "2a00::/12", "::/0"},
	}
	samples := map[bool][]string{
		false: {"0.0.0.1", "1.1.1.1", "8.8.8.8", "9.255.255.255", "11.0.0.0", "100.128.0.0", "126.255.255.255", "128.0.0.1",
			"169.253.255.255", "169.255.0.0", "172.15.255.255", "172.32.0.0", "192.0.1.255", "192.0.3.0", "198.51.99.255",
			"198.51.101.0", "203.0.112.255", "203.0.114.0", "223.255.255.255", "240.0.0.1", "255.255.255.254"},
		true: {"2000::", "2001:db7::1", "2001:db9::1", "2606:4700::1", "2a00::1", "3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	}
	for _, v6 := range []bool{false, true} {
		var rules []netfilter.Rule
		var pfx []netip.Prefix
		for _, p := range prefixes[v6] {
			rules = append(rules, v1r20Dst(p))
			pfx = append(pfx, netip.MustParsePrefix(p))
		}
		cells := InternetCells(v1r20Set(rules...), v6)
		if len(cells) == 0 {
			t.Fatalf("v6=%v: no cells", v6)
		}
		for i, c := range cells {
			if c.Rep != c.From || !v1r20Public(c.From) || !v1r20Public(c.To) || c.From.Compare(c.To) > 0 {
				t.Errorf("v6=%v: cell %v is not a range of public addresses with Rep at its lowest", v6, c)
			}
			if i > 0 && cells[i-1].To.Compare(c.From) >= 0 {
				t.Errorf("v6=%v: cells %v and %v are out of order or overlap", v6, cells[i-1], c)
			}
			for _, p := range pfx {
				p = p.Masked()
				if p.Contains(c.From) != p.Contains(c.To) ||
					(c.From.Compare(p.Addr()) < 0 && p.Addr().Compare(c.To) <= 0) ||
					(c.From.Compare(v1r20Last(p)) <= 0 && v1r20Last(p).Compare(c.To) < 0) {
					t.Errorf("v6=%v: cell %v is cut by the prefix %s", v6, c, p)
				}
			}
		}
		for _, s := range samples[v6] {
			a := netip.MustParseAddr(s)
			if !v1r20Public(a) {
				t.Fatalf("sample %s is not a public address", s)
			}
			n := 0
			for _, c := range cells {
				if c.From.Compare(a) <= 0 && a.Compare(c.To) <= 0 {
					n++
				}
			}
			if n != 1 {
				t.Errorf("v6=%v: public address %s is in %d cells, want 1", v6, s, n)
			}
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/19 && export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r20' ./internal/modules/firewall/eval/
```

Expected: FAIL to build, `undefined: Cell` (the first undefined identifier in `cells_test.go`).

- [ ] **Step 3: Implement**

Create `internal/modules/firewall/eval/cells.go`. Declare `Cell` and `InternetCells` with the signatures and doc comments of Contracts "Internet cells", exactly. Put every helper in this file, unexported, and change nothing in `eval.go`. `InternetCells` works as follows.

1. The space of a family. IPv4 runs from `0.0.0.0` to `255.255.255.255`. IPv6 runs from `2000::` to `3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff`, the last address of `2000::/3`. A last address of a prefix comes from setting its host bits to 1 (take the masked address as bytes, set the bits from `Bits()` on, rebuild with `netip.AddrFromSlice`).
2. The holes, the addresses of the space that are not public unicast. This list is exactly the Contracts exclusion (not `IsGlobalUnicast()`, `IsPrivate()`, CGNAT, documentation) and nothing more. IPv4: `0.0.0.0/32`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.2.0/24`, `192.168.0.0/16`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `255.255.255.255/32`. IPv6: `2001:db8::/32`. `240.0.0.0/4` is not a hole, because `IsGlobalUnicast()` is true for it.
3. The prefixes. Walk every table, every chain and every rule of `rs`, opaque rules included (`rs` may be nil, which gives none). Take `Rule.Source.Prefix` and `Rule.Dest.Prefix` when valid, and the first value of every option named `ctorigdst` of every match of the rule, which is a prefix or a bare address (a bare address is a host prefix). Keep a prefix only when its address family is the requested one; an IPv4-mapped IPv6 prefix (`Addr().Is4In6()`) belongs to neither family. Mask each kept prefix. Ignore negation: a negated prefix cuts the space at the same places.
4. The cut points. Start with the first address of the space. For each prefix add its first address and the address after its last address. Keep a cut point only when it is after the first address of the space and not after its last address (the address after `255.255.255.255` is the zero `netip.Addr` and is dropped). Sort the cut points and drop duplicates.
5. The cells. Cut point `i` starts an interval that ends at the address before cut point `i+1`, and the last interval ends at the last address of the space. For each interval, `From` is its lowest public address and `To` its highest: move the start forward past any hole that contains it, repeatedly, and the end backward before any hole that contains it, repeatedly. An interval with no public address gives no cell. `Rep` is `From`. A cell can contain holes in the middle (for example one cell spans `0.0.0.1` to `255.255.255.254` when no rule names a prefix), which is fine: no prefix tells the public addresses of a cell apart.
6. The result is sorted by `From`, its cells never overlap, and it holds at least one cell, because the cut points are only ever added to a space whose public part is not empty.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r20' ./internal/modules/firewall/eval/
timeout 600 go test -race -count=1 ./internal/modules/firewall/eval/
```

Expected: `ok` for both.

- [ ] **Step 5: Commit**

```sh
git add internal/modules/firewall/eval/cells.go internal/modules/firewall/eval/cells_test.go
git commit -m "feat(firewall): partition the internet by prefixes"
```

- [ ] **Step 6: Run the task's gates**

```sh
export TMPDIR=/root/.cache/w10b-tmp/19 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/19 && go clean -testcache
```

Expected: every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go vet` and `go mod tidy -diff` print nothing, and `git status --short` prints nothing.

### Task 21: Check internet flows against every partition cell

**Files:**
- Modify: `internal/modules/firewall/guard/flows.go:27-32, 49-75, 238-286, 335-362, 398-425` (delete `internet4`, `internet6` and `internetFor`; `flow`, `flowKey`, `flowSet.add`; `viaFlows`, `controllerListenFlows`, `tailscaleFlows`)
- Create: `internal/modules/firewall/guard/cells.go` (`cellFlows`, `checkFlow`)
- Modify: `internal/modules/firewall/guard/guard.go` (`Check`: the loop over flows)
- Modify: `internal/modules/firewall/guard/route.go:53-126` (the `describe` calls of `probes`; the `standIn` handling in `lookup`, line 57, stays as it is; a new `describeFlow`)
- Create: `internal/modules/firewall/guard/internet_cells_test.go`
- Test (modify): `internal/modules/firewall/guard/flows_test.go`, `internal/modules/firewall/guard/guard_test.go`
- Read only: `docs/dforge/plans/2026-10-24-v1-w1-firewall-core.md` (rulings that chose the stand-ins `198.51.100.1` and `2001:db8:ffff::1`, which the owner replaced), `internal/modules/firewall/guard/route_test.go` (its flows hold concrete addresses and `standIn: true`, so it needs no edit)

**Interfaces:**
- Consumes: Contracts "Internet cells" (`eval.Cell`, `eval.InternetCells`, Task 20), and the unknown flow Task 19 adds to `viaFlows` (this task edits the same function and does not change that branch).
- Produces, in package `guard`, all unexported and used by this task's tests only:
  - `type wildEnd int` with `wildNone` (zero), `wildSrc`, `wildDst`; `flow.wild wildEnd` names the end of `flow.pkt` that stands for every public address of its family. Until a cell is chosen that end holds the family's unspecified address, which fixes `pkt.Family`.
  - `flow.span string`: set only on the copy of a flow checked in one cell, `"<From>-<To>"` of the cell.
  - `anyAddr(a netip.Addr) netip.Addr`, `(*router).cellFlows(fl flow) []flow`, `(*router).checkFlow(fl flow, ev …) []FlowResult` and `describeFlow(fl flow, p fwstate.Packet) string`.
  - `internet4`, `internet6` and `internetFor` no longer exist. `flow.standIn` stays and keeps its meaning for a cell: with no route of its family there is nothing to protect.
- The guard's refusal names a blocked cell as `<From>-<To>` in place of the address of the wild end, formatted as below.

**Items:** 2026-11-06-v1-security-review#11

**Depends on:** Task 19, Task 20
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/firewall/guard/internet_cells_test.go`. The tests use the real evaluator on parsed save output, so a routed deny of the connector's real edge range, the scenario of review row 11, is decided by the ruleset and not by a fake. They reuse `ckEnv`, `ckRunning`, `ckIfaces`, `gHost`, `rtQuery`, `rtVia` and `gxQueryMark` of the package's tests:

```go
package guard

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// v1r21Table is save output with ACCEPT policies and the given rules.
func v1r21Table(rules ...string) string {
	return "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
}

// v1r21State is a checked state of the parsed IPv4 and IPv6 save output.
func v1r21State(t *testing.T, v4, v6 string) fwstate.Loaded {
	t.Helper()
	parse := func(text string) *netfilter.Ruleset {
		rs, err := netfilter.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	return fwstate.Loaded{Variant: "nft", V4: parse(v4), V6: parse(v6)}
}

// v1r21Check runs the guard with the real evaluator.
func v1r21Check(t *testing.T, h *gHost, st fwstate.Loaded, plan *core.GuardContext) core.Guard {
	t.Helper()
	env, _, _ := ckEnv()
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func v1r21Tunnel() *core.GuardContext {
	return &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel",
		ProxyAddr: netip.MustParseAddr("172.30.0.3"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}
}

func TestV1r21TunnelFlowNamesTheFirstBlockedCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 198.41.192.0/24 -j DROP", "-A FORWARD -d 104.16.0.0/13 -j DROP"), v1r21Table())
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"0.0.0.1", "104.16.0.0", "104.24.0.0", "198.41.192.0", "198.41.193.0"} {
		h.routes[rtQuery(rep, "172.30.0.3", "dp-panel")] = rtVia("eth0", "")
	}
	g := v1r21Check(t, h, st, v1r21Tunnel())
	if g.OK {
		t.Fatalf("guard = %+v, want a refusal: the connector's edge ranges are dropped while the documentation address is allowed", g)
	}
	const first, later = "104.16.0.0-104.23.255.255", "198.41.192.0-198.41.192.255"
	for _, want := range []string{
		"exposure exp2 tunnel udp from 172.30.0.3 to " + first + ":7844 on dp-panel out eth0: drop",
		"exposure exp2 tunnel from 172.30.0.3 to " + first + ":7844 on dp-panel out eth0: drop",
		"exposure exp2 tunnel from 172.30.0.3 to " + first + ":443 on dp-panel out eth0: drop",
	} {
		if !strings.Contains(g.Message, want) {
			t.Errorf("message %q does not contain %q", g.Message, want)
		}
	}
	if strings.Contains(g.Message, later) {
		t.Errorf("message %q names %s, which is not the first blocked cell", g.Message, later)
	}
}

func TestV1r21TunnelFlowIsEvaluatedInEveryCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 198.41.192.0/24 -j ACCEPT"), v1r21Table())
	reps := []string{"0.0.0.1", "198.41.192.0", "198.41.193.0"}
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range reps {
		h.routes[rtQuery(rep, "172.30.0.3", "dp-panel")] = rtVia("eth0", "")
	}
	if g := v1r21Check(t, h, st, v1r21Tunnel()); !g.OK {
		t.Fatalf("guard = %+v, want OK when every cell passes", g)
	}
	for _, rep := range reps {
		if q := rtQuery(rep, "172.30.0.3", "dp-panel"); !slices.Contains(h.lookups, q) {
			t.Errorf("no route was looked up for the cell starting at %s: lookups %v", rep, h.lookups)
		}
	}
}

func TestV1r21PublicFlowChecksEverySourceCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A INPUT -s 8.8.8.0/24 -p tcp -j DROP"), v1r21Table())
	plan := &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm",
		ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"),
		Public: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:443")}}}
	g := v1r21Check(t, &gHost{ifaces: ckIfaces()}, st, plan)
	want := "exposure exp1 public from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}

func TestV1r21TailscaleFlowChecksEveryIPv6Cell(t *testing.T) {
	st := v1r21State(t, v1r21Table(), v1r21Table("-A OUTPUT -d 2606:4700::/32 -j DROP"))
	h := &gHost{ifaces: gIfaces(), sockets: []netstat.Socket{
		flSock(netstat.ProtoUDP, netstat.StateClose, "[::]:41641", "", "tailscaled", 300),
	}, routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"2000::", "2606:4700::", "2606:4701::"} {
		h.routes[gxQueryMark(rtQuery(rep, "", ""), 0x80000)] = rtVia("eth0", "2001:db8::1")
	}
	g := v1r21Check(t, h, st, nil)
	want := "tailscale udp from 2001:db8::1 to [2606:4700::-2606:4700:ffff:ffff:ffff:ffff:ffff:ffff]:41641 out eth0: drop"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```sh
mkdir -p /root/.cache/w10b-tmp/20 && export TMPDIR=/root/.cache/w10b-tmp/20 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r21' ./internal/modules/firewall/guard/
```

Expected: FAIL by assertion on the current tree (the package builds, because `eval.InternetCells` exists after Task 20 and these tests do not call it):

- `TestV1r21TunnelFlowNamesTheFirstBlockedCell`: `want a refusal`, since the stand-in `198.51.100.1` has no route and the flow vanishes.
- `TestV1r21TunnelFlowIsEvaluatedInEveryCell`: `no route was looked up for the cell starting at 0.0.0.1`.
- `TestV1r21PublicFlowChecksEverySourceCell` and `TestV1r21TailscaleFlowChecksEveryIPv6Cell`: `want a refusal containing …`, because the stand-in address is not in the dropped prefix.

- [ ] **Step 3: Implement**

1. `flows.go`, types: delete the `internet4` and `internet6` variables and their comment, and `internetFor`. Add `type wildEnd int` with the constants `wildNone` (the zero value), `wildSrc` and `wildDst`, and give `flow` the fields `wild wildEnd` and `span string` (see Interfaces). Update the comment of `standIn`: a wild destination whose cell has no route of its family has nothing to protect. Add `anyAddr(a netip.Addr) netip.Addr`: `netip.IPv4Unspecified()` when `a.Unmap().Is4()`, else `netip.IPv6Unspecified()`. `flowKey` gains `wild wildEnd`, and `flowSet.add` fills it from the flow, so two flows differing only in the wild end stay distinct.
2. `flows.go`, flow builders. Each builds its packet with `newPacket` and the family's unspecified address in the wild end, and sets `wild`:
   - `viaFlows`, kind `npm`, each public binding: the source is `anyAddr(pub.Addr())`, `wild: wildSrc`. The destination logic (unspecified when the address is not this host's) is unchanged.
   - `viaFlows`, kind `tunnel`: the destination is `anyAddr(v.ProxyAddr)`, `wild: wildDst, standIn: true`.
   - `controllerListenFlows`: the loop runs over `netip.IPv4Unspecified()` and `netip.IPv6Unspecified()` as the source, `wild: wildSrc`; `listenFor` is called as before.
   - `tailscaleFlows`: `dst := anyAddr(src)`, and every flow it adds (UDP and TCP 443) gets `wild: wildDst, standIn: true`.
3. `cells.go` (new). `(*router).cellFlows(fl flow) []flow` returns `[]flow{fl}` for a flow that has `wild == wildNone` or a non-empty `unknown`. Otherwise it takes the checked state's ruleset of the flow's family (`r.st.V4`, or `r.st.V6` when `fl.pkt.Family == fwstate.V6`; it may be nil) and calls `eval.InternetCells(rs, fl.pkt.Family == fwstate.V6)`. It returns one copy of the flow per cell, in order, with the wild end of `pkt` set to the cell's `Rep` and `span` set to `From + "-" + To` (`netip.Addr.String()` each).
   `(*router).checkFlow(fl flow, ev func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result) []FlowResult` loops over `cellFlows(fl)`. For each cell flow it gets `r.probes(cf)` and evaluates each probe with the existing `evaluate(r, p, ev)`, giving one `FlowResult{Flow: p.desc, Result: …}` per probe. If any result of a cell is not `fwstate.Accept`, it returns that cell's results at once (the first blocked cell; later cells are not evaluated). If no cell is blocked, it returns the results of the first cell that produced any, and nil when none did (every cell had no route to protect). For an ordinary flow this is the old behaviour: its results are returned whether or not they pass.
4. `guard.go`, `Check`: replace the inner loop over `r.probes(fl)` with `results = append(results, r.checkFlow(fl, in.Eval)...)`. Router-advertisement flows pass through the same call and are unaffected, as they have no wild end.
5. `route.go`: add `describeFlow(fl flow, p fwstate.Packet) string`. With an empty `fl.span` it returns `describe(fl.label, p)` unchanged. Otherwise it builds the same text as `describe` (`<label>[ <proto> unless tcp] from <src> to <dst>[ on <in>][ out <out>]`), but the wild end shows `fl.span` in place of its address: for `wildSrc` the source is `<span>`; for `wildDst` the destination is `<span>:<port>` for IPv4 and `[<span>]:<port>` for IPv6. In `probes`, replace every call `describe(fl.label, p)` and `describe(fl.label, q)` with `describeFlow(fl, p)` and `describeFlow(fl, q)`. Keep `describe` itself, which `route_test.go` calls. Leave the `fl.standIn` condition in `lookup` (line 57) as it is.
6. Update the existing tests whose expectations held the stand-in addresses. `route_test.go` needs no edit.

   In `flows_test.go`, `TestBuildFlows`: in each expected flow below, the stand-in address of the wild end becomes the family's unspecified address and `wild` is set. These are the new rows, in the order of the file:

   ```go
   // "a client that came through an exposure is not a direct panel flow"
   {label: "exposure exp1 public", pkt: flPkt("input", "0.0.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc},
   {label: "exposure exp1 public", pkt: flPkt("input", "::", 0, "2001:db8::1", 443, "tcp"), wild: wildSrc},
   {label: "exposure exp1 public", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), wild: wildSrc},
   {label: "exposure exp1 public", pkt: flPkt("input", "::", 0, "::", 443, "tcp"), wild: wildSrc},
   // "tunnel exposure"
   {label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "0.0.0.0", 7844, "udp")), wild: wildDst, standIn: true},
   {label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "0.0.0.0", 7844, "tcp")), wild: wildDst, standIn: true},
   {label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "0.0.0.0", 443, "tcp")), wild: wildDst, standIn: true},
   // "local controller sessions from live facts"
   {label: "controller listener", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), wild: wildSrc},
   {label: "controller listener", pkt: flPkt("input", "::", 0, "::", 443, "tcp"), wild: wildSrc},
   // "the local controller's listening sockets"
   {label: "controller listener", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 7443, "tcp"), wild: wildSrc},
   {label: "controller listener", pkt: flPkt("input", "0.0.0.0", 0, "192.0.2.1", 9443, "tcp"), wild: wildSrc},
   {label: "controller listener", pkt: flPkt("input", "::", 0, "2001:db8::1", 9444, "tcp"), wild: wildSrc},
   // "tailscaled"
   {label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 41641, "0.0.0.0", 41641, "udp"), 0x80000), wild: wildDst, standIn: true},
   {label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), 0x80000), wild: wildDst, standIn: true},
   {label: "tailscale", pkt: gxMark(flPkt("output", "::", 41641, "::", 41641, "udp"), 0x80000), wild: wildDst, standIn: true},
   {label: "tailscale", pkt: gxMark(flPkt("output", "::", 0, "::", 443, "tcp"), 0x80000), wild: wildDst, standIn: true},
   // "tailscaled without a UDP socket"
   {label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), 0x80000), wild: wildDst, standIn: true},
   {label: "tailscale", pkt: gxMark(flPkt("output", "::", 0, "::", 443, "tcp"), 0x80000), wild: wildDst, standIn: true},
   ```

   In `guard_test.go`, the checked states `ckV4` and `ckV6` are empty rulesets, so each wild flow has one cell: IPv4 `0.0.0.1-255.255.255.254` with `Rep` `0.0.0.1`. Replace the stand-in `198.51.100.1` as follows (and run `gofmt -w` on the file, since the map literal's alignment changes):

   - `ckHost` routes: the two keys `rtQuery("198.51.100.1", …)` become `gxQueryMark(rtQuery("0.0.0.1", "", ""), 0x80000)` and `rtQuery("0.0.0.1", "172.30.0.3", "dp-panel")`.
   - `ckAllFlows`: the five lines become

     ```go
     "exposure exp2 tunnel udp from 172.30.0.3 to 0.0.0.1-255.255.255.254:7844 on dp-panel out eth0",
     "exposure exp2 tunnel from 172.30.0.3 to 0.0.0.1-255.255.255.254:7844 on dp-panel out eth0",
     "exposure exp2 tunnel from 172.30.0.3 to 0.0.0.1-255.255.255.254:443 on dp-panel out eth0",
     ```

     and, further down the list,

     ```go
     "tailscale udp from 192.0.2.1 to 0.0.0.1-255.255.255.254:41641 out eth0",
     "tailscale from 192.0.2.1 to 0.0.0.1-255.255.255.254:443 out eth0",
     ```

   - `TestCheckRouterGiven`: the route keys become `rtQuery("172.17.0.5", "0.0.0.1", "eth0")` and `rtQuery("172.17.0.5", "0.0.0.1", "dp-panel")`, and `want` becomes `"exposure exp1 public from 0.0.0.1-255.255.255.254 to 192.0.2.1:80 on eth1: unknown (firewall: route to 172.17.0.5 from 0.0.0.1 on eth1: netstat: no route)"`.
   - `TestGxCheckTailscaleMark`: the host's two route keys become `rtQuery("0.0.0.1", "", "")` and `gxQueryMark(rtQuery("0.0.0.1", "", ""), 0x80000)`, and the expected flows become the two `tailscale` lines above.
   - `TestGxCheckControllerListeners`: the host's route key becomes `rtQuery("0.0.0.1", "", "")`, and the expected message becomes

     ```go
     "the change would cut controller listener from 0.0.0.1-255.255.255.254 to 192.0.2.1:7443 on eth0: drop (filter/ufw-user-input#1); " +
     	"controller listener from 0.0.0.1-255.255.255.254 to 192.0.2.1:7443 on eth1: drop (filter/ufw-user-input#1)"
     ```

7. Run `gofmt -l internal/modules/firewall`; it prints nothing.

- [ ] **Step 4: Run the tests to verify they pass**

```sh
export TMPDIR=/root/.cache/w10b-tmp/20 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestV1r21' ./internal/modules/firewall/guard/
timeout 900 go test -race -count=1 ./internal/modules/firewall/...
```

Expected: `ok` for every package under `internal/modules/firewall`.

- [ ] **Step 5: Commit**

```sh
git add internal/modules/firewall/guard
git commit -m "fix(firewall): check internet flows in every cell"
```

- [ ] **Step 6: Run the task's gates**

```sh
export TMPDIR=/root/.cache/w10b-tmp/20 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
git status --short
rm -rf /root/.cache/w10b-tmp/20 && go clean -testcache
```

Expected: every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go vet` and `go mod tidy -diff` print nothing, and `git status --short` prints nothing. The harness covers the same guard on real hosts (Gate 7), so a ruleset with many prefixes must still finish the check within the action's timeout there.

### Task 22: Set backup file modes through the open descriptor

**Files:**
- Modify: `internal/controller/backup/stream.go` (`streamServe`: the `os.Chmod(path, 0o600)` call after `VACUUM INTO`, and the `os.Open`/`f.Stat` that follow the key read)
- Modify: `internal/controller/backup/contribute.go` (`Backup` struct: one unexported test seam field, `afterCopy`)
- Create: `internal/controller/backup/stream_descriptor_test.go`

**Interfaces:**
- Consumes: from the package's tests, `bsNewEnv`, `bsBody`, `bsRequest`, `(*bsEnv).post`, `(*bsEnv).copies`, `TestParams` (`stream_test.go`, `format_test.go`), and `w10bcSpy`, `w10bcDeps` (`stream_copy_test.go`). None changes.
- Produces: nothing another task consumes. `Backup` gains `afterCopy func(path string)`, nil in production. `streamServe` calls it, when set, once the copy of the database exists and before the copy is opened.

**Items:** 2026-10-21-v1-completion#435

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Hazard: `streamServe` makes the temporary copy with `VACUUM INTO`, then sets its mode with `os.Chmod(path, 0o600)` by path, and later opens the same path again. A chmod by path follows a symbolic link, so a link planted at that path between the copy and the chmod makes Darkpanel change the mode of another file, and the later `os.Open` then streams that file as the database. The copy lives in a directory of mode 0700 that only the controller's user and root can enter, so this is hardening, not a remote hole. `restore.go` already sets modes through one descriptor that refuses links (`restoreQuarantine` opens with `O_NOFOLLOW`; `Ops.Secure` works on a descriptor). The backup now does the same: it opens the copy once without following links, sets the mode on that descriptor, and streams from that descriptor.

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/backup/stream_descriptor_test.go`:

```go
package backup

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestV1r22BackupRefusesACopySwappedForALink replaces the temporary copy with a
// link to a read-only file just after it is made. The backup must refuse and
// leave the file the link names as it was; a chmod by path would make it 0600.
func TestV1r22BackupRefusesACopySwappedForALink(t *testing.T) {
	e := bsNewEnv(t)
	decoy := filepath.Join(t.TempDir(), "decoy.db")
	if err := os.WriteFile(decoy, []byte("placeholder, not the database"), 0o400); err != nil {
		t.Fatal(err)
	}
	e.b.afterCopy = func(path string) {
		if err := os.Remove(path); err != nil {
			t.Errorf("remove the copy: %v", err)
			return
		}
		if err := os.Symlink(decoy, path); err != nil {
			t.Errorf("replace the copy with a link: %v", err)
		}
	}

	rec := e.post(bsBody(t, "placeholder-secret", &TestParams))

	st, err := os.Stat(decoy)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o400 {
		t.Errorf("the file the link names has mode %v, want 0400: the mode was set by path, through the link", st.Mode().Perm())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: a copy that is a link must be refused, not streamed (body %.120s)", rec.Code, rec.Body.String())
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("after the refusal, %v is left beside the database", left)
	}
}

// TestV1r22BackupCopyIsPrivateWhenTheStreamStarts guards the other half of the
// change: the descriptor the backup streams from belongs to a regular file of
// mode 0600, whatever the process umask made it.
func TestV1r22BackupCopyIsPrivateWhenTheStreamStarts(t *testing.T) {
	e := bsNewEnv(t)
	var (
		seen    os.FileMode
		lookErr error
	)
	spy := &w10bcSpy{look: func() {
		names, err := filepath.Glob(filepath.Join(e.dir, "backup-*", "backup.db"))
		if err != nil || len(names) != 1 {
			lookErr = fmt.Errorf("found %v, error %v", names, err)
			return
		}
		st, err := os.Lstat(names[0])
		if err != nil {
			lookErr = err
			return
		}
		seen = st.Mode()
	}}

	rec := httptest.NewRecorder()
	e.b.streamServe(w10bcDeps(e), spy).ServeHTTP(rec, bsRequest(bsBody(t, "placeholder-secret", &TestParams)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if lookErr != nil {
		t.Fatal(lookErr)
	}
	if !seen.IsRegular() || seen.Perm() != 0o600 {
		t.Errorf("when the stream started, the copy had mode %v, want a regular file of 0600", seen)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/21 && export TMPDIR=/root/.cache/w10b-tmp/21 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r22' ./internal/controller/backup/`
Expected: build failure `e.b.afterCopy undefined (type *Backup has no field or method afterCopy)`. Then add only the `afterCopy` field to `Backup` and the one call of it in `streamServe` (Step 3, items 1 and 2), change nothing else, and rerun the same command. Expected: `TestV1r22BackupRefusesACopySwappedForALink` FAILS on `the file the link names has mode -rw-------, want 0400` and on `status = 200, want 500`, and `TestV1r22BackupCopyIsPrivateWhenTheStreamStarts` PASSES (it guards the new code against a dropped chmod). Quote both outputs in the report.

- [ ] **Step 3: Implement**

1. `contribute.go`: add to `Backup` the field `afterCopy func(path string)` with a doc comment that gives the why: a test seam that runs once the temporary copy of the database exists and before it is opened, so a test can swap the file; always nil in production.
2. `stream.go`, `streamServe`: right after the `VACUUM INTO` succeeds, call `b.afterCopy(path)` when it is not nil. Read the field inside the handler function each time a request runs, not when `streamServe` builds the handler, because the test sets it after `bsNewEnv` has built `e.h`.
3. `stream.go`, `streamServe`: remove the `os.Chmod(path, 0o600)` call. Where the code today calls `streamReadKey` and then `os.Open(path)` and `f.Stat()`, open the copy first, in this order:
   - `os.OpenFile(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)`, with the same `//nolint:gosec` G304 reason the current `os.Open` line carries; a failure keeps the error text `backup: open the temporary copy %s: %w`. `O_NOFOLLOW` refuses a link, and `O_NONBLOCK` keeps a FIFO from blocking until the check below refuses it, as `streamReadKey` already does.
   - the deferred `f.Close()` right after the open succeeds;
   - `f.Stat()`; refuse with `backup: the temporary copy %s is not a regular file` when `!st.Mode().IsRegular()`;
   - `f.Chmod(0o600)` (a descriptor-based `fchmod`; it needs no write access), failing with `backup: chmod %s: %w`;
   - only then `streamReadKey(b.cfg.KeyFile)` and its `defer clear(key)`.
   The member size comes from the same `st`. Every failure above answers through `writeProblem`, as the neighbouring failures do, so a refused copy is a 500 problem and the deferred `os.RemoveAll(dir)` still removes the directory.
4. Keep the comment above `VACUUM INTO` that says the private directory protects the copy before its mode is set; it stays true. Add no other comment.
5. Nothing else in the package calls `os.Chmod` by path: `rg -n 'os\.Chmod' internal/controller/backup --glob '!*_test.go'` prints nothing afterwards.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w10b-tmp/21 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r22|TestW10Bc' ./internal/controller/backup/ && ! rg -n 'os\.Chmod' internal/controller/backup --glob '!*_test.go'`
Expected: `ok` and no match from `rg`.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/controller/backup/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/21 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/stream.go internal/controller/backup/contribute.go internal/controller/backup/stream_descriptor_test.go
git commit -m "fix(backup): set the copy's mode on its descriptor"
```

### Task 23: Clear the later-step definite mark of nm.run

**Files:**
- Modify: `internal/modules/npm/effects.go` (`nm.run` and its doc comment; one new unexported helper, `withoutDefiniteMark`)
- Test: `internal/modules/npm/effects_test.go` (new test and helpers)
- Test: `internal/modules/npm/instance_deploy_test.go` (`TestDpApplyFailsAtEachStep`: the expected definite flag)
- Test: `internal/modules/npm/instance_remove_test.go` (`TestRmApply`, subtest `the daemon refuses a volume removal`)

**Interfaces:**
- Consumes: `nm.run`, `npmEffect`, `named`, `refused`, `notDefinite` (`instance_upgrade.go`) as Task 10 left them, and the test helpers `newTHHost`, `(*thHost).nm`, `(*thHost).env`, `(*thHost).journal` (`testhost_test.go`). `core.Definite`, `core.IsDefinite` (`internal/core/contract.go`) are unchanged.
- Produces: nothing another task consumes. The contract of `nm.run` changes in one place: a refusal is `Definite` only when no earlier effect of the change was kept. Every caller of `run` gets this without a code change.

**Items:** 2026-10-21-v1-completion#435

**Depends on:** Task 10
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 0 - spec 2 - coupling 0 - risk 2 = 4

Hazard: `nm.run` marks every refusal `Definite`, a 4xx of the Engine or of NPM or an error its callback already marked, and `Definite` promises that the call, and so the change, committed nothing. That holds for the first journaled step of a change. It is false for a later step. `npm.instance.remove` removes the container, then the volumes; a 409 on `RemoveVolume` after the container is gone is returned as `Definite` although the host already changed, and `npm.instance.deploy` does the same when the second volume or the start is refused after the network exists. W10a Task 29 removed the same mark for the before-state read of a later removal. The fix keeps the refusal's effect `skipped`, which is true of the one call, and drops the mark from the error when an earlier effect of the change is not `skipped`. `notDefinite` would also drop the error's `errcode` code and its cause, so the helper unwraps the mark layer and keeps everything beneath it.

- [ ] **Step 1: Write the failing tests**

In `internal/modules/npm/effects_test.go`, append (the file's existing imports, `context`, `encoding/json`, `errors`, `testing`, `core`, `errcode` and `dockerapi`, are enough):

```go
// v1r23Run journals one step of change c1 through nm.run. Its observation
// reads back the resource it is asked about.
func v1r23Run(h *thHost, effect core.Effect, do func(context.Context) (string, error)) error {
	observe := func(_ context.Context, _ core.Env, got core.Effect) (core.Observation, error) {
		return named(json.RawMessage(`{"seen":"` + got.Resource + `"}`)), nil
	}
	return h.nm().run(context.Background(), h.env("c1"), effect, observe, do)
}

// TestV1r23RunMarksOnlyTheFirstRefusalDefinite: a refusal proves that nothing
// of the change committed only while no earlier effect of the change was kept.
// A refusal after a made or an intended effect leaves the call skipped but is
// not definite, and it keeps its code and its cause.
func TestV1r23RunMarksOnlyTheFirstRefusalDefinite(t *testing.T) {
	refusal := &dockerapi.APIError{StatusCode: 409, Message: "refused"}
	lost := &dockerapi.APIError{StatusCode: 500, Message: "daemon error"}
	precondition := errcode.New(errcode.InvalidInput, "npm: a precondition failed")
	step := func(name string) core.Effect {
		return npmEffect(core.EffectModify, resourceOf(nounContainer, name), json.RawMessage(`{"running":false}`), commitWait)
	}
	made := func(t *testing.T, h *thHost) {
		t.Helper()
		if err := v1r23Run(h, step("earlier"), func(context.Context) (string, error) { return "", nil }); err != nil {
			t.Fatal(err)
		}
	}
	skipped := func(t *testing.T, h *thHost) {
		t.Helper()
		if err := v1r23Run(h, step("earlier"), func(context.Context) (string, error) { return "", refusal }); err == nil {
			t.Fatal("the earlier step was not refused")
		}
	}
	intended := func(t *testing.T, h *thHost) {
		t.Helper()
		if err := v1r23Run(h, step("earlier"), func(context.Context) (string, error) { return "", lost }); err == nil {
			t.Fatal("the earlier step did not fail")
		}
	}
	tests := []struct {
		name     string
		before   func(*testing.T, *thHost)
		cause    error // what the call returns
		keep     error // what the result must still wrap
		definite bool
		state    core.EffectState // of the effect under test
	}{
		{"nothing journaled before", nil, refusal, refusal, true, core.EffectSkipped},
		{"only a skipped step before", skipped, refusal, refusal, true, core.EffectSkipped},
		{"a made step before", made, refusal, refusal, false, core.EffectSkipped},
		{"an intended step before", intended, refusal, refusal, false, core.EffectSkipped},
		{"nothing before, a refusal the call marked", nil, core.Definite(precondition), precondition, true, core.EffectSkipped},
		{"a made step before, a refusal the call marked", made, core.Definite(precondition), precondition, false, core.EffectSkipped},
		{"a made step before, an answer lost", made, lost, lost, false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			if tc.before != nil {
				tc.before(t, h)
			}
			prior := len(h.journal.effects())
			err := v1r23Run(h, step("later"), func(context.Context) (string, error) { return "", tc.cause })
			if err == nil {
				t.Fatal("run succeeded")
			}
			if core.IsDefinite(err) != tc.definite {
				t.Errorf("Definite = %v, want %v (error %v)", core.IsDefinite(err), tc.definite, err)
			}
			if !errors.Is(err, tc.keep) {
				t.Errorf("error %v lost %v", err, tc.keep)
			}
			effects := h.journal.effects()
			if len(effects) != prior+1 || effects[prior].State != tc.state {
				t.Fatalf("effects %+v, want the effect under test after %d earlier ones to be %s", effects, prior, tc.state)
			}
		})
	}
}
```

In `internal/modules/npm/instance_deploy_test.go`, `TestDpApplyFailsAtEachStep`, replace

```go
				if err == nil || core.IsDefinite(err) != refused {
					t.Fatalf("error %v (definite %t), want definite %t", err, core.IsDefinite(err), refused)
				}
```

with

```go
				wantDefinite := refused && s.index == 0
				if err == nil || core.IsDefinite(err) != wantDefinite {
					t.Fatalf("error %v (definite %t), want definite %t: only a refusal of the first step proves that nothing changed", err, core.IsDefinite(err), wantDefinite)
				}
```

In `internal/modules/npm/instance_remove_test.go`, `TestRmApply`, subtest `the daemon refuses a volume removal`, replace

```go
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
```

with

```go
		var answer *dockerapi.APIError
		if err == nil || core.IsDefinite(err) || !errors.As(err, &answer) || answer.StatusCode != 409 {
			t.Fatalf("error %v (definite %v), want the daemon's 409 as an error that is not definite: the container is already gone", err, err != nil && core.IsDefinite(err))
		}
```

(`errors` and `dockerapi` are already imported by that file.) Leave the rest of the subtest, which already expects the container made and the volume skipped.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/22 && export TMPDIR=/root/.cache/w10b-tmp/22 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r23|TestSdRun|TestDpApplyFailsAtEachStep|TestRmApply' ./internal/modules/npm/`
Expected: FAIL. `TestV1r23RunMarksOnlyTheFirstRefusalDefinite` fails its rows `a made step before`, `an intended step before` and `a made step before, a refusal the call marked` with `Definite = true, want false`; `TestDpApplyFailsAtEachStep` fails the refused rows of every step but the network with `definite true, want false: only a refusal of the first step proves that nothing changed`; `TestRmApply/the_daemon_refuses_a_volume_removal` fails with `want the daemon's 409 as an error that is not definite`. `TestSdRun` still passes. If Task 10 changed the parameters of `nm.run`, `v1r23Run` is the one place that calls it: give it the call form `TestSdRun` uses. Quote the output in the report.

- [ ] **Step 3: Implement**

1. `effects.go`, `nm.run`: before `env.Effects.Intend`, read `env.Effects.List(ctx)`. The step is the first one when every listed effect is in state `core.EffectSkipped`, or the list is empty. Skipped effects count as nothing because a skipped call changed nothing. A failed `List` returns `npm: journal <resource>: %w` before anything is intended.
2. `nm.run`, the refusal branch (an error that `refused` accepts or that is already `Definite`): keep the order of the effects. First `Skipped` is recorded for the effect, with its failure joined to the returned error exactly as today. Then the error is returned as `core.Definite(err)` for the first step, as today, and as `withoutDefiniteMark(err)` for a later step. An error that is neither a refusal nor definite is returned as it is, as today.
3. Add `withoutDefiniteMark(err error) error` in `effects.go`, with a doc comment that says why: it returns err without the definite mark and with everything beneath it, so `errors.As` still finds the `errcode` code and `errors.Is` the cause. While `core.IsDefinite(err)` is true, replace err with `errors.Unwrap(err)`. When `Unwrap` returns nil while the mark is still in the chain (an error built with `errors.Join`), return `notDefinite(err)` instead. An error with no mark is returned as it is.
4. Rewrite the doc comment of `run`: a refusal skips the effect, and the error is `Definite` only for the first kept step of the change, because only then does the refusal prove that the change committed nothing; later it carries no mark, since the earlier effects already changed the host.
5. Run the whole package: `timeout 600 go test -count=1 ./internal/modules/npm/...`. A test that still fails because it asserts `Definite` for a refusal of a step that is not the first kept one of its change is corrected in the way Step 1 corrects the two above. A test that fails for any other reason stops the task: report it and do not change it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w10b-tmp/22 GOTOOLCHAIN=local && timeout 600 go test -count=1 ./internal/modules/npm/...`
Expected: `ok` for every package.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/modules/npm/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/22 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/effects.go internal/modules/npm/effects_test.go internal/modules/npm/instance_deploy_test.go internal/modules/npm/instance_remove_test.go
git commit -m "fix(npm): mark only a first refusal as definite"
```

### Task 24: Diagnose and fix the IPv6 recreate-under-scan flake

**Files:**
- Modify: `test/harness/m3_docker_traffic_test.go` (`m3dTrafficProbeScript`, `m3dxTrafficCount`, `m3dxTrafficLeaks`, `m3dTrafficRecreate`)
- Create: `test/harness/m3_docker_window_unit_test.go`
- Read, and change only if the diagnosis names them: `test/harness/m3_docker_test.go`, `test/harness/m3_docker_helpers_test.go`
- Modify only if the cause is in the product: the non-test files `internal/modules/firewall/reconcile.go`, `internal/modules/firewall/actions_container.go`, `internal/modules/firewall/docker_*.go`, plus the new test `internal/modules/firewall/reconcile_window_test.go`. A fix that needs any other file stops the task and is reported.

**Interfaces:**
- Consumes: the harness helpers `m3dTrafficProbe`, `m3dxTrafficProbed`, `m3dxShell`, `m3dTrafficPublish`, `m3drApply`, `waitHosts`, and `update.AgentUnit`; for a product fix, the fakes of `reconcile_test.go` (`rcHost`, `rcState`, `rcWriteBlock`, `rcTask`) and of `docker_e2e_host_test.go`. None changes.
- Produces: nothing another task consumes. The probe log line grows an optional third field, the start of the attempt in Unix nanoseconds (`<port> <ok|fail> <ns>`); a line of two fields stays valid. `v1r24Window` and `v1r24Windows(log string) ([]v1r24Window, error)` are test-only helpers.

**Items:** 2026-10-21-v1-completion#389

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 3 - spec 2 - coupling 0 - risk 2 = 7

The failure (row 389; row 400 lists the same scenario): harness run 42231106496 (2026-11-06, branch `worktree-v1-w10a-known-fixes`, job `vm (ubuntu-22.04)`) failed `TestM3Docker/traffic-recreate-under-scan` with `IPv6 probe: harness: port 18209 connected in 3 of 23 attempts` (`m3_docker_traffic_test.go:519`). The scenario publishes `m3d-r1` on 18208 (allowed) and `m3d-r2` on 18209 (never allowed), probes `agentIPv4:18209` and `agentIPv6:18209` from the scanner VM in a loop, recreates both containers four times (once after an agent restart), and requires that 18209 never connects. The next run, 42237150792, passed, so the failure is intermittent. This task finds the cause before it changes anything. The tests below are the one deliverable certain in advance; the fix follows from what Steps 1 to 4 establish.

- [ ] **Step 1: Read the evidence (no code change)**

1. Run `gh run view 42231106496 --log-failed` and find `TestM3Docker/traffic-recreate-under-scan`. Record: the 23 lines of the IPv6 log are 15 `fail`, then 3 consecutive `ok`, then 5 `fail`; the IPv4 probe of the same run shows no leak; 18208 stayed open and every other subtest of the run passed. One contiguous window of about a second, on one family, fits a transient state of the host and does not fit a port that was open all along.
2. Check what else the run kept. Download nothing into the checkout; list the artifact with `gh api repos/:owner/:repo/actions/runs/42231106496/artifacts --jq '.artifacts[].name'`, fetch `logs-ubuntu-22.04` with `gh api` to a file in an empty directory of your own, and read it with `unzip -l` and `unzip -p`. On 2026-11-06 it held only the VMs' boot consoles (`harness-m3d/m2/agent-console.log` ends at cloud-init, nothing names port 18209 or UFW). So the run cannot settle the cause: it has no agent journal, no firewall state and no time stamps on the probe log. Record that.
3. Look for the earlier failures that rows 389 and 400 name (harness run 41696527324 on debian-12 and the v1-w7 gate-7 run on ubuntu-22.04) with `gh run view <id> --log-failed`. For each, record whether the probe was IPv6, whether the `ok` lines were one contiguous window, and which OS ran it. A log that has expired is recorded as expired.

- [ ] **Step 2: Read the test and the probe (no code change)**

Read `m3dTrafficRecreate`, `m3dTrafficProbe` and `m3dTrafficProbeScript` in `test/harness/m3_docker_traffic_test.go`, and `TestM3Docker` in `m3_docker_test.go`. Record these facts, each of which the hypotheses below use:
1. Before the probes start, the scenario scans only IPv4 (`ScanOpts{Family: "-4"}`) for 18209; the IPv6 state of 18209 is never checked before the probe loop.
2. The probe is a Python loop on the scanner: `connect` with a 0.4 s timeout, then a 0.1 s sleep. An `ok` line needs a completed TCP handshake with `agentIPv6:18209`, which only the agent's kernel can complete: a timeout or a refusal is `fail`. The scanner cannot be answering for itself, because the target is the agent's address.
3. `recreate()` removes and republishes `m3d-r2` and then `m3d-r1`. Only `m3d-r1` has an allowance. Whether a recreated container gets the address it had before is not known from the code; Docker's allocator may hand out the next address instead (inference, unverified). Step 4 settles what depends on it.
4. The log has no time stamps, so a window cannot be placed against a recreation, the agent restart or an agent action.

- [ ] **Step 3: Read the product path (no code change)**

Read `reconcileWatch`, `reconcile`, `reconcileWith` in `internal/modules/firewall/reconcile.go`, `blockFiles` in `docker_state.go`, `containerRuleOps` (`docker_ruleops.go`) and its callers in `actions_container.go` (the line `c.reload = len(c.files) > 0`), `stopFamily` in `internal/modules/firewall/predict/predict.go`, `reloadDockerUser` in `docker_preserve.go`, and spec 2.6 "Proxied families" (`rg -n 'Proxied families' docs/dforge/specs/2026-10-21-v1-completion-design.md`). Record these facts:
1. Every `container start`, `die` and `destroy` event wakes `firewall.reconcile`. The pass compares the block files it would render, `blockFiles(...)`, with the files on disk. The rendered block holds lines that carry container addresses (`dockerfw.Render`, `BindingLine.ContainerAddr`), so a recreated container whose address changed changes `/etc/ufw/after.rules`, and the pass then applies `firewall.containers.apply` (`reconcileWith`).
2. `containers.apply` with changed files sets `reload`, which runs `ufw reload`. The product's own model of it says what that is: `predict.go` states that `TransReload` is a stop followed by a start, and `stopFamily` sets the built-in INPUT, OUTPUT and FORWARD policies to ACCEPT in each family before the start loads the rules again.
3. A published port that `docker-proxy` serves for an IPv6 binding of an IPv4-only container never crosses `FORWARD`; spec 2.6 says UFW's `INPUT` chain governs it and the integration requires a default incoming policy of DROP or REJECT, as an invariant while the integration is on. The IPv4 path crosses `DOCKER-USER`, which the block fills; that a reload leaves it loaded is an inference from the predictor's own model of `ufw_stop` (`deleteChains` removes UFW's chains only), not something the run showed.
4. The existing helper comment on `m3drApply` already states that a container a traffic scenario creates wakes `firewall.reconcile`, "whose file writes and ufw reload" overlap a plan.

- [ ] **Step 4: State the hypotheses and rule each in or out**

Write the outcome of each into the report. For each, name the observation that decides it.
- **H1, the reload window (product).** Each recreation changes the block, the reconcile pass reloads UFW, and for the time of the reload the INPUT policy is ACCEPT, so the `docker-proxy` socket on `[::]:18209` accepts a connection that the invariant says is closed. IPv4 would be spared because its path crosses the `DOCKER-USER` chain, which a reload does not flush (inference, see Step 3). It fits: IPv6 only, one contiguous window of about a second, intermittent (a window of that length needs an attempt inside it), and the probe being the first check in the suite that runs continuously. It is ruled in when a scratch test, not kept, shows that `blockFiles` renders different files for the same state after a container's address changes and that `reconcileWith` then applies `firewall.containers.apply` whose predicted transition is a reload (use `rcState`, `rcFacts`, `rcWriteBlock` and `rcTask` in `reconcile_test.go`). It is ruled out when the rendered files do not change across a recreation, or no reload is predicted. The same scratch test settles the inference of Step 2 item 3: it must render once with the recreated container at its old address and once at a new one, and H1 needs only the second case to occur sometimes.
- **H2, the agent's first pass after its restart (product).** `reconcile` begins with `finishCleanups`, whose `ufw reload` (`docker_cleanup.go`) opens the same window once per agent start. It is ruled in or out together with H1 by the same reading; it differs only in which recreation the window follows, which the time stamps of Step 7 show on a later run.
- **H3, a harness artefact.** Something other than the product answers on `agentIPv6:18209`, or the probe counts a non-connection as `ok`. Rule it out from Step 2: an `ok` needs a completed handshake with the agent's address, no rule of the harness allows 18209, and `m3dTrafficProbe` removes its log and uses `--collect`, so a stale unit cannot feed it. It stays in only if the code shows a path I have not listed.
- **H4, a firewall rule the harness itself installed.** Run `rg -n '18209' test/harness internal` and read every hit: a rule that allows 18209 for the IPv6 family would make the port open all the time, which the 15 leading and 5 trailing `fail` lines contradict.

- [ ] **Step 5: Write the failing Unit tests**

These make the next failure explain itself whatever the cause, because Step 2 showed that the run keeps no evidence. Create `test/harness/m3_docker_window_unit_test.go`:

```go
//go:build harness

package harness

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// v1r24At is the time of the attempt that starts sec seconds into a fixed minute.
func v1r24At(sec int) time.Time {
	return time.Date(2026, time.November, 6, 14, 42, sec, 500, time.UTC)
}

// v1r24Line is one probe log line of port with a start time.
func v1r24Line(port int, result string, sec int) string {
	return strconv.Itoa(port) + " " + result + " " + strconv.FormatInt(v1r24At(sec).UnixNano(), 10)
}

func TestV1r24UnitProbeWindows(t *testing.T) {
	timed := strings.Join([]string{
		v1r24Line(18209, "fail", 1), v1r24Line(18210, "fail", 1),
		v1r24Line(18209, "ok", 2), v1r24Line(18210, "fail", 2),
		v1r24Line(18209, "ok", 3), v1r24Line(18210, "ok", 3),
		v1r24Line(18209, "fail", 4), v1r24Line(18210, "fail", 4),
		v1r24Line(18209, "ok", 5), v1r24Line(18210, "fail", 5),
		"",
	}, "\n")
	for _, tc := range []struct {
		name string
		log  string
		want []v1r24Window
		bad  string // "" accepts
	}{
		{"two ports interleaved", timed, []v1r24Window{
			{Port: 18209, Attempts: 2, First: v1r24At(2), Last: v1r24At(3)},
			{Port: 18209, Attempts: 1, First: v1r24At(5), Last: v1r24At(5)},
			{Port: 18210, Attempts: 1, First: v1r24At(3), Last: v1r24At(3)},
		}, ""},
		{"a log without times", "18209 ok\n18209 ok\n18209 fail\n18209 ok\n", []v1r24Window{
			{Port: 18209, Attempts: 2},
			{Port: 18209, Attempts: 1},
		}, ""},
		{"nothing connected", "18209 fail\n18209 fail\n", nil, ""},
		{"a time that is not a number", "18209 ok soon\n", nil, "malformed"},
		{"a fourth field", "18209 ok 12 extra\n", nil, "malformed"},
		{"an unknown result", "18209 maybe 12\n", nil, "malformed"},
	} {
		got, err := v1r24Windows(tc.log)
		if tc.bad != "" {
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.bad)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: windows %+v, want %+v", tc.name, got, tc.want)
			continue
		}
		for i, w := range tc.want {
			g := got[i]
			if g.Port != w.Port || g.Attempts != w.Attempts || !g.First.Equal(w.First) || !g.Last.Equal(w.Last) {
				t.Errorf("%s: window %d is %+v, want %+v", tc.name, i, g, w)
			}
		}
	}
}

// TestV1r24UnitLeakNamesTheWindows: a leak reported by m3dxTrafficLeaks names
// each window of the leaking port with its attempts and, when the log carries
// times, its start and end, so the failure can be placed against the agent's
// journal. A log of two-field lines gives the same error without times.
func TestV1r24UnitLeakNamesTheWindows(t *testing.T) {
	var timed, plain strings.Builder
	for i := range 23 {
		result := "fail"
		if i >= 15 && i <= 17 {
			result = "ok"
		}
		timed.WriteString(v1r24Line(18209, result, i) + "\n")
		plain.WriteString("18209 " + result + "\n")
	}

	err := m3dxTrafficLeaks(timed.String(), []uint16{18209}, 20)
	if err == nil {
		t.Fatal("m3dxTrafficLeaks accepted a log with a leak")
	}
	first, last := v1r24At(15).Format(time.RFC3339Nano), v1r24At(17).Format(time.RFC3339Nano)
	for _, want := range []string{"port 18209 connected in 3 of 23 attempts", "3 attempts from " + first + " to " + last} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	err = m3dxTrafficLeaks(plain.String(), []uint16{18209}, 20)
	if err == nil || !strings.Contains(err.Error(), "port 18209 connected in 3 of 23 attempts") || !strings.Contains(err.Error(), "3 attempts") || strings.Contains(err.Error(), " from ") {
		t.Errorf("plain log: %v, want the count and a window of 3 attempts without times", err)
	}
	if err := m3dxTrafficProbed(timed.String(), []uint16{18209}, 20); err != nil {
		t.Errorf("a timed log with 23 attempts: %v, want nil", err)
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/23 && export TMPDIR=/root/.cache/w10b-tmp/23 GOTOOLCHAIN=local && timeout 300 go vet -tags harness ./test/harness/`
Expected: build failure `undefined: v1r24Window` (the first undefined identifier of `TestV1r24UnitProbeWindows`). Quote it. After the stubs of Step 7 exist, `timeout 300 go test -tags harness -count=1 -run 'TestV1r24Unit' ./test/harness/` fails on `malformed probe log line` for the timed lines, because `m3dxTrafficCount` takes only two fields today.

- [ ] **Step 7: Implement the probe's time stamps**

1. `m3dTrafficProbeScript`: take `t0 = time.time_ns()` before each `connect` and write `"%d %s %d\n" % (port, result, t0)`. The script's own doc comment (above the constant) names the third field.
2. `m3dxTrafficCount`: accept two fields, or three when the third parses as a positive integer; anything else, a fourth field included, is `harness: malformed probe log line %q` as today. It keeps counting attempts and `ok` lines per port. `m3dxTrafficProbed` and its callers need no change.
3. Add `v1r24Window` (`Port uint16`, `Attempts int`, `First, Last time.Time`; the times are the start of the first and of the last attempt of the run, zero for a log without times) and `v1r24Windows(log string) ([]v1r24Window, error)`. A window is a run of consecutive `ok` attempts at one port; a `fail` of that port ends it, and other ports' lines do not. The result is sorted by port, then by order of appearance. Share the line parser with `m3dxTrafficCount`, so the two accept the same lines.
4. `m3dxTrafficLeaks`: keep the message `harness: port %d connected in %d of %d attempts` and append ` (` + the windows of that port, joined with `; `, + `)`. A window prints `<n> attempts`, and with times `<n> attempts from <First> to <Last>`, both as `time.RFC3339Nano` in UTC.
5. `m3dTrafficRecreate`: log the time (`time.Now().UTC().Format(time.RFC3339Nano)` through `t.Logf`) when each of the three recreations and the agent restart begins and ends, and at the start log `date +%s%N` as the scanner and the agent print it (`f.scanner.Run`, `f.agent.Run`), so the clock difference between the VMs is in the log. When a probe reports a leak, also log the agent's journal for the scenario: `sudo journalctl -u <update.AgentUnit> --utc -o short-iso-precise --no-pager --since "<scenario start as 2006-01-02 15:04:05>"`, built with `m3dxShell` like the neighbouring commands and read with `f.agent.Run`. A failed journal read is logged, not fatal. These lines do not change what the test requires.
6. Do not relax any assertion of the scenario: `minAttempts`, the `ok`-means-leak rule and the IPv4 and IPv6 probes stay.

- [ ] **Step 8: Decide and fix**

Apply this rule to what Steps 1 to 4 established; write the cause, the evidence and the branch taken into the report.
- **The cause is in the harness test** (H3 or H4 stands, or a defect in the probe or the scenario that I did not list): fix it in `test/harness`, with a test whose name contains `Unit` that fails on `main` for that defect and passes after (`TestV1r24Unit…`, in `m3_docker_window_unit_test.go` or the unit file of the file you changed).
- **The cause is in the product** (H1 or H2 stands): fix it in the files listed above, with a unit regression test `TestV1r24…` in `internal/modules/firewall/reconcile_window_test.go`, written on the fakes of `reconcile_test.go` (`rcHost`, `rcState`, `rcWriteBlock`, `rcTask`) or of `docker_e2e_host_test.go`. The test drives a reconcile pass over a recreated container whose address changed, records the host's commands, and asserts the property the cause names. For H1 the assertion is that the pass changes the block without ever leaving the loaded built-in INPUT policy at ACCEPT, in either family: it records no `ufw reload` (or whatever mechanism the fix chooses that keeps the policy at DROP throughout) and the rules loaded afterwards equal what `blockFiles` rendered. It fails on `main` because `main` records `ufw reload`. The fix must remove the window, not shorten it, and must keep the lockout guard, the revert timer and the predictor's agreement with the loaded state; `reloadDockerUser` in `docker_preserve.go` already loads the Darkpanel chains without stopping UFW and is the first mechanism to consider. If the only fix is a design decision the spec does not make (for example replacing `ufw reload` for every block-file change), stop after recording the cause and the options, and report NEEDS_CONTEXT with the decision you need.
- **Neither can be shown from the evidence** (the Step 4 checks are inconclusive): change no product code and do not guess a fix. Keep the Step 7 instrumentation, report the cause as unproven, state which check would settle it, and say that the next harness failure now carries the time stamps and the agent journal to settle it. Gate 7 will then show whether it recurs.

- [ ] **Step 9: Verify**

Run: `export TMPDIR=/root/.cache/w10b-tmp/23 GOTOOLCHAIN=local && timeout 300 go vet -tags harness ./test/... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/...`
Expected: no output from `go vet`, `0 issues.`, and `ok` for the `Unit` tests, including `TestV1r24Unit…`. If the product changed, also run `timeout 600 go test -count=1 -race ./internal/modules/firewall/...`; expected `ok`.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Finish with `rm -rf /root/.cache/w10b-tmp/23 && GOTOOLCHAIN=local go clean -testcache`.

The scenario itself runs only on VMs. Report it as not run here: gate 7's harness run on the pushed branch is the verification, and `TestM3Docker` must pass on all four OS there. A single green run does not prove an intermittent fault is gone, so the report states the cause and the unit test that pins it rather than the green run.

- [ ] **Step 10: Commit**

```bash
git add test/harness/m3_docker_traffic_test.go test/harness/m3_docker_window_unit_test.go
git commit -m "test(harness): time-stamp the recreate probe log"
```

If the diagnosis changed other files, add them with the cause in the commit as a second commit, with the type the change has (`fix(firewall): ...` or `fix(harness): ...`), one concern per commit.

### Task 25: Update the threat model to the reviewed state

**Files:**
- Modify: `docs/security/threat-model.md`
- Modify: `internal/docs/threat_model_test.go` (`w10tmOpen`, and the bullet-count check of `TestW10TmKnownResidualRisks`)
- Create: `internal/docs/threat_model_state_test.go`

**Interfaces:**
- Consumes: Contracts "Probe errors", "Compose failures", "Login locks", "NPM forward-host refusals", "Internet cells" and "Keyed fingerprints", and the rulings W10b-R2 to W10b-R12 of the plan's Assumptions, as the fixes of Tasks 2 to 23 implement them. The page names none of them. The existing helpers `dcRead`, `dcMissing` (`helpers_test.go`) and `w10tmParse`, `w10tmBullets`, `w10tmPage` (`threat_model_test.go`).
- Produces: nothing another task consumes. The page keeps the headings, the three parts of every area and the facts that the existing `TestW10Tm*` tests pin.

**Items:** 2026-11-06-v1-security-review#16, 2026-11-06-v1-security-review#17, 2026-11-06-v1-security-review#18, 2026-11-06-v1-security-review#19, 2026-11-06-v1-security-review#20, 2026-11-06-v1-security-review#21, 2026-11-06-v1-security-review#22, 2026-11-06-v1-security-review#23, 2026-10-21-v1-completion#37, 2026-10-21-v1-completion#245, 2026-10-21-v1-completion#329

**Depends on:** Task 1, Task 2, Task 3, Task 4, Task 5, Task 6, Task 7, Task 8, Task 9, Task 10, Task 11, Task 12, Task 13, Task 14, Task 15, Task 16, Task 17, Task 18, Task 19, Task 20, Task 21, Task 22, Task 23, Task 24
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

The page describes what exists on `main` after the fixes of this plan, and the open risks the owner accepted on 2026-11-06. Three changes in kind: the Mitigations and Residual risk bullets that the fixes of Tasks 5 to 21 changed say what the code now does; four claims that the review found inaccurate are corrected (a `ufw` argument is protected by `withComment` and the tag prefix, not by the comment pattern; the DERP directory is created at mode 0700 but an existing directory is accepted; `-verify-clients` has a supported opt-out; the CLI also reads secrets from a JSON input file); and `## Known residual risks` holds only what stays open: the four accepted risks (NPM's ports inside its container, the `openpgp` advisory, the missing Origin header, the global enrollment budget) and the rebinding of a forward host after it was applied. The fixed questions (RFC 1918 probe targets, the unsalted label digest, the unresolved forward host, port 3000) leave the section.

- [ ] **Step 1: Write the failing tests**

In `internal/docs/threat_model_test.go`, replace the declaration of `w10tmOpen` with

```go
var w10tmOpen = []string{"Origin", "port 81", "port 3000", "openpgp", "enrollment budget", "forward host"}
```

and, in `TestW10TmKnownResidualRisks`, replace

```go
	if len(bullets) < len(w10tmOpen) {
		t.Errorf("Known residual risks has %d bullets, want at least %d: one for each open security question", len(bullets), len(w10tmOpen))
	}
```

with

```go
	if len(bullets) == 0 {
		t.Fatal("Known residual risks has no bullets")
	}
```

Create `internal/docs/threat_model_state_test.go`:

```go
package docs_test

import (
	"strings"
	"testing"
)

// v1r25Part returns the bullets of one part of an area, one to a line.
func v1r25Part(d w10tmDoc, key string) string {
	return strings.Join(w10tmBullets(d.parts[key]), "\n")
}

// TestV1r25KnownResidualRisksAreWhatStaysOpen: the section ends with the four
// risks the owner accepted and the rebinding of a forward host, and with no
// question described as undecided.
func TestV1r25KnownResidualRisksAreWhatStaysOpen(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	lines := d.lines["Known residual risks"]
	bullets := w10tmBullets(lines)
	want := []struct {
		prefix string
		terms  []string
	}{
		{"- `Origin`:", []string{"accepted"}},
		{"- `port 81`:", []string{"port 3000", "accepted"}},
		{"- `openpgp`:", []string{"GO-2026-5932", "accepted"}},
		{"- `enrollment budget`:", []string{"accepted"}},
		{"- `forward host`:", []string{"re-pointed"}},
	}
	if len(bullets) != len(want) {
		t.Fatalf("Known residual risks has %d bullets, want %d:\n%s", len(bullets), len(want), strings.Join(bullets, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(bullets[i], w.prefix) {
			t.Errorf("bullet %d is %.60q, want it to start with %q", i+1, bullets[i], w.prefix)
		}
		for _, name := range dcMissing(bullets[i], w.terms) {
			t.Errorf("bullet %d does not mention %q", i+1, name)
		}
	}
	var intro []string
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			break
		}
		intro = append(intro, line)
	}
	text := strings.Join(intro, " ")
	if !strings.Contains(text, "accepted") || strings.Contains(text, "None is described as accepted") {
		t.Errorf("the introduction %q must say that the owner accepted the risks", text)
	}
	all := strings.Join(bullets, "\n")
	for _, gone := range []string{"RFC 1918", "digest", "undecided"} {
		if strings.Contains(all, gone) {
			t.Errorf("Known residual risks still mentions %q, a question the fixes closed", gone)
		}
	}
	if page := dcRead(t, w10tmPage); strings.Contains(page, "open question") {
		t.Error("the page still calls a risk an open question; each is accepted or fixed")
	}
}

// TestV1r25PageStatesWhatTheCodeNowDoes: each bullet a fix or a corrected
// claim changed holds its statement, and the claim the review found
// inaccurate is gone.
func TestV1r25PageStatesWhatTheCodeNowDoes(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, tc := range []struct {
		key  string
		must []string
		not  []string
	}{
		{"Authentication/Mitigations", []string{
			"a lock on the pair of user name and source", "the /64 of its IPv6 address", "the client address the listener parsed",
		}, nil},
		{"Authentication/Residual risk", []string{"its own attempts against a user name"}, nil},
		{"Agent protocol/Residual risk", []string{"global enrollment budget"}, nil},
		{"Command execution/Mitigations", []string{"`withComment`", "`darkpanel:`"},
			[]string{"the input schema patterns refuse a value that starts with `-`"}},
		{"Compose execution/Mitigations", []string{"exit status", "Compose's stderr"}, nil},
		{"Secret handling and delivery/Mitigations", []string{
			"a JSON input file named with `--input`", "opens the copy without following a link", "a keyed fingerprint",
			"label values are secret inputs", "every element of a container's `Cmd`",
		}, []string{"only from a prompt or standard input"}},
		{"Firewall/Mitigations", []string{
			"public leg of a panel exposure", "fails closed", "each cell of the public unicast space", "first blocked cell",
		}, nil},
		{"Exposure/Mitigations", []string{
			"any port of NPM's own container names", "at plan and again at apply", "`plan.state_changed`",
			"allowlisted error code", "first administrator",
		}, nil},
		{"Exposure/Residual risk", []string{"forward host that is a DNS name"}, nil},
		{"DERP host/Threats", []string{"compromised `darkpanel-derp` account"}, nil},
		{"DERP host/Mitigations", []string{
			"`verifyClients: false`", "supported opt-out", "repository file", "a directory the service user already owns",
			"no symlink anywhere on the path", "1 MiB", "`operate`", "CGNAT", "ULA", "`no_stun_answer`",
		}, []string{"created and kept at mode 0700"}},
		{"DERP host/Residual risk", []string{"debug log"}, nil},
	} {
		got := v1r25Part(d, tc.key)
		for _, name := range dcMissing(got, tc.must) {
			t.Errorf("%s does not state %q", tc.key, name)
		}
		for _, name := range tc.not {
			if strings.Contains(got, name) {
				t.Errorf("%s still says %q", tc.key, name)
			}
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w10b-tmp/24 && export TMPDIR=/root/.cache/w10b-tmp/24 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r25|TestW10Tm' ./internal/docs/`
Expected: FAIL. `TestV1r25KnownResidualRisksAreWhatStaysOpen` reports 7 bullets where 5 are wanted, `TestV1r25PageStatesWhatTheCodeNowDoes` reports each missing statement (for example `Authentication/Mitigations does not state "a lock on the pair of user name and source"`), and `TestW10TmKnownResidualRisks` reports `Known residual risks does not mention "enrollment budget"`. The other `TestW10Tm*` tests pass. Quote the output in the report.

- [ ] **Step 3: Implement**

Edit `docs/security/threat-model.md` in the page's style (English, present tense, plain Markdown, one bullet per line, a path in backticks at the end of every Mitigations bullet). Before writing a bullet, confirm that the merged code does what it says: for example `rg -n 'hmac-sha256:' internal/modules/npm internal/modules/docker --glob '!*_test.go'`, `rg -n 'O_NOFOLLOW' internal/controller/backup/stream.go`, `rg -n 'func InternetCells' internal/modules/firewall/eval`, `rg -n 'plan.state_changed' internal/modules/npm --glob '!*_test.go'`, `rg -n 'no_stun_answer' internal/modules/derp --glob '!*_test.go'`, `rg -n 'LookupNetIP' internal/modules/npm --glob '!*_test.go'`. A statement the code does not hold is written as the code does it, and the difference goes into the report. Every path a bullet names exists. Nothing on the page names a plan, wave, task, register row or review, or promises future work. Each text below is one bullet, written without its fence; the words in the tests' `must` lists appear in them verbatim. The edits, area by area:

1. `### Authentication`
   - Mitigations: replace the bullet that begins `A login rate limit by user name and by source address` with

     ```text
     - A login rate limit applies a lock by source address and a lock on the pair of user name and source, where the source is the client's IPv4 address or the /64 of its IPv6 address. Behind a runtime listener the source is the client address the listener parsed, not the text the audit log keeps. A flood of new keys cannot clear a running lock (`internal/controller/auth`, `internal/controller/api`).
     ```

   - Residual risk: add

     ```text
     - A lock on the pair of user name and source gives each source its own attempts against a user name, so an attacker who uses many sources, many IPv6 /64 networks included, makes more guesses than one who uses few. A correct password is still not enough to sign in without a TOTP code.
     ```

     In the bullet that ends `The open question about the Origin header is listed in Known residual risks.`, replace that last sentence with `The accepted risk of a request without an Origin header is listed in Known residual risks.`
2. `### Agent protocol`, Residual risk: add

   ```text
   - The global enrollment budget is shared by every source, so a peer that reaches the agent listener can use it up and delay enrollment. The accepted risk is listed in Known residual risks.
   ```

3. `### Command execution`, Mitigations: replace the first bullet with

   ```text
   - The exec provider runs only allowlisted absolute binary paths with a fixed minimal environment. It never uses a shell and runs only argument vectors that a rule for that path accepts, mostly exact vectors. ufw has no `--`, so a value that starts with `-` must never reach it as an argument: the input schema patterns of an address, a port and an interface refuse such a value, and a comment reaches ufw only after the tag prefix `darkpanel:` that every rule a client adds carries, because `withComment` drops the comment of a manual rule that starts with `-` (`internal/platform`, `internal/modules/firewall/model`).
   ```

4. `### Compose execution`, Mitigations: add

   ```text
   - A failed `docker compose config` or run reports its exit status and the command to run on the host to see why, and no byte of Compose's stderr reaches an error, a change record or a log line, because Compose can quote values it resolved from env files (`internal/modules/docker`).
   ```

5. `### Secret handling and delivery`, Mitigations:
   - replace the bullet that begins `The CLI takes secrets only from a prompt` with

     ```text
     - The CLI takes a secret from a prompt, from standard input or from a JSON input file named with `--input`, and never from a flag; the file's path is on the command line and its content is not (`internal/cli`).
     ```

   - replace the bullet that begins `A restore stages with mode 0600` with

     ```text
     - A restore stages with mode 0600 and gives owner and mode to each staged file through one descriptor that refuses links. A backup keeps its plaintext copy of the database in a private directory that the controller sweeps at startup, opens the copy without following a link, sets its mode 0600 through that descriptor and streams from the same descriptor, and reads the key file with a bound, so a key that grew is refused (`internal/controller/backup`).
     ```

   - add these three bullets:

     ```text
     - An effect document holds a value it only compares as a keyed fingerprint, `hmac-sha256:` followed by the HMAC-SHA-256 of the value under a key derived from the role key, so nobody can check the fingerprint against a guess without that key. A value an undo restores goes into the effect's sealed secret instead. This covers the settings of Nginx Proxy Manager proxy hosts, custom locations included, and the labels of Docker networks and volumes (`internal/modules/npm`, `internal/modules/docker`, `internal/core/lifecycle`).
     - Network and volume label values are secret inputs: a plan shows a label's key and whether it is added, changed or removed, and never its value (`internal/modules/docker`).
     - Container inspection masks every element of a container's `Cmd`, because an image with an `Entrypoint` makes the first element an argument and not a program. An element of `Entrypoint` keeps the masking rule it had (`internal/modules/docker`).
     ```

6. `### Firewall`, Mitigations: replace the first bullet with

   ```text
   - A lockout guard predicts the effect of a change on the protected flows and refuses it unless explicitly overridden. It checks the public leg of a panel exposure, the browser to NPM binding, from the exposure's own record, and a request through an NPM exposure without those bindings fails closed. It evaluates "any internet address" once for each cell of the public unicast space that the ruleset's prefixes cut and holds only when every cell passes, and a refusal names the first blocked cell (`internal/modules/firewall/predict`, `internal/modules/firewall/eval`, `internal/modules/firewall/guard`, `internal/controller/api`, `internal/modules/panel`).
   ```

   Then run `rg -l 'TestV1r24' internal/modules/firewall`. When it lists a test, Task 24 fixed a product cause: read that test's doc comment and add one sentence stating the property it asserts to the Docker-integration bullet of the same part, and keep the bullet's path list. When it lists nothing, Task 24 changed no product code and the part needs no further edit.
7. `### Exposure`
   - Threats: replace `A proxy host that points at the panel or at NPM itself.` with `A proxy host that points at the panel or at NPM itself, by address or through a DNS name.`, and add the bullet `A local user who creates Nginx Proxy Manager's first administrator before Darkpanel does.`
   - Mitigations: replace the bullet that begins `Proxy hosts that forward to NPM's own port 81` with

     ```text
     - Outside the admin-exposure composite, a proxy host may not forward to any port of NPM's own container names or addresses, or to the panel gateway; the refusal is `npm.reserved`, and port 81 stays allowed inside that composite. A forward host that is a DNS name is resolved on the agent's host at plan and again at apply: every answer is checked as an address, a name with no answer is invalid input, and a set of answers that changed between the two is refused as `plan.state_changed` (`internal/modules/npm/proxy`, `internal/modules/npm`).
     ```

     Replace the bullet that begins `The agent's NPM client limits the size` with

     ```text
     - The agent's NPM client limits the size of every answer and drains refusals. It keeps only the status and an allowlisted error code of a refusal, so NPM's message text never reaches an error, a log line or a change record (`internal/modules/npm/npmapi`).
     ```

     Add

     ```text
     - When the creation of NPM's first administrator finds one that Darkpanel did not create, the deploy stops and removes the container and the data volumes it created before it fails (`internal/modules/npm`).
     ```

   - Residual risk: replace the bullet that begins `The open questions about Origin` with

     ```text
     - A forward host that is a DNS name and is re-pointed after it was applied is not checked again. The accepted risk is listed in Known residual risks.
     ```

     Replace the bullet that begins `A container on a network joined through the NPM attach action` with

     ```text
     - A container on a network joined through the NPM attach action shares that network with NPM and reaches its admin login on port 81 and its API on port 3000 inside the container; the admin password is 32 random bytes.
     ```

8. `### DERP host`
   - Threats: add

     ```text
     - A compromised `darkpanel-derp` account that redirects or blocks a read the agent makes as root.
     ```

   - Mitigations: replace the bullet that begins `Derper runs with` with

     ```text
     - Derper runs with `-verify-clients` unless the install input sets `verifyClients: false`, a supported opt-out whose plan warns that any node that knows the region can relay through the server. With the flag, derper checks every client's node key against the host's own `tailscaled` and refuses the client when `tailscaled` fails (`internal/modules/derp`).
     ```

     In the bullet that begins `The host's` and names `tailscaled` as pinned, replace the clause about the rollback with `a pin that fails rolls the package back, and a failed repository-file step rolls the repository file back, each on a context of its own, even when the forward step's context has been cancelled`, keeping the rest of the bullet and its path list. Replace the bullet that begins `Derper's node key file lives in` (it also holds the probe's text) with these three bullets:

     ```text
     - Derper's node key file lives in `/var/lib/darkpanel/derp`, a directory owned by `darkpanel-derp`. Darkpanel creates it at mode 0700 and gives it to the service user at that mode; a directory the service user already owns is accepted as it is, without a mode check (`internal/modules/derp/server/server.go`, `internal/platform/files_servicedir.go`). Darkpanel does not set the mode of the key file itself, which derper writes.
     - The agent reads DERP certificate and key files through the platform's descriptor-based reads: no symlink anywhere on the path, a regular file only, and at most 1 MiB, so a compromised `darkpanel-derp` account cannot redirect a root read or block the agent with a FIFO or a huge file (`internal/platform`, `internal/modules/derp`).
     - `derp.node.probe` needs the `operate` scope. The probe uses a throwaway node key (`internal/platform/tslocal/derp.go`). It accepts only an IP literal or a lower-case DNS name of at least two labels made of letters, digits and hyphens, and refuses loopback, unspecified, multicast, link-local, RFC 1918, CGNAT (`100.64.0.0/10`) and ULA (`fc00::/7`) addresses, as literals and among the addresses a name resolves to, after it unmaps IPv4-mapped IPv6 addresses. A failed probe reports one value of a fixed set (`timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`, `certificate_not_trusted`, `pin_mismatch`, `bad_answer`, `no_stun_answer`, `failed`), and the raw error appears only in the agent's debug log (`internal/modules/derp/node_probe.go`).
     ```

   - Residual risk: replace the bullet that begins `The probe's target ranges` with

     ```text
     - A failed probe reports only a fixed error value, so finding the cause of a failure needs the agent's debug log.
     ```

9. `## Known residual risks`: replace the introduction and every bullet with exactly this text, and nothing else:

   ```text
   These risks stay open. The owner accepted the first four for this release after the security review; the last is inherent in resolving a name at a point in time.

   - `Origin`: a request without an Origin header is accepted for a cookie session on an unsafe method through a runtime listener. The SameSite=Strict cookie and the HMAC CSRF token that every unsafe request must carry still guard it, and requiring Origin would add no attacker that those checks let through. The owner accepted this.
   - `port 81`: NPM serves its admin login on port 81 and its API on port 3000 inside its container, on every interface, so any container on a network joined through the attach action reaches both; only the host side of port 81 is bound to loopback. The admin password is 32 random bytes and the pinned image offers no way to bind the ports to loopback, so a joined container gains an authentication surface and nothing more. The owner accepted this.
   - `openpgp`: the `openpgp` package of `golang.org/x/crypto` carries advisory GO-2026-5932 and no tagged release carries the fix. Darkpanel imports nothing from it. The pin stays until a tagged release carries the fix. The owner accepted this.
   - `enrollment budget`: the global enrollment budget is 60 requests a minute across all sources, so a peer that reaches the agent listener, on the tailnet by design, can use it up and block further enrollment for the rest of the minute. It affects enrollment only, and an enrollment token lasts one hour, so a sustained flood forces a new token. The owner accepted this.
   - `forward host`: a forward host that is a DNS name is checked when the plan is made and again when it is applied, and a name that is re-pointed after it was applied is not checked again, so a name the caller controls can later resolve to an address that was refused at apply.
   ```

Keep the page's other sections as they are.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w10b-tmp/24 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestV1r25|TestW10Tm' ./internal/docs/ && timeout 300 go test -count=1 ./internal/docs/`
Expected: `ok` for both. The whole package covers the style, link and path tests of the page.

Then run the plan's full Go checks with their real output: `timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/docs/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`. Expected: every test line `ok` or `?`, `0 issues.`, and no output from `go vet` and `go mod tidy -diff`. Also run `rg -n 'open question|undecided|register|row [0-9]' docs/security/threat-model.md` and expect no match. Finish with `rm -rf /root/.cache/w10b-tmp/24 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/security/threat-model.md internal/docs/threat_model_test.go internal/docs/threat_model_state_test.go
git commit -m "docs(security): update the threat model after review"
```
