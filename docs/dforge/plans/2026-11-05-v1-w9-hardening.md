# v1 wave 9: fuzzing, accessibility, documentation, upgrade matrix and footprint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 9 of the v1 completion spec — the parser inventory and a fuzz target for every parser in it, with `scripts/fuzz.sh` and `.github/workflows/fuzz.yml` (9a, R74); WCAG 2.1 AA evidence and fixes for the core flows with `docs/accessibility.md` (9b, R77); the user documentation, the module-developer guide and the API reference generated from the action schemas (9c, R78, R79); and the harness tests `TestUpgradeMatrix` and `TestFootprint` with the harness-only `--state-dir` (9d, R75, R76) — discharging register rows 38–43, 107 and 248.

**Architecture:** Five foundation tasks fix the shared surfaces first: the inventory format and runner script, the web accessibility test helpers and the page-title hook, the API-reference generator, the docs-check test helpers, and the harness `--state-dir` hook. Six fuzz tasks then add targets package group by package group against the inventory's fixed target names, while the harness upgrade test runs beside them. Four accessibility tasks audit and fix the core flows in jsdom, four documentation tasks write `docs/user/` and `docs/dev/` against tests that read the registry, and the footprint test follows. A browser pass with Playwright then covers what jsdom cannot (contrast, zoom, reflow, screen-reader names), the conformance record cites every test and screenshot, and a closing task adds the fuzz workflow once every target exists, runs the real inventory check and copies the plan rulings into the spec.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`) with native fuzzing (`testing.F`), Bash 5 for `scripts/fuzz.sh`, React 19 + TypeScript with Vitest 5.0.5, vitest-axe 1.0.0-pre.5 and @testing-library/user-event 14.6.8, @playwright/test 1.64.0 with axe-core 4.14.0 (both already in `web/pnpm-lock.yaml`), the KVM harness in `test/harness` (GitHub Actions `harness.yml`), GitHub Actions for `fuzz.yml`.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-3 | 4-6 | 7-9 | 10-12 | 13-15 | 16-18 | 19-21 | 22 | 23 | 24

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w9-tmp/integ && export TMPDIR=/root/.cache/w9-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 120 bash scripts/fuzz.test.sh && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w9-tmp/wave && export TMPDIR=/root/.cache/w9-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./internal/... ./cmd/... ./release/... && timeout 1200 make lint-web test-web`

**Plan review:** 2026-11-05 — dforge:judge-opus — executability 16 / coherence 15 / coverage 16 / assumptions 16 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 9's Fuzzing, Upgrade matrix, Footprint, Accessibility and Documentation bullets (R74–R79), the 9a–9d rows of Parallel decomposition, the path-selection hook of 1.12, the M7 row of Verification except the security register, the plan rulings W9-R1 to W9-R10 under Assumptions (Task 24 copies them into the spec), and the register rows the tasks' **Items:** lines name. Out of scope: the threat model and security review (R73, wave 10), the runbook, `CHANGELOG.md` and release readiness (wave 10), the live demonstrations with owner credentials (register row 55), and every open minor of earlier waves that no **Items:** line names.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test, fuzz target or command of this plan talks to the Docker daemon, D-Bus or systemd, runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`, binds an address other than `127.0.0.1` (port 0, or the port Playwright's `webServer` picks in Task 22) or a Unix socket under `t.TempDir()`, or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/run/darkpanel*` or `/proc` of another process. A fuzz target calls a pure function or a function over a fake (`core.Runner`, file reader, `io.Reader`) and never the real host. Real installs, updates, RSS sampling and agents run only on harness VMs (Tasks 11 and 21).
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w9-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` shows 90 % or more before a task's first build, trim the Go build cache (`go clean -cache` is acceptable; `go clean -fuzzcache` after a fuzz task is expected).
- Fuzzing budget on this host: a fuzz task runs each target it adds or extends with `go test -run '^$' -fuzz '^<Target>$' -fuzztime 30s -parallel 4 <package>` (one target per run, anchored), and quotes each run's last line. A run that finds a failure leaves its input under `<package>/testdata/fuzz/<Target>/`; that file is committed as a regression seed together with the fix (W9-R3). No other file under `testdata/fuzz/` is committed.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml` or `web/pnpm-workspace.yaml`; after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing. The web tooling this plan uses is already locked: vitest-axe, @testing-library/user-event, @playwright/test and (through vitest-axe) axe-core.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`; a task that edits `cmd/darkpanel/` also runs `go test -tags harness -count=1 ./cmd/darkpanel/` and `go vet -tags harness ./cmd/...`. A task that edits `web/` runs `make lint-web test-web`. A task that edits a shell script runs `shellcheck` on it (`make lint-sh` covers `release/*.sh` only; run `shellcheck scripts/fuzz.sh scripts/fuzz.test.sh` directly). Only Task 3 and Task 24 run `make api-docs`; no task runs `make schemas` (no task changes an action schema).
- TDD: write the test, run it and see the expected RED (a build failure naming the first undefined identifier, a failed assertion, or for a fuzz target over existing code the inventory check of Contracts "Fuzz runner" naming the missing target), implement, run GREEN. Quote the RED and GREEN output in the report verbatim, not a paraphrase. A task that skips a line of the full-suite constraint above says so and why in its report.
- Frozen files (spec 1.12), which no task edits except where a task's **Files:** block names the file: `internal/core/**`, `internal/platform/*.go` (the package's own non-test files), `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`, `internal/modules/modules.go`, `internal/cli/modules.go`, `cmd/darkpanel/paths.go`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/app.tsx`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `test/harness/pins.go`, `schemas/**`. New `*_test.go` files in a frozen package are allowed: a test file is not part of the frozen API. Production code outside these paths changes only where a task's **Files:** block names it. A task that finds a missing code, field or extension point in a frozen file stops and reports it; it does not edit the file.
- File ownership inside wave 9 (spec Parallel decomposition, as the plan's waves refine it): only Task 3 edits `Makefile`; only Tasks 11 and 21 edit `.github/workflows/harness.yml` (in different waves); only Task 1 creates `scripts/fuzz.sh`, `scripts/fuzz.test.sh` and `docs/security/parsers.md` (Task 24 may correct a row of `parsers.md`); only Task 24 creates `.github/workflows/fuzz.yml`, once every target exists, so the workflow's `inventory` job is never red on a merged wave; `.github/workflows/ci.yml` is not edited (W9-R4).
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; no TCP listener except `httptest` on `127.0.0.1` port 0; no package-level mutable state written by tests; a fuzz target keeps no state between inputs. Vitest tests stub `fetch` through `stubFetch` and never reach a network. Task 22's Playwright run starts its own Vite server on `127.0.0.1` with `strictPort: false` and answers every `/api/**` request from in-test fixtures (`page.route`), so no two worktrees share a port or a backend. Harness tests derive VM names, work directories and address ranges from the test name (`NewLab`) and respect the `-vms` limit. Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new Go test identifier and every new unexported helper in a test file carries its task's prefix from Contracts "Test prefixes"; fuzz targets are named exactly as Contracts "Fuzz targets" names them. Web test files are named after their component (`<component>.a11y.test.tsx` for the accessibility tests).
- Code style: match the surrounding code; a doc comment on every exported identifier and on every fuzz target, stating the property it checks; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean; eslint `--max-warnings 0` and `tsc --noEmit` clean in `web/`. Error strings start with the package name. A fuzz target asserts a property beyond "does not panic" wherever Contracts "Fuzz targets" names one, and fails with `t.Fatalf` quoting at most the first 64 bytes of the input with `%q`.
- Documentation style (Tasks 16–19, 23): English, plain Markdown (ATX headings, one `#` per file, fenced code blocks tagged `sh`, `json` or `text`, no HTML), sentence-case headings, second person, present tense, commands exactly as the CLI prints them (`sudo darkpanel …`). A page describes what exists on `main`; it never promises future work and never names a plan, wave, task or register row. Every relative link resolves to a file in the repository.
- Secrets: no log line, error, fuzz failure message, document example or screenshot carries a real token, password, TOTP secret, passphrase, tunnel token or private key; examples use obvious placeholders (`<token-file>`, `example.com`, `203.0.113.10`).
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 11 and 21 touch `test/harness/` and `.github/workflows/harness.yml`, and Task 4 touches `internal/update/`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. Dispatch `fuzz.yml` on the branch as well (`gh workflow run fuzz.yml --ref <branch>`) and record its conclusion. No task creates or pushes a tag, and no task runs `release.yml`.

## Contracts

- **Test prefixes.** Task 1 `fz_` (Bash test functions in `scripts/fuzz.test.sh`); Task 3 `Ad` (`internal/apidocs`); Task 12 `Dc` (`internal/docs` helpers); Task 4 `Sd` (`cmd/darkpanel`) and `Rc` (`internal/update`); Tasks 5–10 add only fuzz targets, regression tests `TestFz<Task>*` and helpers `fz<Task>*` (for example `fz5Seeds`); Task 10's regression test for the Retry-After fix is `TestRaRetryAfterOverflow`; Task 11 `Upg` (`test/harness`: `TestUpgradeMatrix`, unit tests `TestUpgUnit*`, workflow tests `TestUpgwUnit*`); Tasks 16–19: `Du` (Task 16), `Dm` (Task 17), `Dn` (Task 18), `Dg` (Task 19) in `internal/docs`; Task 21 `Fp` (`test/harness`: `TestFootprint`, `TestFpUnit*`, `TestFpwUnit*`); Task 23 `accessibility-doc` (web test file name); Task 24 `Wc` (`internal/docs`).

- **Inventory format** (Task 1, `docs/security/parsers.md`). One `#` title, an introduction, then exactly these two sections in this order:

  ```text
  ## Fuzzed parsers

  | Parser | Input | Package | Target | Tags |
  |---|---|---|---|---|
  | `netfilter.Parse` | `*-save` output of iptables and ip6tables | `./internal/platform/netfilter` | `FuzzParse` | - |
  ...

  ## Not fuzzed

  | Parser | Input | Reason |
  |---|---|---|
  ...
  ```

  `Package` is a `./`-relative Go package path; `Target` a fuzz function name; `Tags` `-` or a comma-separated build-tag list (`harness`). Several rows may name the same `(Package, Target)`. Every row of Contracts "Fuzz targets" is one or more rows of `## Fuzzed parsers`; `## Not fuzzed` holds the parsers of Assumptions "Not fuzzed", each with its reason (W9-R1).

- **Fuzz runner** (Task 1, `scripts/fuzz.sh`, Bash, `set -euo pipefail`, run from the repository root):

  ```text
  scripts/fuzz.sh [--check] [--list] [--time <go duration>] [--shard <k>/<n>] [--inventory <path>]
  ```

  - It reads the `## Fuzzed parsers` table of `--inventory` (default `docs/security/parsers.md`) and builds the distinct `(package, target, tags)` triples in first-appearance order.
  - `--check`: for every triple, requires a line matching `^func <target>\(f \*testing\.F\)` in a `*_test.go` file directly in the package directory, and for every `^func Fuzz[A-Za-z0-9_]*\(f \*testing\.F\)` in any `*_test.go` under `cmd/`, `internal/`, `release/` and `test/`, requires a triple naming it and its package; prints one line per problem, `fuzz: <package>: no target <target>` or `fuzz: <package>: <target> is not in the inventory`, and exits 1 when any problem was printed, else prints `fuzz: inventory ok (<n> targets)` and exits 0.
  - `--list`: prints `<package> <target> <tags>` per triple of this shard and exits 0.
  - Otherwise it runs `--check` first, then for each triple of this shard `go test -run '^$' -fuzz '^<target>$' -fuzztime <time> [-tags <tags>] <package>` (default `--time 30s`), continues after a failure, prints `fuzz: FAIL <package> <target>` per failed run and exits 1 when any run failed.
  - `--shard k/n` (1 ≤ k ≤ n) keeps the triples whose 0-based index `i` satisfies `i % n == k-1`; default `1/1`.
  - Exit 2 with a `fuzz: usage:` line on an unknown option or a malformed `--shard`, `--time` or missing inventory.

- **Fuzz workflow** (Task 24, `.github/workflows/fuzz.yml`): `on: push` (every branch), `schedule` (`cron: "41 2 * * *"`) and `workflow_dispatch`; `permissions: contents: read`; `env: GOTOOLCHAIN: local`; runs on `ubuntu-24.04` with `actions/checkout` and `actions/setup-go` pinned by the same SHAs `ci.yml` uses. Job `inventory`: `bash scripts/fuzz.test.sh` and `bash scripts/fuzz.sh --check`. Job `fuzz` (needs `inventory`): a `strategy.matrix.shard` of `[1, 2, 3, 4]` on push and dispatch, running `bash scripts/fuzz.sh --time 30s --shard <shard>/4`, `timeout-minutes: 60`; on `schedule` the matrix is `[1..12]` with `--time 10m --shard <shard>/12` and `timeout-minutes: 300` (W9-R2); on failure it uploads `**/testdata/fuzz/**` as an artifact.

- **Fuzz targets.** Package, target, tags, the parsers it covers, and the property it asserts besides not panicking. Existing targets keep their names; "extend" means the target gains the seeds or the property named here.

  | Task | Package | Target | Tags | Parsers | Property |
  |---|---|---|---|---|---|
  | 5 | `./internal/platform/netfilter` | `FuzzParse` (extend) | - | `Parse`, `Key` | seeds every `.save` file under `internal/platform/netfilter/testdata/`, `internal/modules/firewall/dockerfw/testdata/capture/` and `internal/modules/firewall/eval/testdata/`; `Key` of every parsed rule does not panic |
  | 5 | `./internal/platform/netfilter` | `FuzzParseRestore` (existing) | - | `ParseRestore` | existing |
  | 5 | `./internal/platform/netstat` | `FuzzParseSocketLine` | - | `parseEntry`, `parseAddrPort` (`/proc/net/{tcp,udp}{,6}`) | an accepted entry's address family matches the requested family |
  | 5 | `./internal/platform/netstat` | `FuzzParseNetlink` | - | `parseNetlink`, `parseRoute` (rtnetlink) | every returned message is no longer than the input; an accepted route's prefix length equals the header's `rtm_dst_len` |
  | 5 | `./internal/platform` | `FuzzMarkedBlocks` | - | `scanMarkers`, `replaceBlock`, `removeBlock`, `splitLines` | joining `splitLines(c)` gives `c`; `replaceBlock` is idempotent and its output passes `scanMarkers`; removing a block just added to a file without one gives the file back |
  | 5 | `./internal/platform/dockerapi` | `FuzzLogStream` | - | `(*LogStream).Next` (multiplexed frames) | no frame is longer than the stream's frame limit |
  | 5 | `./internal/platform/dockerapi` | `FuzzCompareVersions` | - | `parseVersion`, `compareVersions` | `compareVersions(a,b) == -compareVersions(b,a)` when both parse |
  | 5 | `./internal/platform/tslocal` | `FuzzPrefsJSON` | - | `(*Prefs).UnmarshalJSON`, `MarshalJSON` | unmarshal, marshal, unmarshal again gives equal modelled fields |
  | 6 | `./internal/modules/docker/compose` | `FuzzParse` | - | `Parse` (`docker compose config --format json`) | input over `MaxConfigBytes` is refused; `Validate` over an accepted config does not panic |
  | 6 | `./internal/modules/docker` | `FuzzContainerDetail` | - | `containerDetailOf`, `envVars`, `redactArgs`, `redactLabels` (inspect JSON) | with redaction on, no environment value, no argument after the first and no label value outside the shown labels appears in the output |
  | 6 | `./internal/modules/docker` | `FuzzParseAPIVersion` | - | `parseAPIVersion` | when it parses, the result's major and minor are non-negative |
  | 6 | `./internal/modules/docker/logfilter` | `FuzzFilterChunking` | - | `(*Filter).Push`, `Flush` (log bytes) | the output does not depend on where the input is split; no registered secret appears in the output |
  | 7 | `./internal/modules/firewall/model` | `FuzzParseTuple` (existing) | - | `ParseTuple` (`### tuple ###` lines) | existing |
  | 7 | `./internal/modules/firewall/model` | `FuzzParseTag` | - | `ParseTag` (UFW rule comments) | when it parses, `Tag(id, label)` gives the input back |
  | 7 | `./internal/modules/firewall/conf` | `FuzzReadFiles` (existing) | - | `Read`, `parseShell`, `parseSysctl` (`ufw.conf`, `/etc/default/ufw`, `sysctl.conf`) | existing |
  | 7 | `./internal/modules/firewall/conf` | `FuzzConfiguredIPv6` | - | `ConfiguredIPv6`, `SysctlKey` | `SysctlKey` is idempotent |
  | 7 | `./internal/modules/firewall/loaded` | `FuzzParseNFTTable` (existing) | - | `parseNFTTables`, `parseNFTTable` | existing |
  | 7 | `./internal/modules/firewall/loaded` | `FuzzToolVariant` | - | `toolVariant` (`iptables --version`, over a fake `core.Runner`) | when ok, the variant is `nft` or `legacy` |
  | 7 | `./internal/modules/firewall` | `FuzzInstalledVersion` | - | `installedVersion`, `upstreamAtLeast` (`/var/lib/dpkg/status`) | when ok, the version is non-empty |
  | 7 | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - | `BlockOf`, `WithBlock`, `WithoutBlock` (marked block in `after.rules`, `after6.rules`) | `WithBlock` is idempotent; `BlockOf(WithBlock(f, b))` gives `b`; `WithoutBlock(WithBlock(f, b))` gives `f` when `f` had no block |
  | 7 | `./internal/modules/firewall/predict` | `FuzzEdit` (existing) | - | `Edit`, `parseVector` | existing |
  | 7 | `./internal/modules/firewall/predict` | `FuzzSetDefault` | - | `ufwDefaults`, `setDefault` | `setDefault` is idempotent and `ufwDefaults` reads back the value it set |
  | 7 | `./internal/modules/firewall/eval` | `FuzzEvaluate` | - | `Evaluate`, `Unmodelled` over any ruleset `netfilter.Parse` accepts | does not panic for a fixed packet set |
  | 8 | `./internal/modules/tailscale` | `FuzzReadOSRelease` | - | `readOSRelease` (`/etc/os-release`) | does not panic |
  | 8 | `./internal/modules/tailscale` | `FuzzDpkgStatus` | - | `installedVersion` (`/var/lib/dpkg/status`, over a fake reader) | a non-empty result only for a `Package: tailscale` stanza whose status ends in `installed` |
  | 8 | `./internal/modules/tailscale` | `FuzzKeyringFingerprints` | - | `keyringFingerprints` (OpenPGP keyring) | every fingerprint is 40 upper-case hex characters |
  | 8 | `./internal/modules/tailscale/stun` | `FuzzParse` | - | `Parse`, `decodeAddress` (STUN replies) | a binding request built by `Request` never parses as a success |
  | 8 | `./internal/modules/tailnet/policy` | `FuzzApply` | - | `Apply`, `pointerTokens` (HuJSON patching) | output is nil on error; applying the same edits twice changes nothing; output re-parses as HuJSON |
  | 8 | `./internal/modules/tailnet/policy` | `FuzzCanonical` | - | `Canonical`, `Diff` | `Canonical` is idempotent and valid JSON; `Diff(a, a)` has no changed lines |
  | 8 | `./internal/modules/derp` | `FuzzParseDERPMap` | - | `parseDERPMap` | every key equals its region id in decimal |
  | 8 | `./internal/modules/derp/artifact` | `FuzzParseManifest` | - | `ParseManifest` (derper manifest) | `ParseManifest(m.Encode())` gives `m` |
  | 8 | `./internal/modules/derp/server` | `FuzzParseCert` | - | `ParseCert` (certificate PEM) | the error text never contains the input |
  | 8 | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - | `(*Config).UnmarshalJSON`, `(*Rule).UnmarshalJSON`, `Hash` | round trip is deep-equal and `Hash` is stable |
  | 8 | `./internal/modules/cloudflare` | `FuzzServiceHost` | - | `serviceHost`, `legacyIPv4`, `serviceProblem` (tunnel service URL) | a canonical dotted quad gives the same address as `netip.ParseAddr` |
  | 8 | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - | `CheckUpstream`, `parseNginxIPv4`, `parseInetAton` (forward host) | the reading that counts is the first that succeeds, in `upstreamAddr`'s order; a host whose counting reading is loopback or unspecified, or that is a loopback name, is refused unless the caller is `npm.admin.expose` |
  | 9 | `./internal/update` | `FuzzParseVersion` | - | `ParseVersion`, `Compare` (release index) | `ParseVersion(v.String())` gives `v`; `Compare` is antisymmetric |
  | 9 | `./internal/update` | `FuzzChecksumFor` | - | `checksumFor` (`checksums.txt`) | a result is 64 lower-case hex characters |
  | 9 | `./internal/update` | `FuzzExtractBinary` | - | `extractBinary` (release tarball, written to `t.TempDir()`) | at most one file is written, at the destination |
  | 9 | `./release/releasetool` | `FuzzParseVersion` | - | `parseVersion`, `parseTag`, `compare` | every version it accepts `update.ParseVersion` accepts, with the same comparison sign |
  | 9 | `./release/releasetool` | `FuzzKeyID` | - | `keyID` (minisign signature) | a result is 16 upper-case hex characters |
  | 9 | `./internal/transport` | `FuzzParseBootstrap` (existing) | - | `ParseBootstrap` (enrollment bundle) | existing |
  | 9 | `./internal/transport` | `FuzzParseToken` | - | `ParseToken` | `ParseToken(t.String())` gives `t` |
  | 9 | `./test/harness` | `FuzzParsePorts` | `harness` | `ParsePorts`, `m3dParsePortEntry` (`nmap -oG -`) | ports are 1–65535, protocol `tcp` or `udp`, state non-empty |
  | 9 | `./internal/modules/hostinfo` | `FuzzParseOSRelease`, `FuzzParseCPUInfo`, `FuzzParseMemInfo`, `FuzzParseLoadAvg`, `FuzzParseUptime` (extend) | - | `parseOSRelease`, `parseCPUInfo`, `parseMemInfo`, `parseLoadAvg`, `parseUptime` | seeds every file under `internal/modules/hostinfo/testdata/` |
  | 10 | `./internal/controller/backup` | `FuzzReadHeader` | - | `formatReadHeader`, `formatEncodeHeader` (`.dpbk` header) | an accepted header passes `Params.Validate` and re-encodes to the same bytes |
  | 10 | `./internal/controller/backup` | `FuzzFrames` | - | `frameNewReader`, `formatReadMembers` (`.dpbk` frames, fixed test key) | input the frame writer did not produce fails with `ErrAuth`, `ErrTruncated` or `ErrMembers` |
  | 10 | `./internal/controller/backup` | `FuzzDecodeJournal` | - | `decodeJournal`, `checkJournal` (restore journal) | an accepted journal has format 1 and a known phase |
  | 10 | `./internal/core/secret` | `FuzzOpen` | - | `(*Sealer).Open` (sealed values) | input `Seal` did not produce never opens |
  | 10 | `./internal/core/secret` | `FuzzRedactJSON` | - | `RedactJSON` (audit and journal redaction) | output is valid JSON and no value the schema marks secret appears in it |
  | 10 | `./internal/controller/cloud` | `FuzzRetryAfter` | - | `(*Client).retryAfter` (`Retry-After` header) | every integer of seconds above 60 gives more than `maxRetryAfter`; the result is never negative |
  | 10 | `./internal/controller/events` | `FuzzParseID` | - | `ParseID` (`Last-Event-ID` header) | `ParseID(FormatID(e, s))` gives `e, s` |
  | 10 | `./internal/cli` | `FuzzPrintEvents` | - | `printEvents` (controller SSE) | the output holds no C0 control other than `\n` and no C1 or bidirectional control |
  | 10 | `./internal/install` | `FuzzCheckAgentAddress` | - | `CheckAgentAddress` (unit-file flag) | an accepted value has no whitespace, quote, `%` or `$` |

- **Accessibility helpers** (Task 2, `web/src/test/a11y.ts`):

  ```ts
  import type { UserEvent } from "@testing-library/user-event"
  // Runs axe on el and fails with the rule ids and targets of every violation.
  export async function expectNoAxeViolations(el: Element): Promise<void>
  // Presses Tab (or Shift+Tab when backwards) count times and returns the
  // accessible name of document.activeElement after each press ("" when none).
  export async function tabSequence(user: UserEvent, count: number, opts?: { backwards?: boolean }): Promise<string[]>
  // The accessible name of document.activeElement, "" when it is body.
  export function focusedName(): string
  ```

  The accessible name is the one `@testing-library/dom`'s role queries compute (`getRoles` and the `name` option of `queryAllByRole`); `@testing-library/dom` exports no `computeAccessibleName`.

- **Page title** (Task 2, `web/src/lib/page-title.ts`):

  ```ts
  // Sets document.title to "<title> · Darkpanel" (or "Darkpanel" for "") while
  // the calling component is mounted, and restores the previous title after.
  export function usePageTitle(title: string): void
  ```

  Pages that call it: login (`Sign in`), hosts (`Hosts`), host (`<host name> · <tab label>`), change (`Change <short id>`), audit (`Audit log`), tokens (`API tokens`), and every global page entry of a module (its `label`) — Tasks 13-15 and 20 add the calls in the files they own.

- **API reference** (Task 3, package `internal/apidocs`, `internal/apidocs/apidocsgen`):

  ```go
  // Generate renders the OpenAPI document of reg and routes and the Markdown
  // reference built from it, keyed by slash path relative to docs/api.
  func Generate(reg *core.Registry, routes []schema.Route) (map[string][]byte, error)
  ```

  Keys: `openapi.json` (the `schema.OpenAPI` document re-indented with two spaces and a final newline), `README.md` (the index: authentication, the common `Plan`, `Change`, `ApplyRequest` and `Problem` components, the hand-written routes, and one link per module page) and `<module>.md` for `core` and every module of `reg.Modules()` that has an operation (one `##` section per action or resource in operation-id order, with its method and path, summary, minimum scope, and input and output tables rendered from the component schemas). Output is byte-for-byte deterministic. `go run ./internal/apidocs/apidocsgen -out docs/api` writes every key and deletes any other `.md` or `.json` file directly in the directory. `make api-docs` runs exactly that. The package also exports `func Write(dir string, files map[string][]byte) error`, which writes the files and removes the stale ones, for `apidocsgen` and its test. `TestAdAPIDocsCurrent` (`internal/apidocs/apidocs_test.go`) calls `Generate(registry.Build(), api.Routes())` and fails, naming the file and `run make api-docs`, when a file under `docs/api/` differs, is missing or is not a key.

- **Docs checks** (Task 12, test-only package `internal/docs`, file `internal/docs/helpers_test.go`, package `docs_test`):

  ```go
  // dcRoot is the repository root, found from the test's working directory.
  func dcRoot(t *testing.T) string
  // dcRead returns the file at the slash path rel under the root, failing when it is missing.
  func dcRead(t *testing.T, rel string) string
  // dcMentions fails once per name that the file at rel does not contain verbatim, naming both.
  func dcMentions(t *testing.T, rel string, names ...string)
  // dcModuleNames returns the sorted names of every action and resource of module id in registry.Build() whose Spec is not Internal.
  func dcModuleNames(t *testing.T, id string) []string
  // dcLinks fails once per relative Markdown link in the file at rel whose target file does not exist.
  func dcLinks(t *testing.T, rel string)
  ```

- **Documentation paths** (Tasks 16–19): `docs/user/README.md` (index linking every page below), `docs/user/install.md`, `docs/user/first-admin.md`, `docs/user/lockout-recovery.md`, `docs/user/uninstall.md`, `docs/user/administration.md` (the `core` actions: users, tokens, hosts, modules, audit, updates and rollouts, backup and restore), `docs/user/modules/<id>.md` for `hostinfo`, `firewall`, `docker`, `tailscale`, `tailnet` (Task 17) and `derp`, `npm`, `cloudflare`, `panel` (Task 18), `docs/dev/module-guide.md` (Task 19). Task 16 writes `docs/user/README.md` with links to all of them. Each docs task runs `dcLinks` only on the pages it writes, except that Task 16 does not run it on `docs/user/README.md` (it links pages Tasks 17 and 18 write in the same wave); Task 24's `TestWcDocsLinks` runs `dcLinks` on every Markdown file under `docs/user/`, `docs/dev/`, `docs/api/` and on `docs/accessibility.md`.

- **Harness state directory** (Task 4, `cmd/darkpanel/paths_harness.go`, `//go:build harness`): an `init` sets `addRootFlags` to register the persistent string flag `--state-dir` (help `harness only: keep every path under this directory`) bound to the package variable `harnessStateDir`, and sets `selectPaths` to return `paths.Under(harnessStateDir)` whenever `harnessStateDir != ""` — for a nil command too, which is the update rollback fallback — and `paths.Resolve(dev)` otherwise; with `--state-dir` and `--dev` both set it returns the error `--state-dir and --dev cannot be combined` (the CLI prints it after its `darkpanel: ` prefix). A build without the `harness` tag has no `--state-dir` flag.

- **Upgrade matrix** (Task 11): `var upgFlag = flag.Bool("upg", false, "run TestUpgradeMatrix; needs -os and -m2, and is a go test run of its own")`. Sources file `test/harness/upgrade_sources.txt`: `#` comments and blank lines ignored, else one line per source `<version> <commit>` (a semantic version without `v`, a 40-character lower-case commit id), unique versions, ascending; its first line is `1.0.0 <commit>` (W9-R7). Builds under `bin/harness/`: `darkpanel-<source version>` per source (from that commit, tags `harnesskeys`), `darkpanel-1.1.0` (tags `harnesskeys`) and `darkpanel-1.1.1` (tags `harnesskeys,harnessbroken`) from the commit under test. Job `vm-upg` in `harness.yml` with the four-OS matrix, step `go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/ -args -os <os> -upg -bin … -m2 … -commit … -workdir "$RUNNER_TEMP/harness-upg" -cache …`, `timeout-minutes: 90`.

- **Footprint** (Task 21): `var fpFlag = flag.Bool("fp", false, "run TestFootprint; needs -os and -m2, and is a go test run of its own")`; constants `fpAgentMax = 40_000_000` and `fpControllerMax = 150_000_000` (bytes, W9-R9), `fpWarmup = 5 * time.Minute`, `fpWindow = 10 * time.Minute`, `fpSampleEvery = 10 * time.Second`, `fpHostsEvery = 15 * time.Second`, `fpExtraAgents = 9`. The extra-agent binary `bin/harness/darkpanel-<versionN>-state` is built by the `build` job with tags `harnesskeys,harness` and `-X github.com/darkraise/darkpanel/internal/buildinfo.Version=<versionN>` (`versionN` is `0.2.0`, `test/harness/m2_test.go`). Job `vm-fp` with the four-OS matrix, step `go test -tags harness -timeout 60m -v -run '^TestFootprint$' ./test/harness/ -args -os <os> -fp …`, `timeout-minutes: 80`. Task 21 also adds `go test -tags harness ./cmd/darkpanel/` to the `build` job's harness unit tests step.

- **Conformance record** (Task 23, `docs/accessibility.md`): one `#` title; `## Scope` naming the six core flows of spec section 9; `## Criteria`, one table per flow with columns `Criterion | Level | Method | Evidence`, where Method is `automated` or `manual` and Evidence is a repository-relative path (a `*.a11y.test.tsx` file, the Playwright file, or a screenshot under `docs/accessibility/`); `## Known issues` (none open at AA when the plan ends, else each with its criterion). Screenshots from Task 22 live in `docs/accessibility/<flow>-<theme>-<check>.png`.

## Assumptions (evidence)

All checked on 2026-11-05 at `3c5e837` unless stated.

- Existing fuzz targets (12): `FuzzParseBootstrap` (`internal/transport/token_fuzz_test.go`), `FuzzParse` (`internal/platform/netfilter/parse_fuzz_test.go`), `FuzzParseRestore` (`internal/platform/netfilter/restore_test.go`), `FuzzParseOSRelease`, `FuzzParseCPUInfo`, `FuzzParseMemInfo`, `FuzzParseLoadAvg`, `FuzzParseUptime` (`internal/modules/hostinfo/parse_test.go`), `FuzzParseTuple` (`internal/modules/firewall/model/tuple_test.go`), `FuzzReadFiles` (`internal/modules/firewall/conf/conf_test.go`), `FuzzParseNFTTable` (`internal/modules/firewall/loaded/nft_test.go`), `FuzzEdit` (`internal/modules/firewall/predict/edit_test.go`) — `rg -l "func Fuzz"` on 2026-11-05.
- `go test -fuzz` takes one package and a pattern that must match exactly one target, so the runner anchors every pattern (`^FuzzParse$` would otherwise also match `FuzzParseRestore`) — https://go.dev/doc/security/fuzz/ ("-fuzz … must match exactly one fuzz test").
- `Makefile` already has a `fuzz` target that runs `bash scripts/fuzz.sh` and fails when the script is missing (`Makefile`, target `fuzz`), so no Makefile change is needed for 9a.
- `internal/controller/cloud/client.go:241-242` (`retryAfter`) computes `time.Duration(s)*time.Second` from `strconv.Atoi(v)` without a bound, so a `Retry-After` of 9223372037 or more overflows to a negative duration that `max(…, time.Nanosecond)` turns into 1 ns, and `wait > maxRetryAfter` (`:145`) lets the client retry at once instead of refusing. Task 10's `FuzzRetryAfter` and `TestRaRetryAfterOverflow` pin it and Task 10 fixes it (W9-R3); the file is not frozen.
- `internal/modules/firewall/loaded` `toolVariant` reads the first line of `iptables --version` without `strings.TrimSpace`, while the module's `checkTools` (`internal/modules/firewall/preflight.go`) trims first, so output with a leading blank line is read differently by the two: an adjacent finding, recorded by Task 24 as a register row, not fixed here (Task 7's `FuzzToolVariant` asserts only `toolVariant`'s own property). Likewise the read-only clipboard inputs of `web/src/pages/create-token-dialog.tsx` and `web/src/modules/tailnet/keys-page.tsx` lack a label (drafting audit, 2026-11-05); they are outside the core flows, so Task 24 records them as a row too, together with two findings of drafting runs: `policy.Apply` does not converge on a document that names a member twice (Task 8's `FuzzApply` skips such documents), and the audit log's "Load older entries" button drops the focus to the body when the last page loads (Task 13 leaves it). Drafting runs also found defects the tasks fix and pin: `releasetool` orders numeric pre-release identifiers above `uint64` by text, unlike `update.Version.Compare` (Task 9); the CLI's `jsonSafe` lets DEL (U+007F) reach the terminal (Task 10); heading-order and label violations in the core pages (Tasks 13, 14); a firewall status refresh remounting the open change dialog (Task 15).
- The OpenAPI document is built offline and deterministically by `schema.OpenAPI(reg, routes)` (`internal/core/schema/openapi.go:93`, `TestOpenAPIDeterministic`) from `registry.Build()` (`internal/registry/registry.go:29`) and `api.Routes()` (`internal/controller/api/routes.go:72`); the server serves the same document plus web-surface extension routes, of which none exist (`internal/controller/backup/contribute.go` contributes socket routes only). No `openapi.json` is committed today. A test in `internal/core/schema` cannot import `internal/registry` (import cycle), hence the new `internal/apidocs` package (W9-R4).
- `make schemas` regenerates `schemas/` and `TestSnapshotsCurrent` (`internal/modules/modules_test.go:67`) fails when it is stale; `TestAdAPIDocsCurrent` follows the same pattern, so a later schema change needs `make schemas api-docs`.
- Web: axe already runs in about 56 test files through `expect(await axe(el)).toHaveNoViolations()` with `vitest-axe/extend-expect` in `web/src/test/setup.ts`; none of `pages/login.test.tsx`, `pages/hosts.test.tsx`, `pages/host.test.tsx`, `pages/create-host-dialog.test.tsx`, `pages/change.test.tsx` runs axe; no test checks focus order or focus return. Dialogs come from `darkraise-ui` 6.9.6 (`DialogContent` uses `useFocusTrap(ref, { loop: true, restoreFocus: true })`, which restores focus only to an element still in the document); the shell's `SidebarLayout` provides a skip link, landmarks and route focus; nothing sets `document.title` (`web/index.html` has `<title>Darkpanel</title>` and `lang="en"`). axe in jsdom does not check colour contrast.
- `@playwright/test` 1.64.0 and `axe-core` 4.14.0 are installed (`web/node_modules/.pnpm/`), Chromium is cached in `~/.cache/ms-playwright`, no `playwright.config.*` exists, and `axe-core` is not resolvable from `web/` directly (only through `vitest-axe`'s own `node_modules`): `createRequire(require.resolve("vitest-axe")).resolve("axe-core/axe.min.js")` finds it (drafting run of 2026-11-05 in a scratch copy). Playwright 1.64 pins Chromium build 1248, while `~/.cache/ms-playwright` holds builds 1217–1243 only, so the default launch fails here; `web/playwright.config.ts` reads an `E2E_CHROMIUM` executable path, and Task 22's first step verifies the launch with the cached 1243 build (CI installs the pinned build with `playwright install chromium` if the pass ever runs there). darkraise-ui writes its colour tokens as inline styles on `<html>`, so contrast overrides in `globals.css` need `!important` (drafting run, 2026-11-05). On `3c5e837` the drafted browser pass fails 27 of 88 checks (text and field-border contrast, the change dialog overflowing at 320 px); the CSS overrides and one `min-w-0` class on the change dialog make all 88 pass. Vitest's default include matches `*.spec.ts` and `*.test.ts`, and ESLint lints every `**/*.{ts,tsx,js}` in `web/` (`web/eslint.config.js`), hence the Playwright file name `web/e2e/a11y.pw.ts` (W9-R6).
- Harness: the M2 update scenarios use `versionN = 0.2.0` … `versionMigration = 0.3.4` (`test/harness/m2_test.go`), `serveReleases` publishes `m2Versions` only (`m2_test.go:177`), `scenarioInstall` installs `versionN` (`:231`) and `failedUpdate` ends at `requireFleetVersion(versionGood)` (`m2_update_test.go:198`), so Task 11 adds version-parameterised forms. The updater orders pre-releases by semver 2.0 §11 (`internal/update/version.go`, `comparePrerelease`) and refuses a cross-major step (`Steps`); `version_test.go` has no `rc.2` against `rc.10` row and no `1.0.0-rc.1` → `1.0.0` step. `make harness-builds HARNESS_BUILDS=… HARNESS_DIR=…` overrides both variables from the command line. No git tag exists, so the only upgrade source is the baseline.
- `selectPaths` call sites: `serve` (`cmd/darkpanel/main.go:180`), `agent run`, `enroll`, `check`, `task` (`agent.go:246`, `:302`, `:380`, `:416`), `withUpdater` (`update.go:58`) and the rollback fallback with a nil command (`update.go:229`); `cmd/darkpanel/paths_test.go` swaps the hooks through `psSwap`, but no test pins which command each call site passes (register row 107). `paths.Under(root)` moves every path of `paths.Paths` under one directory (`internal/paths/paths.go:83`). `.golangci.yml` lints with the `harness` build tag.
- Every module is `DefaultEnabled: true`; `core.module.disable/v1` takes `coreactions.ModuleInput{HostID, ModuleID}` (`internal/controller/coreactions/enablement.go`); the only agent background task is `firewall.reconcile` (`internal/modules/firewall/reconcile.go`). That nine extra agents on the agent VM can enroll under `--state-dir`, run as `systemd-run` units, and have every module but `hostinfo` disabled before their firewall preflight starts a reconcile: unverified — Task 21 verifies it in its first harness step.
- **Not fuzzed** (each goes in the `## Not fuzzed` table with this reason): typed `encoding/json` decoding of provider API responses (Docker, Tailscale, Cloudflare, NPM) and of the agent's mTLS peer — stdlib decoding into typed structs, no hand-written parser; `systemd` state through D-Bus — typed D-Bus decoding; Darkpanel's own database rows and root-owned state (`auth.parsePHC`, firewall journal state, `update.ReadRecord`, `ValidateStaged`) — written only by Darkpanel; the update mirror's release index (`releases.json`) — typed `encoding/json` decoding after a size bound and a format-version check; the minisign signatures that `update.VerifyChecksums` checks — parsed by the third-party `aead.dev/minisign`; `backup.Read` as a whole — every input runs Argon2id at 64 MiB or more, so its header and frames are fuzzed separately; `netstat` process ownership through `/proc/<pid>/fd` — a file walk without a text grammar; trivial single-call readers (`held`, `lastLine`, `legacyTables`, `readRPFilter`, `readSysctl`) — no grammar beyond `strings.TrimSpace` or `strconv.Atoi`; repository-owned inputs (`LoadPins` of `images.json`, CLI flags parsed by cobra).
- Scoring (ladder.md): Files counts file shapes as the v1 wave 7 and wave 8 plans did, pairing a test file with the file it tests and counting test files that apply one given pattern (the fuzz targets, the `*.a11y.test.tsx` files) as one shape; a generated output counts with the command that writes it. The fuzz tasks score spec 1 because their targets are given whole and only the named fixes are prose. Rule S split two drafted tasks: the fuzz workflow moved from Task 1 to Task 24, and the docs-check helpers moved from Task 3 to Task 12.
- No task carries an **Executor:** line: codex is off by setting, and the agy offer of 2026-11-05 got no answer within 15 minutes, so the plan is Claude-only.

**Plan rulings** (Task 24 copies them into spec section 9):

- **W9-R1 inventory.** `docs/security/parsers.md` lists every parser of host-tool output, config files and untrusted input under `## Fuzzed parsers`, each with its target, and the parsers that are not fuzzed under `## Not fuzzed`, each with a reason; the check fails when a fuzzed row has no target or a target has no row.
- **W9-R2 budgets.** Every push runs each target for 30 s in four shards; the nightly run gives each target 10 minutes in twelve shards, so no job exceeds GitHub's six-hour limit; both run on GitHub-hosted `ubuntu-24.04`.
- **W9-R3 findings.** A fuzz target that finds a defect in a non-frozen file is fixed in the same task with the failing input committed as a regression seed and a named unit test; in a frozen file the task stops and the integrator fixes it on `main`. The `retryAfter` overflow is fixed by bounding the parsed seconds before converting them, so any value above 60 seconds is refused as longer than `maxRetryAfter`.
- **W9-R4 API reference.** `internal/apidocs` renders `docs/api/openapi.json` and one Markdown page per module from `schema.OpenAPI(registry.Build(), api.Routes())`; `make api-docs` writes it; the CI check is `TestAdAPIDocsCurrent` in the existing `go` job, so `ci.yml` does not change.
- **W9-R5 documentation checks.** `internal/docs` (tests only) reads the registry and the CLI facts the pages must name — every non-internal action and resource of each module on its page, every flag of `darkpanel uninstall` and every declared module uninstall option, the break-glass user commands, and the identifiers the module guide teaches — so a page goes stale visibly.
- **W9-R6 accessibility method.** Automated: `*.a11y.test.tsx` files with `expectNoAxeViolations` and keyboard tests through `tabSequence` and `focusedName` on every page and dialog of the core flows; page titles through `usePageTitle`. Manual: `web/e2e/a11y.pw.ts`, run with `pnpm -C web exec playwright test -c playwright.config.ts`, renders the same flows in Chromium against fixtures, runs axe's contrast rules in both themes, checks 200 % zoom and 320 px reflow for horizontal scrolling, reads the change dialog's accessible names, and saves screenshots under `docs/accessibility/`; contrast fixes are CSS variable overrides in `web/src/styles/globals.css`. The first-admin and lockout-recovery pages are Markdown documents, checked manually for heading order and link text.
- **W9-R7 upgrade sources.** `test/harness/upgrade_sources.txt` lists the upgrade sources; its first source is `1.0.0` at the commit wave 9 starts from, `3c5e837d976666d556d0fa1939b7636c67be2a64`, and each later real `v1.*` tag is added as a line. The `build` job builds every source from its commit through a `git worktree` with `fetch-depth: 0`, and the build under test as `1.1.0` and its broken twin as `1.1.1`.
- **W9-R8 upgrade scenarios.** For each source: install controller and agent at the source version, roll out `1.1.0` while `hostinfo.summary` answers on the agent host one minor behind, then roll out `1.1.1`, which rolls back within two minutes and leaves the fleet at `1.1.0`.
- **W9-R9 footprint.** Megabytes are decimal: the agent's `VmRSS` stays at most 40 000 000 bytes and the controller's at most 150 000 000 bytes. The controller manages its own host, the agent VM and nine extra agents on the agent VM (eleven hosts, more than S6's ten); the extra agents run the `harness`-tagged build under `--state-dir` as `systemd-run` units with only `hostinfo` enabled.
- **W9-R10 state directory.** `--state-dir` exists only in `harness` builds, binds to a package variable so the nil-command rollback fallback honours it, and refuses `--dev`.

## Task index

1. fuzz: parser inventory and runner script
2. web: accessibility test helpers and page titles
3. docs: API reference generator
4. cmd: harness --state-dir and selectPaths call sites
5. fuzz: platform clients and parsers
6. fuzz: docker module parsers
7. fuzz: firewall module parsers
8. fuzz: tailscale, tailnet, derp, cloudflare and npm parsers
9. fuzz: update, release, transport, hostinfo and harness parsers
10. fuzz: backup, secrets, cloud client, events, CLI and install parsers
11. harness: TestUpgradeMatrix and the vm-upg job
12. docs: documentation check helpers
13. web a11y: login, hosts, host page and create-host dialog
14. web a11y: change dialog and change pages
15. web a11y: firewall change flow
16. docs: install, first admin, lockout recovery, uninstall and administration
17. docs: module pages for hostinfo, firewall, docker, tailscale and tailnet
18. docs: module pages for derp, npm, cloudflare and panel
19. docs: module developer guide
20. web a11y: container actions and expose flows
21. harness: TestFootprint and the vm-fp job
22. web a11y: browser pass for contrast, zoom and reflow
23. docs: accessibility conformance record
24. wave close: fuzz workflow, inventory check, findings and spec rulings

---

### Task 1: fuzz: parser inventory and runner script

**Files:**
- Create: `docs/security/parsers.md`
- Create: `scripts/fuzz.sh`
- Create: `scripts/fuzz.test.sh`

**Interfaces:**
- Consumes: none. The rows of the inventory come from Contracts "Fuzz targets" and Assumptions "Not fuzzed". Read, not edited: `Makefile` (target `fuzz` already runs `bash scripts/fuzz.sh`), `scripts/check-adrs.sh` and `scripts/check-adrs.test.sh` (the house style of a Bash script and its test: `set -euo pipefail`, awk instead of grep, an `expect` helper, a final `<n> passed, <m> failed` line).
- Produces: Contracts "Inventory format" and "Fuzz runner", and the real inventory: 57 distinct `(package, target, tags)` triples, 12 of which exist on the base. Tasks 5-10 add the other 45 targets under the names the inventory fixes; Task 24 runs `bash scripts/fuzz.sh --check` for real.

**Items:** 38

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

Create `scripts/fuzz.test.sh`. Every case builds its fixture inventory and fixture Go packages under one `mktemp -d` directory and runs the script from there with `--inventory`, so no case depends on the real inventory. The run cases use a fake `go` on `PATH` to pin the arguments, and one case runs a trivial fuzz target for one second with the real `go`.

```bash
#!/usr/bin/env bash
# shellcheck disable=SC2016 # the fixture files hold literal backticks
# Tests for scripts/fuzz.sh. Each case builds its own fixture tree under a
# temporary directory and runs the script from there, so no case depends on
# the repository's real inventory.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
runner="$here/fuzz.sh"
pass=0; failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ ! -f "$runner" ]; then echo "FAIL: $runner does not exist"; exit 1; fi

fz_note() { echo "FAIL $1: $2"; failures=$((failures + 1)); }

# fz_inventory <dir> <row>...: writes <dir>/parsers.md; a row is
# "<package> <target> <tags>" and becomes one table row.
fz_inventory() {
  local dir=$1 row pkg target tags; shift
  {
    printf '# Parsers\n\nIntroduction.\n\n## Fuzzed parsers\n\n'
    printf '| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n'
    for row in "$@"; do
      read -r pkg target tags <<<"$row"
      printf '| `p.%s` | input | `%s` | `%s` | %s |\n' "$target" "$pkg" "$target" "$tags"
    done
    printf '\n## Not fuzzed\n\n| Parser | Input | Reason |\n|---|---|---|\n| `q.Read` | file | not a parser |\n'
  } > "$dir/parsers.md"
}

# fz_package <dir> <package dir> <target>...: writes a test file holding one
# fuzz target per name whose body accepts every input.
fz_package() {
  local dir=$1 pkgdir=$2 target; shift 2
  mkdir -p "$dir/$pkgdir"
  {
    printf 'package %s\n\nimport "testing"\n' "$(basename "$pkgdir")"
    for target in "$@"; do
      printf '\nfunc %s(f *testing.F) {\n\tf.Add([]byte("x"))\n\tf.Fuzz(func(t *testing.T, b []byte) {})\n}\n' "$target"
    done
  } > "$dir/$pkgdir/x_test.go"
}

# fz_tree <name>: makes a fixture directory with a go.mod and prints it.
fz_tree() {
  local dir="$work/$1"
  mkdir -p "$dir"
  printf 'module example.com/fz\n\ngo 1.20\n' > "$dir/go.mod"
  echo "$dir"
}

# fz_fakego <dir>: puts a fake go command in <dir>/bin that logs its
# arguments to <dir>/go.log and fails when they name FuzzBad.
fz_fakego() {
  mkdir -p "$1/bin"
  printf '#!/usr/bin/env bash\necho "$*" >> "%s/go.log"\ncase "$*" in *FuzzBad*) exit 1;; esac\n' "$1" > "$1/bin/go"
  chmod +x "$1/bin/go"
}

# fz_expect <name> <want exit> <output substring or -> <command...>: runs the
# command from the current directory and checks its exit status and output.
fz_expect() {
  local name=$1 want=$2 needle=$3 out rc; shift 3
  out=$("$@" 2>&1); rc=$?
  if [ "$rc" -ne "$want" ]; then fz_note "$name" "exit $rc, want $want; output: $out"; return; fi
  if [ "$needle" != "-" ] && [[ "$out" != *"$needle"* ]]; then fz_note "$name" "output lacks '$needle': $out"; return; fi
  pass=$((pass + 1))
}

# fz_equal <name> <got> <want>
fz_equal() {
  if [ "$2" != "$3" ]; then fz_note "$1" "got [$2], want [$3]"; else pass=$((pass + 1)); fi
}

fz_check_ok() {
  local d; d=$(fz_tree check_ok)
  fz_inventory "$d" "./internal/alpha FuzzAlpha -" "./internal/alpha FuzzAlpha -" "./internal/beta FuzzParse harness" "./internal/beta FuzzParseRestore -"
  fz_package "$d" internal/alpha FuzzAlpha
  fz_package "$d" internal/beta FuzzParse FuzzParseRestore
  cd "$d" || return
  fz_expect check_ok 0 "fuzz: inventory ok (3 targets)" bash "$runner" --check --inventory parsers.md
}

fz_check_missing_target() {
  local d; d=$(fz_tree check_missing)
  fz_inventory "$d" "./internal/alpha FuzzAlpha -" "./internal/beta FuzzParse -" "./internal/gamma FuzzGone -" "./internal/delta FuzzSub -"
  fz_package "$d" internal/alpha FuzzAlpha
  # FuzzParseRestore does not satisfy a row for FuzzParse.
  fz_package "$d" internal/beta FuzzParseRestore
  # A target in a subdirectory does not satisfy a row for the parent package.
  fz_package "$d" internal/delta/sub FuzzSub
  cd "$d" || return
  fz_expect missing_prefix_only 1 "fuzz: ./internal/beta: no target FuzzParse" bash "$runner" --check --inventory parsers.md
  fz_expect missing_no_package 1 "fuzz: ./internal/gamma: no target FuzzGone" bash "$runner" --check --inventory parsers.md
  fz_expect missing_subdirectory 1 "fuzz: ./internal/delta: no target FuzzSub" bash "$runner" --check --inventory parsers.md
  fz_equal missing_lines "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c '^fuzz: .*no target ')" 3
}

fz_check_orphan_target() {
  local d; d=$(fz_tree check_orphan)
  fz_inventory "$d" "./internal/alpha FuzzParse -"
  fz_package "$d" internal/alpha FuzzParse
  # The same name in another package is an orphan: a row names a target and its package.
  fz_package "$d" internal/beta FuzzParse
  fz_package "$d" internal/gamma FuzzOrphan
  fz_package "$d" release/tool FuzzRelease
  fz_package "$d" test/harness FuzzHarness
  fz_package "$d" cmd/tool FuzzCmd
  # Outside cmd, internal, release and test a fuzz target is not the check's business.
  fz_package "$d" web/other FuzzIgnored
  cd "$d" || return
  fz_expect orphan_other_package 1 "fuzz: ./internal/beta: FuzzParse is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_internal 1 "fuzz: ./internal/gamma: FuzzOrphan is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_release 1 "fuzz: ./release/tool: FuzzRelease is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_test 1 "fuzz: ./test/harness: FuzzHarness is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_cmd 1 "fuzz: ./cmd/tool: FuzzCmd is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_equal orphan_lines "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c '^fuzz: ')" 5
  fz_equal orphan_outside_roots "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c 'FuzzIgnored')" 0
}

fz_list_and_shard() {
  local d; d=$(fz_tree list)
  fz_inventory "$d" "./a FuzzA -" "./a FuzzA -" "./b FuzzB harness" "./c FuzzC -" "./d FuzzD -" "./e FuzzE harness,extra"
  cd "$d" || return
  fz_equal list_all "$(bash "$runner" --list --inventory parsers.md)" "$(printf './a FuzzA -\n./b FuzzB harness\n./c FuzzC -\n./d FuzzD -\n./e FuzzE harness,extra')"
  fz_equal list_shard_2_of_3 "$(bash "$runner" --list --shard 2/3 --inventory parsers.md)" "$(printf './b FuzzB harness\n./e FuzzE harness,extra')"
  fz_equal list_shard_1_of_3 "$(bash "$runner" --list --shard 1/3 --inventory parsers.md)" "$(printf './a FuzzA -\n./d FuzzD -')"
  fz_equal list_shard_3_of_3 "$(bash "$runner" --list --shard 3/3 --inventory parsers.md)" "./c FuzzC -"
  fz_equal list_shard_1_of_1 "$(bash "$runner" --list --shard 1/1 --inventory parsers.md | wc -l | tr -d ' ')" 5
  fz_equal list_more_shards_than_targets "$(bash "$runner" --list --shard 7/9 --inventory parsers.md | wc -l | tr -d ' ')" 0
  # --list reads only the inventory: no package exists in this fixture.
  fz_expect list_exit 0 - bash "$runner" --list --inventory parsers.md
}

fz_usage() {
  local d; d=$(fz_tree usage)
  fz_inventory "$d" "./a FuzzA -"
  printf '# Parsers\n\n## Not fuzzed\n\n' > "$d/nosection.md"
  printf '# Parsers\n\n## Fuzzed parsers\n\n| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n| `p.A` | input | `./a` |\n' > "$d/short.md"
  cd "$d" || return
  fz_expect usage_unknown_option 2 "fuzz: usage:" bash "$runner" --nope --inventory parsers.md
  fz_expect usage_shard_zero 2 "fuzz: usage:" bash "$runner" --list --shard 0/3 --inventory parsers.md
  fz_expect usage_shard_above_count 2 "fuzz: usage:" bash "$runner" --list --shard 4/3 --inventory parsers.md
  fz_expect usage_shard_no_slash 2 "fuzz: usage:" bash "$runner" --list --shard 2 --inventory parsers.md
  fz_expect usage_shard_not_numbers 2 "fuzz: usage:" bash "$runner" --list --shard a/b --inventory parsers.md
  fz_expect usage_shard_missing_value 2 "fuzz: usage:" bash "$runner" --list --inventory parsers.md --shard
  fz_expect usage_time_word 2 "fuzz: usage:" bash "$runner" --list --time soon --inventory parsers.md
  fz_expect usage_time_no_unit 2 "fuzz: usage:" bash "$runner" --list --time 10 --inventory parsers.md
  fz_expect usage_time_valid 0 - bash "$runner" --list --time 1m30s --inventory parsers.md
  fz_expect usage_inventory_missing 2 "fuzz: usage:" bash "$runner" --list --inventory absent.md
  fz_expect usage_inventory_no_section 2 "fuzz: usage:" bash "$runner" --list --inventory nosection.md
  fz_expect usage_inventory_short_row 2 "fuzz: usage:" bash "$runner" --list --inventory short.md
}

fz_run_arguments_and_continuation() {
  local d out rc; d=$(fz_tree run_args)
  fz_fakego "$d"
  fz_inventory "$d" "./internal/a FuzzA -" "./internal/bad FuzzBad -" "./internal/t FuzzTagged harness" "./internal/c FuzzC -"
  fz_package "$d" internal/a FuzzA
  fz_package "$d" internal/bad FuzzBad
  fz_package "$d" internal/t FuzzTagged
  fz_package "$d" internal/c FuzzC
  cd "$d" || return
  out=$(PATH="$d/bin:$PATH" bash "$runner" --time 7s --inventory parsers.md 2>&1); rc=$?
  fz_equal run_exit "$rc" 1
  fz_equal run_go_calls "$(cat go.log)" "$(printf '%s\n' \
    'test -run ^$ -fuzz ^FuzzA$ -fuzztime 7s ./internal/a' \
    'test -run ^$ -fuzz ^FuzzBad$ -fuzztime 7s ./internal/bad' \
    'test -run ^$ -fuzz ^FuzzTagged$ -fuzztime 7s -tags harness ./internal/t' \
    'test -run ^$ -fuzz ^FuzzC$ -fuzztime 7s ./internal/c')"
  if [[ "$out" == *"fuzz: FAIL ./internal/bad FuzzBad"* ]]; then pass=$((pass + 1)); else fz_note run_fail_line "output lacks the FAIL line: $out"; fi
  fz_equal run_fail_lines "$(grep -c '^fuzz: FAIL ' <<<"$out")" 1
  rm go.log
  out=$(PATH="$d/bin:$PATH" bash "$runner" --shard 2/2 --inventory parsers.md 2>&1); rc=$?
  fz_equal run_shard_exit "$rc" 1
  fz_equal run_shard_calls "$(cat go.log)" "$(printf '%s\n' \
    'test -run ^$ -fuzz ^FuzzBad$ -fuzztime 30s ./internal/bad' \
    'test -run ^$ -fuzz ^FuzzC$ -fuzztime 30s ./internal/c')"
}

fz_run_checks_first() {
  local d; d=$(fz_tree run_check)
  fz_fakego "$d"
  fz_inventory "$d" "./internal/a FuzzA -" "./internal/b FuzzGone -"
  fz_package "$d" internal/a FuzzA
  cd "$d" || return
  fz_expect run_check_fails 1 "fuzz: ./internal/b: no target FuzzGone" env PATH="$d/bin:$PATH" bash "$runner" --time 1s --inventory parsers.md
  fz_equal run_check_ran_nothing "$(cat go.log 2>/dev/null)" ""
}

fz_run_real_fuzz_target() {
  local d; d=$(fz_tree run_real)
  fz_inventory "$d" "./internal/ok FuzzOk -"
  fz_package "$d" internal/ok FuzzOk
  cd "$d" || return
  fz_expect run_real 0 "elapsed" env GOTOOLCHAIN=local bash "$runner" --time 1s --inventory parsers.md
}

for t in fz_check_ok fz_check_missing_target fz_check_orphan_target fz_list_and_shard fz_usage \
  fz_run_arguments_and_continuation fz_run_checks_first fz_run_real_fuzz_target; do
  cd "$work" || exit 1
  "$t"
done

echo "fuzz tests: $pass passed, $failures failed"
[ "$failures" -eq 0 ]
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w9-tmp/1 && TMPDIR=/root/.cache/w9-tmp/1 timeout 120 bash scripts/fuzz.test.sh`
Expected: exit 1 with the single line `FAIL: <repository>/scripts/fuzz.sh does not exist`. Quote it in the report.

- [ ] **Step 3: Implement**

1. `docs/security/parsers.md`: create it with exactly this content. It is the real inventory (W9-R1): one row per Contracts "Fuzz targets" row and parser, and one `## Not fuzzed` row per entry of Assumptions "Not fuzzed". Do not reword a Package or Target cell: Tasks 5-10 create targets under these names.

````markdown
# Parser inventory

Every parser of host-tool output, configuration files and untrusted input in the repository is listed here with the Go fuzz target that exercises it. `scripts/fuzz.sh` reads the `## Fuzzed parsers` table: `make fuzz` and `.github/workflows/fuzz.yml` run each target, and `scripts/fuzz.sh --check` fails when a row names a target that does not exist in its package or a fuzz target in `cmd/`, `internal/`, `release/` or `test/` has no row.

A cell never contains a pipe character. Several rows may name one `(Package, Target)` pair when one target covers several parsers. A target that needs a build tag lists it under Tags.

## Fuzzed parsers

| Parser | Input | Package | Target | Tags |
|---|---|---|---|---|
| `netfilter.Parse` | `*-save` output of iptables and ip6tables | `./internal/platform/netfilter` | `FuzzParse` | - |
| `netfilter.Key` | a rule parsed from `*-save` output | `./internal/platform/netfilter` | `FuzzParse` | - |
| `netfilter.ParseRestore` | `iptables-restore` input: the UFW files of one load | `./internal/platform/netfilter` | `FuzzParseRestore` | - |
| `netstat.parseEntry` | a line of `/proc/net/{tcp,udp}{,6}` | `./internal/platform/netstat` | `FuzzParseSocketLine` | - |
| `netstat.parseAddrPort` | an address and port field of `/proc/net/{tcp,udp}{,6}` | `./internal/platform/netstat` | `FuzzParseSocketLine` | - |
| `netstat.parseNetlink` | rtnetlink messages | `./internal/platform/netstat` | `FuzzParseNetlink` | - |
| `netstat.parseRoute` | an rtnetlink route message | `./internal/platform/netstat` | `FuzzParseNetlink` | - |
| `platform.scanMarkers` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.replaceBlock` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.removeBlock` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.splitLines` | the text of a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `dockerapi.(*LogStream).Next` | the multiplexed log stream of the Docker Engine API | `./internal/platform/dockerapi` | `FuzzLogStream` | - |
| `dockerapi.parseVersion` | a Docker Engine API version string | `./internal/platform/dockerapi` | `FuzzCompareVersions` | - |
| `dockerapi.compareVersions` | two Docker Engine API version strings | `./internal/platform/dockerapi` | `FuzzCompareVersions` | - |
| `tslocal.(*Prefs).UnmarshalJSON` | the preferences JSON of the Tailscale local API | `./internal/platform/tslocal` | `FuzzPrefsJSON` | - |
| `tslocal.(*Prefs).MarshalJSON` | the preferences JSON of the Tailscale local API | `./internal/platform/tslocal` | `FuzzPrefsJSON` | - |
| `compose.Parse` | `docker compose config --format json` output | `./internal/modules/docker/compose` | `FuzzParse` | - |
| `docker.containerDetailOf` | container inspect JSON of the Docker Engine API | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.envVars` | the environment of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.redactArgs` | the command line of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.redactLabels` | the labels of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.parseAPIVersion` | the Docker Engine API version string | `./internal/modules/docker` | `FuzzParseAPIVersion` | - |
| `logfilter.(*Filter).Push` | container log bytes | `./internal/modules/docker/logfilter` | `FuzzFilterChunking` | - |
| `logfilter.(*Filter).Flush` | container log bytes | `./internal/modules/docker/logfilter` | `FuzzFilterChunking` | - |
| `model.ParseTuple` | `### tuple ###` lines of UFW rule files | `./internal/modules/firewall/model` | `FuzzParseTuple` | - |
| `model.ParseTag` | the comment of a UFW rule | `./internal/modules/firewall/model` | `FuzzParseTag` | - |
| `conf.Read` | `ufw.conf`, `/etc/default/ufw` and `sysctl.conf` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.parseShell` | `ufw.conf` and `/etc/default/ufw` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.parseSysctl` | `sysctl.conf` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.ConfiguredIPv6` | `/etc/default/ufw` | `./internal/modules/firewall/conf` | `FuzzConfiguredIPv6` | - |
| `conf.SysctlKey` | a `sysctl.conf` key | `./internal/modules/firewall/conf` | `FuzzConfiguredIPv6` | - |
| `loaded.parseNFTTables` | `nft list tables` output | `./internal/modules/firewall/loaded` | `FuzzParseNFTTable` | - |
| `loaded.parseNFTTable` | `nft list table` output | `./internal/modules/firewall/loaded` | `FuzzParseNFTTable` | - |
| `loaded.toolVariant` | `iptables --version` output | `./internal/modules/firewall/loaded` | `FuzzToolVariant` | - |
| `firewall.installedVersion` | `/var/lib/dpkg/status` | `./internal/modules/firewall` | `FuzzInstalledVersion` | - |
| `firewall.upstreamAtLeast` | a Debian package version | `./internal/modules/firewall` | `FuzzInstalledVersion` | - |
| `dockerfw.BlockOf` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `dockerfw.WithBlock` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `dockerfw.WithoutBlock` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `predict.Edit` | `ufw` command arguments applied to the UFW rule files | `./internal/modules/firewall/predict` | `FuzzEdit` | - |
| `predict.parseVector` | the rule vector of a `ufw` command | `./internal/modules/firewall/predict` | `FuzzEdit` | - |
| `predict.ufwDefaults` | the default policies of `/etc/default/ufw` | `./internal/modules/firewall/predict` | `FuzzSetDefault` | - |
| `predict.setDefault` | the default policies of `/etc/default/ufw` | `./internal/modules/firewall/predict` | `FuzzSetDefault` | - |
| `eval.Evaluate` | a ruleset that `netfilter.Parse` accepts | `./internal/modules/firewall/eval` | `FuzzEvaluate` | - |
| `eval.Unmodelled` | a ruleset that `netfilter.Parse` accepts | `./internal/modules/firewall/eval` | `FuzzEvaluate` | - |
| `tailscale.readOSRelease` | `/etc/os-release` | `./internal/modules/tailscale` | `FuzzReadOSRelease` | - |
| `tailscale.installedVersion` | `/var/lib/dpkg/status` | `./internal/modules/tailscale` | `FuzzDpkgStatus` | - |
| `tailscale.keyringFingerprints` | an OpenPGP keyring | `./internal/modules/tailscale` | `FuzzKeyringFingerprints` | - |
| `stun.Parse` | STUN replies | `./internal/modules/tailscale/stun` | `FuzzParse` | - |
| `stun.decodeAddress` | the address attributes of STUN replies | `./internal/modules/tailscale/stun` | `FuzzParse` | - |
| `policy.Apply` | a tailnet policy file in HuJSON and the edits to patch into it | `./internal/modules/tailnet/policy` | `FuzzApply` | - |
| `policy.pointerTokens` | a JSON pointer of a policy edit | `./internal/modules/tailnet/policy` | `FuzzApply` | - |
| `policy.Canonical` | a tailnet policy file in HuJSON | `./internal/modules/tailnet/policy` | `FuzzCanonical` | - |
| `policy.Diff` | two tailnet policy files in HuJSON | `./internal/modules/tailnet/policy` | `FuzzCanonical` | - |
| `derp.parseDERPMap` | the DERP map in a tailnet policy, as HuJSON | `./internal/modules/derp` | `FuzzParseDERPMap` | - |
| `artifact.ParseManifest` | the derper manifest | `./internal/modules/derp/artifact` | `FuzzParseManifest` | - |
| `server.ParseCert` | a certificate in PEM form | `./internal/modules/derp/server` | `FuzzParseCert` | - |
| `ingress.(*Config).UnmarshalJSON` | the ingress configuration of a Cloudflare tunnel, as JSON | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `ingress.(*Rule).UnmarshalJSON` | one ingress rule of a Cloudflare tunnel, as JSON | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `ingress.Hash` | an ingress configuration of a Cloudflare tunnel | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `cloudflare.serviceHost` | the service URL of a tunnel ingress rule | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `cloudflare.legacyIPv4` | the host of a tunnel service URL | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `cloudflare.serviceProblem` | the service URL of a tunnel ingress rule | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `proxy.CheckUpstream` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `proxy.parseNginxIPv4` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `proxy.parseInetAton` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `update.ParseVersion` | a version in the release index | `./internal/update` | `FuzzParseVersion` | - |
| `update.Compare` | two versions in the release index | `./internal/update` | `FuzzParseVersion` | - |
| `update.checksumFor` | `checksums.txt` of a release | `./internal/update` | `FuzzChecksumFor` | - |
| `update.extractBinary` | a release tarball | `./internal/update` | `FuzzExtractBinary` | - |
| `releasetool.parseVersion` | a version given to the release tooling | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.parseTag` | a release tag | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.compare` | two versions given to the release tooling | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.keyID` | a minisign signature file | `./release/releasetool` | `FuzzKeyID` | - |
| `transport.ParseBootstrap` | the enrollment bundle | `./internal/transport` | `FuzzParseBootstrap` | - |
| `transport.ParseToken` | an enrollment token | `./internal/transport` | `FuzzParseToken` | - |
| `harness.ParsePorts` | `nmap -oG -` output | `./test/harness` | `FuzzParsePorts` | harness |
| `harness.m3dParsePortEntry` | a port entry of `nmap -oG -` output | `./test/harness` | `FuzzParsePorts` | harness |
| `hostinfo.parseOSRelease` | `/etc/os-release` | `./internal/modules/hostinfo` | `FuzzParseOSRelease` | - |
| `hostinfo.parseCPUInfo` | `/proc/cpuinfo` | `./internal/modules/hostinfo` | `FuzzParseCPUInfo` | - |
| `hostinfo.parseMemInfo` | `/proc/meminfo` | `./internal/modules/hostinfo` | `FuzzParseMemInfo` | - |
| `hostinfo.parseLoadAvg` | `/proc/loadavg` | `./internal/modules/hostinfo` | `FuzzParseLoadAvg` | - |
| `hostinfo.parseUptime` | `/proc/uptime` | `./internal/modules/hostinfo` | `FuzzParseUptime` | - |
| `backup.formatReadHeader` | the header of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzReadHeader` | - |
| `backup.formatEncodeHeader` | the header of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzReadHeader` | - |
| `backup.frameNewReader` | the frames of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzFrames` | - |
| `backup.formatReadMembers` | the members of the frames of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzFrames` | - |
| `backup.decodeJournal` | the restore journal | `./internal/controller/backup` | `FuzzDecodeJournal` | - |
| `backup.checkJournal` | the restore journal | `./internal/controller/backup` | `FuzzDecodeJournal` | - |
| `secret.(*Sealer).Open` | a sealed value from the database | `./internal/core/secret` | `FuzzOpen` | - |
| `secret.RedactJSON` | an audit or journal record in JSON | `./internal/core/secret` | `FuzzRedactJSON` | - |
| `cloud.(*Client).retryAfter` | the `Retry-After` header of a cloud API response | `./internal/controller/cloud` | `FuzzRetryAfter` | - |
| `events.ParseID` | the `Last-Event-ID` request header | `./internal/controller/events` | `FuzzParseID` | - |
| `cli.printEvents` | the server-sent event stream of the controller | `./internal/cli` | `FuzzPrintEvents` | - |
| `install.CheckAgentAddress` | the agent address flag of a unit file | `./internal/install` | `FuzzCheckAgentAddress` | - |

## Not fuzzed

| Parser | Input | Reason |
|---|---|---|
| Typed JSON decoding of provider API responses | Docker, Tailscale, Cloudflare and Nginx Proxy Manager API responses, and the JSON of the agent's mTLS peer | `encoding/json` decodes into typed structs; there is no hand-written parser |
| `systemd` state through D-Bus | unit and job state from systemd | typed D-Bus decoding, not a text grammar |
| `auth.parsePHC` | password hashes in Darkpanel's database | written only by Darkpanel |
| Firewall journal state | the firewall journal rows | written only by Darkpanel |
| `update.ReadRecord` | update transaction records under the update directory | root-owned state that only Darkpanel writes |
| `ValidateStaged` | the staged update binary and its record | root-owned state that only Darkpanel writes |
| Release index decoding (`update` source `releases.json`) | the release index of the update mirror | `encoding/json` decodes into typed structs after a size bound, and the format version is checked; there is no hand-written parser |
| Signature parsing in `update.VerifyChecksums` | the minisign signatures of `checksums.txt` from the update mirror | parsed and verified by the third-party `aead.dev/minisign`; Darkpanel's own code only compares the parsed algorithm and trusted comment |
| `backup.Read` as a whole | a `.dpbk` backup file | every input runs Argon2id at 64 MiB or more, so the header and the frames are fuzzed separately by `FuzzReadHeader` and `FuzzFrames` |
| Process ownership in `netstat` | `/proc/<pid>/fd` links | a file walk without a text grammar |
| `held` | a single value read from the host | no grammar beyond `strings.TrimSpace` |
| `lastLine` | the last line of a command's output | no grammar beyond `strings.TrimSpace` |
| `legacyTables` | a single value read from the host | no grammar beyond `strings.TrimSpace` |
| `readRPFilter` | a sysctl value | no grammar beyond `strconv.Atoi` |
| `readSysctl` | a sysctl value | no grammar beyond `strconv.Atoi` |
| `LoadPins` | `images.json` | repository-owned input |
| Command-line flags | arguments of `darkpanel` commands | parsed by cobra |
````

2. `scripts/fuzz.sh` (Bash, `#!/usr/bin/env bash`, `set -euo pipefail`, run from the repository root; Contracts "Fuzz runner" is the specification, and these decisions complete it). Use awk and `find`, not grep, for everything it parses: grep is ugrep on some hosts and GNU grep on others (see the first comment of `scripts/check-adrs.sh`).
   - Options: `--check` and `--list` choose the mode (the default mode is run); `--time`, `--shard` and `--inventory` take the next argument, and a missing value is a usage error; anything else starting with `-` is an unknown option. Defaults: inventory `docs/security/parsers.md`, time `30s`, shard `1/1`.
   - A function `usage <reason>` prints one line to standard error that starts with `fuzz: usage: `, then the reason and the synopsis, and exits 2. It is the only way the script exits 2. It is called for: an unknown option, a missing option value, a `--time` value that does not match `^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$` (checked in every mode, so `--time 10` and `--time soon` fail), a `--shard` value that is not `<k>/<n>` with `1 <= k <= n`, an `--inventory` that is not a file, an inventory without a `## Fuzzed parsers` section, and a table row of that section that does not have exactly five cells.
   - Reading the inventory: take the lines between `## Fuzzed parsers` and the next line that starts with `## `; rows are the lines that start with `|` and are split on `|` (a cell never contains a pipe); skip the header row (its Package cell is `Package`) and the separator row (a Package cell of dashes only); trim spaces and backticks from every cell; keep the distinct `(package, target, tags)` triples in first-appearance order. `--shard k/n` keeps the triples whose 0-based index `i` satisfies `i % n == k-1`. `--check` and the run mode use the unfiltered triples for the check and the filtered ones for the runs.
   - `--list` prints the triples of the shard as `<package> <target> <tags>`, one per line on standard output, with the Tags cell as written (`-` for none), and exits 0 without reading any package or running the check. An empty shard prints nothing.
   - `--check`: a triple is satisfied when a `*_test.go` file directly in the package directory (the package without its leading `./`; subdirectories do not count) has a line matching `^func <target>\(f \*testing\.F\)` (`FuzzParseRestore` does not satisfy `FuzzParse`). Print `fuzz: <package>: no target <target>` to standard error for each unsatisfied triple in inventory order. Then find every `^func Fuzz[A-Za-z0-9_]*\(f \*testing\.F\)` in the `*_test.go` files under those of `cmd`, `internal`, `release` and `test` that exist; the package of a hit is `./` plus the directory of its file; print `fuzz: <package>: <target> is not in the inventory` to standard error for each hit whose `<package> <target>` pair is in no inventory row, in byte order of the pair, without duplicates. Exit 1 when any line was printed. Otherwise print `fuzz: inventory ok (<n> targets)` to standard output, `n` being the number of distinct triples of the whole inventory, and exit 0.
   - Run mode: run the check first and exit 1 with its lines when it fails, running nothing. Then, for each triple of the shard in order, run `go test -run '^$' -fuzz '^<target>$' -fuzztime <time>`, with `-tags <tags>` added when Tags is not `-`, then the package, finding `go` through `PATH`. The output of `go test` passes through. A failing run prints `fuzz: FAIL <package> <target>` to standard error and the loop continues with the next triple. Exit 1 when any run failed, else 0 (an empty shard runs nothing and exits 0).

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
mkdir -p /root/.cache/w9-tmp/1 && export TMPDIR=/root/.cache/w9-tmp/1 GOTOOLCHAIN=local
timeout 120 bash scripts/fuzz.test.sh
timeout 60 shellcheck scripts/fuzz.sh scripts/fuzz.test.sh
timeout 60 bash scripts/fuzz.sh --list | wc -l
timeout 120 bash scripts/fuzz.sh --check 2>&1 | grep -c ': no target '
timeout 120 bash scripts/fuzz.sh --check 2>&1 | grep -c 'is not in the inventory'
timeout 300 go mod tidy -diff
```

Expected, in order: `fuzz tests: 40 passed, 0 failed`; no shellcheck output; `57`; `45`; `0`; no output from `go mod tidy -diff`. The real `bash scripts/fuzz.sh --check` exits 1 here and `make fuzz` fails the same way: the 45 targets that Tasks 5-10 add do not exist yet, and the 12 existing ones (`FuzzParseBootstrap`, `FuzzParse`, `FuzzParseRestore`, the five `hostinfo` targets, `FuzzParseTuple`, `FuzzReadFiles`, `FuzzParseNFTTable`, `FuzzEdit`) are in the inventory, so `0` orphans is the check that the inventory names every existing target. Quote the outputs. Then `rm -rf /root/.cache/w9-tmp/1` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/security/parsers.md scripts/fuzz.sh scripts/fuzz.test.sh
git commit -m "feat(fuzz): add the parser inventory and runner"
```

### Task 2: web: accessibility test helpers and page titles

**Files:**
- Create: `web/src/test/a11y.ts`
- Create: `web/src/test/a11y.test.tsx`
- Create: `web/src/lib/page-title.ts`
- Create: `web/src/lib/page-title.test.tsx`

**Interfaces:**
- Consumes: none. Read, not edited: `web/src/test/setup.ts` (loads `vitest-axe/extend-expect`, cleans up after each test), `web/src/test/utils.tsx`, `web/src/test/axe.test.tsx` (the existing axe usage), `web/vitest.config.ts` (jsdom, `@` alias).
- Produces: Contracts "Accessibility helpers" (`expectNoAxeViolations`, `tabSequence`, `focusedName`) and "Page title" (`usePageTitle`). Tasks 13-15, 20 and 22 import the helpers; Tasks 13-15 and 20 call `usePageTitle`.

**Items:** 41

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

Create `web/src/test/a11y.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "./a11y"

function Form() {
  return (
    <form>
      <label htmlFor="a11y-name">Name</label>
      <input id="a11y-name" />
      <button type="button" aria-label="Close">
        x
      </button>
      <a href="/docs">Docs</a>
    </form>
  )
}

async function failure(promise: Promise<void>): Promise<string> {
  try {
    await promise
  } catch (err) {
    return err instanceof Error ? err.message : String(err)
  }
  return ""
}

test("expectNoAxeViolations resolves for a labelled form", async () => {
  const { container } = render(<Form />)
  await expectNoAxeViolations(container)
})

test("expectNoAxeViolations names the rule and the target of every violation", async () => {
  const { container } = render(
    <div>
      <input id="a11y-bare" />
      <img id="a11y-img" src="x.png" />
    </div>,
  )
  const message = await failure(expectNoAxeViolations(container))
  expect(message).toMatch(/^axe found 2 violation\(s\):/)
  expect(message).toMatch(/label: .*#a11y-bare/)
  expect(message).toMatch(/image-alt: .*#a11y-img/)
})

test("tabSequence returns the accessible name after each Tab press", async () => {
  render(<Form />)
  expect(await tabSequence(userEvent.setup(), 3)).toEqual(["Name", "Close", "Docs"])
})

test("tabSequence goes backwards from the end with Shift+Tab", async () => {
  render(<Form />)
  expect(await tabSequence(userEvent.setup(), 2, { backwards: true })).toEqual(["Docs", "Close"])
})

test("focusedName is empty while the body has focus and follows focus", () => {
  render(<Form />)
  expect(focusedName()).toBe("")
  screen.getByRole("link", { name: "Docs" }).focus()
  expect(focusedName()).toBe("Docs")
})

test("focusedName prefers aria-labelledby over the content", () => {
  render(
    <div>
      <span id="a11y-label">Delete host web-1</span>
      <button aria-labelledby="a11y-label">Delete</button>
    </div>,
  )
  screen.getByRole("button").focus()
  expect(focusedName()).toBe("Delete host web-1")
})
```

Create `web/src/lib/page-title.test.tsx`:

```tsx
import { render } from "@testing-library/react"
import { beforeEach, expect, test } from "vitest"
import { usePageTitle } from "./page-title"

function Page({ title }: { title: string }) {
  usePageTitle(title)
  return null
}

beforeEach(() => {
  document.title = "Before"
})

test.each([
  ["Hosts", "Hosts · Darkpanel"],
  ["", "Darkpanel"],
])("usePageTitle(%j) sets the title to %j", (title, want) => {
  render(<Page title={title} />)
  expect(document.title).toBe(want)
})

test("usePageTitle follows a changed title", () => {
  const { rerender } = render(<Page title="Hosts" />)
  rerender(<Page title="Audit log" />)
  expect(document.title).toBe("Audit log · Darkpanel")
})

test("usePageTitle restores the previous title on unmount", () => {
  const { unmount } = render(<Page title="Hosts" />)
  unmount()
  expect(document.title).toBe("Before")
})

test("a page that replaces another in one commit keeps its own title", () => {
  const { rerender, unmount } = render(<Page key="a" title="Hosts" />)
  rerender(<Page key="b" title="Audit log" />)
  expect(document.title).toBe("Audit log · Darkpanel")
  unmount()
  expect(document.title).toBe("Before")
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 pnpm -C web exec vitest run src/test/a11y.test.tsx src/lib/page-title.test.tsx`
Expected: both suites fail to load, with `Failed to resolve import "./page-title" from "src/lib/page-title.test.tsx"` and the same for `./a11y`. Quote the output.

- [ ] **Step 3: Implement**

1. `web/src/lib/page-title.ts` exports `usePageTitle(title: string): void`, a hook built on `useEffect` that depends on `title`. When the effect runs it reads `document.title` as the previous title, sets `document.title` to `"<title> · Darkpanel"` (the separator is a space, U+00B7, a space), or to `"Darkpanel"` when `title` is `""`, and returns a cleanup that sets `document.title` back to the previous title. Reading the previous title inside the effect, not at render time, is what keeps a page that replaces another in the same commit correct: React runs every cleanup of a commit before any new effect, so the new page captures the restored title and then overwrites it. Give the export the doc comment of Contracts "Page title".
2. `web/src/test/a11y.ts` exports the three functions of Contracts "Accessibility helpers", each with its doc comment:
   - `expectNoAxeViolations(el)` runs `axe` from `vitest-axe` on `el`. With no violation it resolves. Otherwise it throws an `Error` whose message is `axe found <n> violation(s):` followed, for each violation, by a newline and `<rule id>: <target>, <target>`, where each target is the node's `target` array joined with a space. It does not use `toHaveNoViolations`, whose message is a long formatted report.
   - `focusedName()` returns `""` when `document.activeElement` is not an `HTMLElement` or is `document.body`. Otherwise it returns the accessible name of that element. `dom-accessibility-api`, which computes it, is not resolvable from `web/` (pnpm strict layout) and no task may change `web/package.json`. Use what `@testing-library/dom` does export: find the role the element has through `getRoles(document.body)` (the entry whose elements include it), then call `queryAllByRole(document.body, role, { hidden: true, name: (accessibleName, node) => ... })` whose `name` callback records `accessibleName` when `node` is the element and returns `false`. The recorded value is the name that `dom-accessibility-api` computed. An element that has no role returns `""`.
   - `tabSequence(user, count, opts)` presses Tab `count` times with `await user.tab({ shift: opts.backwards === true })` (`opts` defaults to `{}`), pushing `focusedName()` after each press, and returns the names.

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
timeout 300 pnpm -C web exec vitest run src/test/a11y.test.tsx src/lib/page-title.test.tsx
timeout 1200 make lint-web test-web
timeout 300 go mod tidy -diff
```

Expected: `Test Files  2 passed (2)` and `Tests  11 passed (11)` for the first command; `make lint-web test-web` ends with every test file passed, eslint and `tsc --noEmit` printing nothing; no output from `go mod tidy -diff`. Quote the summary lines. The Vitest line `Not implemented: HTMLCanvasElement's getContext()` is axe's own noise in jsdom and is expected.

- [ ] **Step 5: Commit**

```bash
git add web/src/test/a11y.ts web/src/test/a11y.test.tsx web/src/lib/page-title.ts web/src/lib/page-title.test.tsx
git commit -m "feat(web): add a11y test helpers and page titles"
```

### Task 3: docs: API reference generator

**Files:**
- Create: `internal/apidocs/apidocs.go` (package `apidocs`: `Generate`, `Write`)
- Create: `internal/apidocs/render.go` (the Markdown rendering behind `Generate`)
- Create: `internal/apidocs/apidocs_test.go` (package `apidocs_test`)
- Create: `internal/apidocs/apidocsgen/main.go` (package `main`)
- Create: `docs/api/**` (generated by `make api-docs`, never written by hand)
- Modify: `Makefile` (add the `api-docs` target and its name on the `.PHONY` line)

**Interfaces:**
- Consumes: `schema.OpenAPI(reg, routes)`, `schema.Route`, `registry.Build()`, `api.Routes()` and `core.RequiredScope`, all existing. Read, not edited: `internal/core/schema/openapi.go`, `internal/core/schema/schemagen/main.go` (the shape of a generator command), `internal/registry/registry.go`, `internal/controller/api/routes.go`, `internal/modules/modules_test.go` (`TestSnapshotsCurrent`, the pattern of `TestAdAPIDocsCurrent`), `internal/core/contract.go` (`ActionSpec`, `ResourceDef`, `Manifest`), `internal/core/scope.go`.
- Produces: Contracts "API reference" (`apidocs.Generate`, `docs/api/**`, `make api-docs`, `TestAdAPIDocsCurrent`). The package also exports `apidocs.Write(dir string, files map[string][]byte) error`, which `apidocsgen` and `TestAdWriteReplacesTheGeneratedFiles` use. Tasks 16, 18, 19 and 24 link to or regenerate `docs/api/`.

**Items:** 43

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/apidocs/apidocs_test.go`. It builds a two-module fixture registry (`demo` with one action of each kind, a break-glass action and a resource; `quiet` with only a break-glass action), so the golden text does not move when a real action changes. `TestAdAPIDocsCurrent` checks the committed `docs/api/`.

```go
package apidocs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/apidocs"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

type adAddress struct {
	Host string `json:"host" jsonschema_description:"The host name."`
	Port int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
}

type adInput struct {
	Name     string      `json:"name" jsonschema_description:"The item name, such as web-<n>."`
	Mode     string      `json:"mode,omitempty" jsonschema:"enum=fast,enum=safe" jsonschema_description:"How to apply it: fast | safe."`
	Password string      `json:"password" secret:"true"`
	Target   adAddress   `json:"target"`
	Backups  []adAddress `json:"backups,omitempty"`
	Tags     []string    `json:"tags,omitempty"`
}

type adOutput struct {
	ID     string    `json:"id"`
	Token  string    `json:"token" secret:"true"`
	Target adAddress `json:"target"`
}

type adEmpty struct{}

type adStatus struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty" jsonschema_description:"Why it is not healthy."`
}

// adHandler implements every handler interface and does nothing; Generate
// never calls handlers.
type adHandler[In, Out any] struct{}

func (adHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (adHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (adHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (adHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (adHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) { return nil, nil }

func (adHandler[In, Out]) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

// adDemo has one action of each kind that gets a section, a break-glass
// action that gets none, and a resource.
type adDemo struct{}

func (adDemo) Manifest() core.Manifest {
	h := adHandler[adInput, adOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Summary: "Show one item", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Summary: "Add an item", Version: 1, Side: core.SideAgent,
				LockKeys: []string{"demo"}, RepairedBy: "demo.item.repair"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.repair", Summary: "Repair the items", Version: 1, Class: core.ClassIrreversible,
				Side: core.SideAgent, LockKeys: []string{"demo"}}, adHandler[adEmpty, adOutput]{}),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Summary: "Remove an item", Version: 1, Class: core.ClassCompensable,
				Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineReveal[adEmpty, adOutput](core.ActionSpec{Name: "demo.item.reveal", Summary: "Reveal the item secret", Version: 1,
				Side: core.SideAgent, MinScope: core.ScopeAdmin},
				func(context.Context, core.Env, adEmpty) (core.Revealed[adOutput], error) {
					return core.Revealed[adOutput]{}, nil
				}),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
				BreakGlass: true}, adHandler[adEmpty, adOutput]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (adStatus, error) { return adStatus{}, nil }),
		},
	}
}

// adQuiet has only a break-glass action, so it has no operation and no page.
type adQuiet struct{}

func (adQuiet) Manifest() core.Manifest {
	return core.Manifest{
		ID: "quiet", Version: 1,
		Actions: []core.ActionDef{
			core.DefineMutating(core.ActionSpec{Name: "quiet.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
				BreakGlass: true}, adHandler[adEmpty, adOutput]{}),
		},
	}
}

var adRoutes = []schema.Route{
	{Method: "GET", Path: "/healthz", Summary: "Health", Public: true},
	{Method: "POST", Path: "/api/v1/auth/login", Summary: "Log in", Public: true},
	{Method: "GET", Path: "/api/v1/changes/{id}", Summary: "Show a change"},
}

func adRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(adDemo{}, adQuiet{})
	if err != nil {
		t.Fatalf("core.NewRegistry: %v", err)
	}
	return reg
}

func adGenerate(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := apidocs.Generate(adRegistry(t), adRoutes)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return files
}

// adSection returns the text of the ## section of page titled heading, up to
// the next ## heading.
func adSection(t *testing.T, page, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("the page has no section %q:\n%s", heading, page)
	}
	body, _, _ := strings.Cut(rest, "\n## ")
	return "## " + heading + "\n" + body
}

func adLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestAdGenerateKeys(t *testing.T) {
	files := adGenerate(t)
	if got, want := slices.Sorted(maps.Keys(files)), []string{"README.md", "demo.md", "openapi.json"}; !slices.Equal(got, want) {
		t.Errorf("Generate keys = %v, want %v: one page per module with an operation, and none for a module with only a break-glass action", got, want)
	}
}

func TestAdOpenAPIDocumentIsTheIndentedSchemaDocument(t *testing.T) {
	raw, err := schema.OpenAPI(adRegistry(t), adRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Indent(&want, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	want.WriteByte('\n')
	if got := adGenerate(t)["openapi.json"]; !bytes.Equal(got, want.Bytes()) {
		t.Errorf("openapi.json is not schema.OpenAPI's document indented with two spaces and a final newline; it starts %.60q", got)
	}
}

func TestAdModulePageSections(t *testing.T) {
	page := string(adGenerate(t)["demo.md"])
	if !strings.HasPrefix(page, "# demo API\n\nBack to the [API reference](README.md).\n\n## ") {
		t.Errorf("the page starts %.100q, want the title, the link to the index and the first section", page)
	}
	var headings []string
	for _, line := range strings.Split(page, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			headings = append(headings, h)
		}
	}
	want := []string{"demo.item.add v1", "demo.item.get v1", "demo.item.remove v1", "demo.item.repair v1", "demo.item.reveal v1", "demo.status"}
	if !slices.Equal(headings, want) {
		t.Errorf("sections = %q, want %q (sorted by name and version, no break-glass action)", headings, want)
	}

	add := adSection(t, page, "demo.item.add v1")
	wantAdd := adLines(
		"## demo.item.add v1",
		"",
		"Add an item",
		"",
		"| Operation | Request |",
		"|---|---|",
		"| plan | `POST /api/v1/hosts/{host}/actions/demo.item.add/v1/plan` |",
		"| apply | `POST /api/v1/hosts/{host}/actions/demo.item.add/v1/apply` |",
		"",
		"Minimum scope: `operate`.",
		"",
		"### Input",
		"",
		"Sent in the `input` field of the request body.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `backups` | array of object | no |  |",
		"| `backups[].host` | string | yes | The host name. |",
		"| `backups[].port` | integer | yes |  |",
		"| `mode` | string | no | How to apply it: fast \\| safe. Values: fast, safe. |",
		"| `name` | string | yes | The item name, such as web-\\<n\\>. |",
		"| `password` | string | yes | Secret. |",
		"| `tags` | array of string | no |  |",
		"| `target` | object | yes |  |",
		"| `target.host` | string | yes | The host name. |",
		"| `target.port` | integer | yes |  |",
		"",
		"### Output",
		"",
		"Returned in the `output` field of the change that apply returns.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `id` | string | yes |  |",
		"| `target` | object | yes |  |",
		"| `target.host` | string | yes | The host name. |",
		"| `target.port` | integer | yes |  |",
		"| `token` | string | yes | Secret. |",
		"",
	)
	if strings.TrimRight(add, "\n") != strings.TrimRight(wantAdd, "\n") {
		t.Errorf("section demo.item.add v1 =\n%s\nwant\n%s", add, wantAdd)
	}

	for _, tt := range []struct{ heading, want string }{
		{"demo.item.get v1", "| read | `POST /api/v1/hosts/{host}/actions/demo.item.get/v1/read` |"},
		{"demo.item.get v1", "Minimum scope: `read`."},
		{"demo.item.remove v1", "| plan | `POST /api/v1/actions/demo.item.remove/v1/plan` |"},
		{"demo.item.remove v1", "Minimum scope: `admin`."},
		{"demo.item.repair v1", "This action takes no input fields."},
		{"demo.item.reveal v1", "| reveal | `POST /api/v1/hosts/{host}/actions/demo.item.reveal/v1/reveal` |"},
		{"demo.item.reveal v1", "Returned in the `value` field of the response."},
		{"demo.item.reveal v1", "Minimum scope: `admin`."},
	} {
		if sec := adSection(t, page, tt.heading); !strings.Contains(sec, tt.want+"\n") {
			t.Errorf("section %s lacks the line %q:\n%s", tt.heading, tt.want, sec)
		}
	}

	status := adSection(t, page, "demo.status")
	wantStatus := adLines(
		"## demo.status",
		"",
		"Read demo.status",
		"",
		"| Operation | Request |",
		"|---|---|",
		"| read | `GET /api/v1/hosts/{host}/resources/demo/status` |",
		"",
		"Minimum scope: `read`.",
		"",
		"### Output",
		"",
		"Returned in the `output` field of the response.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `detail` | string | no | Why it is not healthy. |",
		"| `healthy` | boolean | yes |  |",
	)
	if strings.TrimRight(status, "\n") != strings.TrimRight(wantStatus, "\n") {
		t.Errorf("section demo.status =\n%s\nwant\n%s", status, wantStatus)
	}
}

func TestAdIndexPage(t *testing.T) {
	index := string(adGenerate(t)["README.md"])
	if !strings.HasPrefix(index, "# Darkpanel API reference\n") {
		t.Errorf("the index starts %.60q, want the title", index)
	}
	for _, want := range []string{
		"## Authentication",
		"| `session` | apiKey | cookie `__Host-darkpanel_session` | Session cookie; darkpanel_session on the plain-HTTP loopback listener. |",
		"| `token` | http | `Authorization: Bearer` header | API token dpt_\\<id\\>_\\<secret\\>. |",
		"## Common components",
		"### Plan",
		"### Change",
		"### ApplyRequest",
		"### Problem",
		"| `planId` | string | yes |  |",
		"| `expiresAt` | string (date-time) | yes |  |",
		"## Routes",
		"| GET | `/api/v1/changes/{id}` | session or token | Show a change |",
		"| GET | `/healthz` | none | Health |",
		"| POST | `/api/v1/auth/login` | none | Log in |",
		"## Modules",
		"- [demo](demo.md)",
	} {
		if !strings.Contains(index, want+"\n") {
			t.Errorf("the index lacks the line %q", want)
		}
	}
	if strings.Contains(index, "quiet") {
		t.Error("the index links a module without an operation")
	}
	if strings.Contains(index, "demo.item.add") {
		t.Error("the index lists an action; the routes table holds only the hand-written routes")
	}
	order := []string{"## Authentication", "## Common components", "## Routes", "## Modules"}
	last := -1
	for _, h := range order {
		at := strings.Index(index, "\n"+h+"\n")
		if at < last {
			t.Errorf("section %q is out of order in the index", h)
		}
		last = at
	}
}

func TestAdGenerateIsDeterministic(t *testing.T) {
	first, second := adGenerate(t), adGenerate(t)
	for name, b := range first {
		if !bytes.Equal(b, second[name]) {
			t.Errorf("two Generate calls differ in %s", name)
		}
	}
}

func TestAdWriteReplacesTheGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"stale.md": "old", "stale.json": "{}", "notes.txt": "keep", "sub/inner.md": "keep",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"README.md": []byte("# index\n"), "openapi.json": []byte("{}\n")}
	for range 2 {
		if err := apidocs.Write(dir, files); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	for name, want := range files {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, gone := range []string{"stale.md", "stale.json"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Write left %s behind: %v", gone, err)
		}
	}
	for _, kept := range []string{"notes.txt", "sub/inner.md"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("Write removed %s, which is not a .md or .json file directly in the directory: %v", kept, err)
		}
	}
}

// TestAdAPIDocsCurrent regenerates the reference in memory and fails when a
// committed file under docs/api/ differs, is missing, or belongs to no key.
// make api-docs fixes all three for the .md and .json files it manages.
func TestAdAPIDocsCurrent(t *testing.T) {
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	files, err := apidocs.Generate(reg, api.Routes())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := filepath.Join("..", "..", "docs", "api")
	for _, name := range slices.Sorted(maps.Keys(files)) {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))) //nolint:gosec // G304: a path under the committed docs/api directory
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.Errorf("docs/api/%s is missing; run make api-docs", name)
		case err != nil:
			t.Errorf("docs/api/%s: %v", name, err)
		case !bytes.Equal(got, files[name]):
			t.Errorf("docs/api/%s is out of date; run make api-docs", name)
		}
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			t.Errorf("docs/api/%s belongs to no generated file; delete it (make api-docs removes a stale .md or .json file)", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walk docs/api: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
mkdir -p /root/.cache/w9-tmp/3 && export TMPDIR=/root/.cache/w9-tmp/3 GOTOOLCHAIN=local
timeout 300 go test -count=1 ./internal/apidocs/
```

Expected: it fails with `no non-test Go files in <repository>/internal/apidocs` and `FAIL ... [build failed]`. Quote the output.

- [ ] **Step 3: Implement**

1. `internal/apidocs/apidocs.go`: package comment, then `Generate(reg *core.Registry, routes []schema.Route) (map[string][]byte, error)` with the doc comment of Contracts "API reference". It:
   - calls `schema.OpenAPI(reg, routes)`; the key `openapi.json` is that document re-indented with `json.Indent` (prefix `""`, indent two spaces) plus a final newline;
   - unmarshals the same bytes into a generic `map[string]any` and builds an index of every operation of its `paths` by `operationId` (the upper-case method, the path and the operation object); operations without an `operationId` are the hand-written routes;
   - for each manifest of `reg.Modules()`, in order, builds the module page (below) and, when the module has at least one section, stores it under `<id>.md`; the README lists exactly those modules, in that order;
   - stores `README.md` last;
   - returns errors, never panics: a missing operation is `apidocs: the OpenAPI document has no operation <id>`, a missing component schema `apidocs: no component schema <name>`.

   `Write(dir string, files map[string][]byte) error` creates `dir` (mode 0750), removes every other `.md` and `.json` file directly in it (never a subdirectory and never another extension), then writes each file by its slash path (mode 0644; `//nolint:gosec // G306: documentation files are world-readable`). Wrap errors as `apidocs: ...`. It is idempotent.

2. `internal/apidocs/render.go` renders, byte for byte deterministically (sort every map key; never range a map into output), exactly these shapes. `TestAdModulePageSections` and `TestAdIndexPage` pin them.
   - **Cell text** (`cell`): collapse runs of whitespace, including newlines, to one space, and escape `|`, `<` and `>` with a backslash, so a description such as `web-<n>` stays visible and does not break the table.
   - **Resolving a schema**: a schema with a `$ref` of `#/components/schemas/<name>` is replaced by that component, repeatedly; the last name followed identifies the schema for cycle detection.
   - **Type column** (`typeName`, on the resolved schema): `oneOf` gives the member type names joined with ` or `; `type` `array` gives `array of <item type name>` (`array of any` when it has no `items`); `type` `object` with an `additionalProperties` schema and no `properties` gives `map of <type name>`; `type` `string` with a `format` gives `string (<format>)`; a `type` list gives its entries joined with ` or `; any other `type` string is printed as it is; a schema without `type` is `any`.
   - **Description column**: the property's own `description` (else the resolved schema's), then, when an `enum` exists on the property or the resolved schema, `Values: <v1>, <v2>.`, then, when `x-secret` is true there, `Secret.`, joined with single spaces. Empty when none applies. The result goes through `cell`.
   - **Table** (`table(component, empty)`): the component schema is resolved; when it has no `properties` the result is the line `empty` only. Otherwise it is the header `| Field | Type | Required | Description |` and `|---|---|---|---|`, then one row per property in ascending name order: ``| `<path>` | <type> | yes or no | <description> |``, where `yes` means the name is in the parent's `required` list. After a row, when the property (or, for an array, its resolved `items`) resolves to a schema with `properties` whose reference is not already being expanded further up, its properties follow as rows with the path prefix `<path>.` (object) or `<path>[].` (array), recursively.
   - **Section of an action**: heading `## <name> v<version>`; a blank line; the summary of the first listed operation, then a blank line (both omitted when the operation has no summary); the table `| Operation | Request |` / `|---|---|` with one row ``| <kind> | `<METHOD> <path>` |`` per operation, followed by a blank line; the line ``Minimum scope: `<scope>`.`` with `core.RequiredScope(spec)`; a blank line; then `### Input`, a blank line, ``Sent in the `input` field of the request body.``, a blank line and the table of the component `<name>.v<version>.input` (empty text `This action takes no input fields.`); then `### Output`, a blank line, the note, a blank line and the table of `<name>.v<version>.output` (empty text `This action returns no fields.`). The kinds and operation ids are: a reveal action (`Spec.Reveal`) has `reveal`; any other read action has `read`; every other action has `plan` then `apply`; the operation id is `<name>.v<version>.<kind>`. The note is ``Returned in the `value` field of the response.`` for reveal, ``Returned in the `output` field of the response.`` for read, and ``Returned in the `output` field of the change that apply returns.`` for plan and apply.
   - **Section of a resource**: heading `## <name>`, the summary of operation `<name>.get`, the operations table with one row ``| read | `GET <path>` |``, the scope `read`, no Input part, and the Output part of the component `<name>.output` with the read note.
   - **Module page**: ``# <id> API``, a blank line, ``Back to the [API reference](README.md).``, then each section preceded by a blank line. The sections are the module's `Manifest.Actions` except those with `Spec.BreakGlass` or `Spec.Internal`, plus its `Manifest.Resources`, sorted by the key `<name>.v<version>` (action) or `<name>` (resource) in byte order. A module with no section has no page.
   - **README.md**: `# Darkpanel API reference`; a paragraph saying the reference is generated from the action schemas and that `openapi.json` is the OpenAPI 3.1 document the controller serves at `/api/v1/openapi.json`; `## Authentication` with the table `| Scheme | Type | Credential | Description |` over the document's `securitySchemes` in name order (credential: for `apiKey`, ``<in> `<name>` ``, for example ``cookie `__Host-darkpanel_session` ``; for `http`, ``` `Authorization: <Scheme>` header ``` with the scheme's first letter in upper case, for example ``` `Authorization: Bearer` header ```; description through `cell`); `## Common components` with `### Plan`, `### Change`, `### ApplyRequest`, `### Problem` in that order, each followed by `table` of that component (empty text `No fields.`); `## Routes` with the table `| Method | Path | Authentication | Summary |`, one row per operation without an `operationId`, sorted by path then method, method in upper case, the path in backticks, Authentication `none` when its `security` list is empty and `session or token` otherwise; `## Modules` with one `- [<id>](<id>.md)` line per page. Sections are separated by one blank line and the file ends with a newline.

3. `internal/apidocs/apidocsgen/main.go`: package comment (`Command apidocsgen writes the API reference into docs/api. Run it through make api-docs; ...`), a `-out` flag defaulting to `docs/api`, the shape of `schemagen/main.go`: build `registry.Build()`, call `apidocs.Generate(reg, api.Routes())`, then `apidocs.Write(out, files)`; on error print `apidocsgen: <error>` to standard error and exit 1.

4. `Makefile`: add `api-docs` to the `.PHONY` line and, after the `schemas` target, add this configuration (a tab indents the command):

```make
# api-docs regenerates docs/api from the registry; TestAdAPIDocsCurrent fails when it is stale.
api-docs:
	go run ./internal/apidocs/apidocsgen -out docs/api
```

5. Generate the committed reference: `mkdir -p /root/.cache/w9-tmp/3 && TMPDIR=/root/.cache/w9-tmp/3 GOTOOLCHAIN=local timeout 300 make api-docs`. It writes `docs/api/openapi.json`, `README.md` and one page per module with an operation: `core.md`, `hostinfo.md`, `firewall.md`, `docker.md`, `tailscale.md`, `tailnet.md`, `derp.md`, `npm.md`, `cloudflare.md`, `panel.md` (12 files). Do not edit them by hand. `ci.yml` does not change: the `go` job already runs `TestAdAPIDocsCurrent` through `make test-go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
export TMPDIR=/root/.cache/w9-tmp/3 GOTOOLCHAIN=local
timeout 300 go test -count=1 -v ./internal/apidocs/
ls docs/api | wc -l
cp -r docs/api /root/.cache/w9-tmp/3/api-before && timeout 300 make api-docs && diff -r /root/.cache/w9-tmp/3/api-before docs/api
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/apidocs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every `TestAd*` test passes, including `TestAdAPIDocsCurrent`; `12`; the second `make api-docs` leaves `docs/api` identical (`diff -r` prints nothing); `go vet` prints nothing; every line of the full suite is `ok` or `?`; `0 issues.`; no output from `go mod tidy -diff`. Quote the results. Then `rm -rf /root/.cache/w9-tmp/3` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/apidocs Makefile docs/api
git commit -m "feat(apidocs): generate the API reference"
```

### Task 4: cmd: harness --state-dir and selectPaths call sites

**Files:**
- Create: `cmd/darkpanel/paths_harness.go` (`//go:build harness`)
- Create: `cmd/darkpanel/paths_harness_test.go` (`//go:build harness`)
- Modify: `cmd/darkpanel/paths_test.go` (add the call-site pins and their imports)
- Modify: `internal/update/version_test.go` (`TestVersionCompare`, `TestInChannel`, `TestSteps`, the `stepReleases` table)

**Interfaces:**
- Consumes: read, not edited: `cmd/darkpanel/main.go` (`newRoot`, `newServeCmd`), `agent.go`, `update.go` (`withUpdater`, `rollbackFallback`), `harness_crash.go` (the build-tag file pattern), `internal/paths/paths.go`, `internal/update/version.go`. The frozen hooks `selectPaths func(*cobra.Command, bool) (paths.Paths, error)` and `addRootFlags func(*cobra.Command)` of `cmd/darkpanel/paths.go`, `paths.Under` and `paths.Resolve`.
- Produces: Contracts "Harness state directory": the package variable `harnessStateDir` and the persistent `--state-dir` flag, present only in a `harness` build. Task 21 starts `darkpanel` processes with `--state-dir` and build them with tags `harnesskeys,harness`.

**Items:** 39, 40, 107, 248

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Write the failing test**

Create `cmd/darkpanel/paths_harness_test.go`. It runs only under `-tags harness`. The four tests cover the selection rule (including the nil command of the update rollback fallback, register row 248), the flag, and the flag reaching a command. They never call `rollbackFallback`: a wrong selection there would execute `/usr/local/lib/darkpanel/darkpanel.prev` of this live host, so the nil-command rule is tested through `selectPaths(nil, false)` alone.

```go
//go:build harness

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/paths"
)

// sdStateDir sets harnessStateDir for one test and clears it afterwards.
func sdStateDir(t *testing.T, dir string) {
	t.Helper()
	old := harnessStateDir
	t.Cleanup(func() { harnessStateDir = old })
	harnessStateDir = dir
}

func TestSdSelectPathsUnderStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	dir := t.TempDir()
	cmd := &cobra.Command{Use: "run"}
	tests := []struct {
		name string
		dir  string
		cmd  *cobra.Command
		dev  bool
		want paths.Paths
		err  string
	}{
		{"state dir for a command", dir, cmd, false, paths.Under(dir), ""},
		{"state dir for the nil command of the rollback fallback", dir, nil, false, paths.Under(dir), ""},
		{"state dir with --dev", dir, cmd, true, paths.Paths{}, "--state-dir and --dev cannot be combined"},
		{"no state dir, production", "", cmd, false, paths.Production(), ""},
		{"no state dir, nil command", "", nil, false, paths.Production(), ""},
		{"no state dir, --dev", "", cmd, true, paths.Under(filepath.Join(state, "darkpanel-dev")), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdStateDir(t, tt.dir)
			got, err := selectPaths(tt.cmd, tt.dev)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("selectPaths error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("selectPaths = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestSdStateDirFlagIsPersistent(t *testing.T) {
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	flag := root.PersistentFlags().Lookup("state-dir")
	if flag == nil {
		t.Fatal("the root command has no persistent --state-dir flag")
	}
	if want := "harness only: keep every path under this directory"; flag.Usage != want {
		t.Errorf("--state-dir usage = %q, want %q", flag.Usage, want)
	}
	for _, path := range [][]string{{"serve"}, {"agent", "run"}, {"update", "recover"}} {
		if cmd, _, err := root.Find(path); err != nil || cmd.Flag("state-dir") == nil {
			t.Errorf("%s does not inherit --state-dir: %v, %v", strings.Join(path, " "), cmd, err)
		}
	}
}

func TestSdStateDirReachesTheCommand(t *testing.T) {
	served := fakeServe(t, 1000, nil)
	dir := t.TempDir()
	t.Cleanup(func() { harnessStateDir = "" })
	if out, code := runArgs("serve", "--state-dir", dir); code != cli.ExitOK {
		t.Fatalf("serve --state-dir: exit %d: %s", code, out)
	}
	if len(*served) != 1 || (*served)[0].Paths != paths.Under(dir) {
		t.Errorf("serve --state-dir served %+v, want the paths under %s", *served, dir)
	}
	out, code := runArgs("serve", "--dev", "--state-dir", dir)
	if code != cli.ExitError || !strings.Contains(out, "--state-dir and --dev cannot be combined") || len(*served) != 1 {
		t.Errorf("serve --dev --state-dir: exit %d, output %q, served %d times; want exit %d, the refusal and no second run",
			code, out, len(*served), cli.ExitError)
	}
}
```

- [ ] **Step 2: Extend the test `cmd/darkpanel/paths_test.go`**

Register row 107: no test pins which command each `selectPaths` call site passes. Replace the import block of `cmd/darkpanel/paths_test.go` with this one, then append the code below to the end of the file. `sdRecord` wraps whatever `selectPaths` the test already installed (`fakeAgent`, `fakeUpdate`, `fakeServe`'s production default), so each command still runs against its own fakes; the table pins the command path and the `dev` flag of every call. These tests describe behaviour the base already has, so they pass before Step 6; Step 5 shows that they bite.

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/paths"
)
```

```go

// sdRecord wraps the current selectPaths so that it records "<command path>
// dev=<dev>" for every call, "<nil>" in place of the command path for a nil
// command, and answers through the function it wrapped.
func sdRecord(t *testing.T) *[]string {
	t.Helper()
	inner := selectPaths
	t.Cleanup(func() { selectPaths = inner })
	var calls []string
	selectPaths = func(cmd *cobra.Command, dev bool) (paths.Paths, error) {
		name := "<nil>"
		if cmd != nil {
			name = cmd.CommandPath()
		}
		calls = append(calls, fmt.Sprintf("%s dev=%v", name, dev))
		return inner(cmd, dev)
	}
	return &calls
}

func TestSdCallSitesPassTheirCommand(t *testing.T) {
	agent := func(t *testing.T) { fakeAgent(t, 1000) }
	update := func(t *testing.T) { fakeUpdate(t, 0) }
	tests := []struct {
		name string
		seam func(t *testing.T)
		args []string
		want string
	}{
		{"serve", func(t *testing.T) {
			fakeServe(t, 1000, nil)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_RUNTIME_DIR", "")
		}, []string{"serve", "--dev"}, "darkpanel serve dev=true"},
		{"agent run", agent, []string{"agent", "run", "--dev"}, "darkpanel agent run dev=true"},
		{"agent enroll", agent, []string{"agent", "enroll", "--dev"}, "darkpanel agent enroll dev=true"},
		{"agent check", agent, []string{"agent", "check", "--dev"}, "darkpanel agent check dev=true"},
		{"agent task", func(t *testing.T) { taskAgentDB(t, fakeAgent(t, 1000).dev) },
			[]string{"agent", "task", "--dev", "01JTASK", "apply", "1"}, "darkpanel agent task dev=true"},
		{"update run", update, []string{"update", "run", "01HZY6TX"}, "darkpanel update run dev=false"},
		{"update recover", update, []string{"update", "recover"}, "darkpanel update recover dev=false"},
		{"update rollback --direct", update, []string{"update", "rollback", "--direct"}, "darkpanel update rollback dev=false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.seam(t)
			calls := sdRecord(t)
			stdout, stderr, code := runStdin(strings.NewReader("a-token\n"), tt.args...)
			if !slices.Equal(*calls, []string{tt.want}) {
				t.Errorf("darkpanel %s: selectPaths calls %q, want exactly %q (exit %d, stdout %q, stderr %q)",
					strings.Join(tt.args, " "), *calls, tt.want, code, stdout, stderr)
			}
		})
	}
}

func TestSdRollbackFallbackPassesNoCommand(t *testing.T) {
	root := paths.Under(t.TempDir())
	saved := selectPaths
	t.Cleanup(func() { selectPaths = saved })
	selectPaths = func(*cobra.Command, bool) (paths.Paths, error) { return root, nil }
	calls := sdRecord(t)
	// root holds no previous binary, so the fallback stops before it runs anything.
	_, err := rollbackFallback(io.Discard)(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "no previous binary") {
		t.Errorf("the rollback fallback returned %v, want the refusal that there is no previous binary", err)
	}
	if want := []string{"<nil> dev=false"}; !slices.Equal(*calls, want) {
		t.Errorf("the rollback fallback called selectPaths %q, want %q", *calls, want)
	}
}
```

- [ ] **Step 3: Extend the tests `internal/update/version_test.go::TestVersionCompare` and `::TestSteps`**

Register row 39 asks for a unit test of pre-release ordering. Make four edits to `internal/update/version_test.go`:

1. In `TestVersionCompare`, in the `ordered` slice, replace `"1.0.0-rc.1", "1.0.0",` with `"1.0.0-rc.1", "1.0.0-rc.2", "1.0.0-rc.10", "1.0.0",` (the loop checks every pair, so `rc.2 < rc.10` is checked in both directions).
2. At the top of the `stepReleases` literal add these four entries (release candidates are published in the `beta` channel, like the other pre-releases of the table):

```go
	{Version: "1.0.0", Channel: "stable"},
	{Version: "1.0.0-rc.10", Channel: "beta"},
	{Version: "1.0.0-rc.1", Channel: "beta"},
	{Version: "1.0.0-rc.2", Channel: "beta"},
```

3. In `TestInChannel`, because `stepReleases` grew, change the two expectations to these lines:

```go
	stable := []string{"1.0.0", "1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.4.0", "1.4.1", "1.5.0", "1.8.0", "2.0.0"}
	beta := []string{"1.0.0-rc.1", "1.0.0-rc.2", "1.0.0-rc.10", "1.0.0", "1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.3.3-beta.1", "1.4.0", "1.4.1", "1.5.0", "1.6.0-beta.1", "1.8.0", "2.0.0"}
```

4. In `TestSteps`, insert these three rows before the row `"forced downgrade of one minor"`:

```go
		{"release candidate to the next, numerically", "1.0.0-rc.2", "1.0.0-rc.10", "beta", false, []string{"1.0.0-rc.10"}, ""},
		{"release candidate to its release", "1.0.0-rc.1", "1.0.0", "beta", false, []string{"1.0.0"}, ""},
		{"the later release candidate to the earlier", "1.0.0-rc.10", "1.0.0-rc.2", "beta", false, nil, errcode.UpdateDowngradeRefused},
```

- [ ] **Step 4: Run the tests**

Run:

```bash
mkdir -p /root/.cache/w9-tmp/4 && export TMPDIR=/root/.cache/w9-tmp/4 GOTOOLCHAIN=local
timeout 600 go test -tags harness -count=1 -run 'TestSd' ./cmd/darkpanel/
timeout 600 go test -count=1 -run 'TestSdCallSitesPassTheirCommand|TestSdRollbackFallbackPassesNoCommand' ./cmd/darkpanel/
timeout 300 go test -count=1 -run 'TestVersionCompare|TestInChannel|TestSteps' ./internal/update/
```

Expected: the first is RED, a build failure naming `undefined: harnessStateDir` (four occurrences in `paths_harness_test.go`) and `FAIL ... [build failed]`; the other two PASS, because they pin behaviour the base already has. Quote all three outputs.

- [ ] **Step 5: Show that the pins bite**

Make each of these temporary edits, run the named test, see it FAIL as described, and undo the edit with `git checkout -- <file>` before the next one. None of these edits is committed, and `git status --short` at the end lists only this task's files.

1. In `cmd/darkpanel/update.go`, in `withUpdater`, change `selectPaths(cmd, false)` to `selectPaths(nil, false)`. `go test -count=1 -run TestSdCallSitesPassTheirCommand ./cmd/darkpanel/` fails for `update_run`, `update_recover` and `update_rollback_--direct`, each reporting `"<nil> dev=false"` where `"darkpanel update ... dev=false"` was wanted.
2. In `cmd/darkpanel/agent.go`, in `newAgentEnrollCmd`, change `selectPaths(cmd, dev)` to `selectPaths(cmd, false)`. The same test fails for `agent_enroll`, reporting `dev=false` where `dev=true` was wanted.
3. In `internal/update/version.go`, in `comparePrerelease`, replace the body of `case an && bn:` with `return strings.Compare(a, b)`. `go test -count=1 -run 'TestVersionCompare|TestInChannel|TestSteps' ./internal/update/` fails: `1.0.0-rc.2.Compare(1.0.0-rc.10) = 1, want -1`, the `beta` list of `TestInChannel`, and the two `rc` rows of `TestSteps`.

- [ ] **Step 6: Implement**

Create `cmd/darkpanel/paths_harness.go` with the build constraint `//go:build harness` (as `harness_crash.go` has `//go:build harnesscrash`) and package `main`:

1. A package variable `harnessStateDir string`.
2. An `init` that replaces both frozen hooks:
   - `addRootFlags` registers on the root's persistent flags the string flag `state-dir`, default `""`, usage exactly `harness only: keep every path under this directory`, bound with `StringVar` to `harnessStateDir`. Binding to the package variable, not to a value read from a command, is what lets the nil-command rollback fallback honour the flag (register row 248). `StringVar` sets the variable to its default at registration, so every `newRoot` starts from `""`.
   - `selectPaths` ignores its command argument. With `harnessStateDir == ""` it returns `paths.Resolve(dev)`. With `harnessStateDir != ""` and `dev` true it returns the zero `paths.Paths` and the error `--state-dir and --dev cannot be combined` (the string is fixed by Contracts; the CLI prints it as `darkpanel: --state-dir and --dev cannot be combined`, and the test checks a substring of the printed output). With `harnessStateDir != ""` and `dev` false it returns `paths.Under(harnessStateDir)`, for a nil command too.
3. No change to `paths.go` (frozen) or any other production file. A build without the `harness` tag has no `--state-dir` flag.

- [ ] **Step 7: Run the tests to verify they pass**

Run:

```bash
export TMPDIR=/root/.cache/w9-tmp/4 GOTOOLCHAIN=local
timeout 600 go test -tags harness -count=1 -v -run 'TestSd' ./cmd/darkpanel/
timeout 600 go test -tags harness -count=1 ./cmd/darkpanel/
timeout 300 go vet ./... && timeout 300 go vet -tags harness ./cmd/...
timeout 600 go build ./... && timeout 600 go build -tags harness -o /dev/null ./cmd/darkpanel/
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./cmd/darkpanel/ ./internal/update/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every `TestSd*` test passes under the `harness` tag; the whole `cmd/darkpanel` package passes under the tag, and the full `go test ./...` run passes it without the tag; `go vet` prints nothing; every line of the full suite is `ok` or `?`; `0 issues.` (the linter builds with the `harness` tag); no output from `go mod tidy -diff`. Quote the results. Then `rm -rf /root/.cache/w9-tmp/4` and `go clean -testcache`.

- [ ] **Step 8: Commit**

```bash
git add internal/update/version_test.go
git commit -m "test(update): pin release candidate ordering"
git add cmd/darkpanel/paths_test.go
git commit -m "test(cmd): pin the command each selectPaths site passes"
git add cmd/darkpanel/paths_harness.go cmd/darkpanel/paths_harness_test.go
git commit -m "feat(cmd): add the harness --state-dir flag"
```

### Task 5: fuzz: platform clients and parsers

**Files:**
- Modify: `internal/platform/netfilter/parse_fuzz_test.go` (extends `FuzzParse`)
- Create: `internal/platform/netstat/fuzz_test.go` (`FuzzParseSocketLine`, `FuzzParseNetlink`)
- Create: `internal/platform/fuzz_test.go` (`FuzzMarkedBlocks`)
- Create: `internal/platform/dockerapi/fuzz_test.go` (`FuzzLogStream`, `FuzzCompareVersions`)
- Create: `internal/platform/tslocal/fuzz_test.go` (`FuzzPrefsJSON`, `TestFz5PrefsCaseVariantKeys`)
- Modify: `internal/platform/tslocal/client.go` (`Prefs.MarshalJSON`, Step 4)
- Modify only for a defect a run finds (W9-R3): the other non-frozen production files of `netfilter`, `netstat`, `dockerapi` and `tslocal`. `internal/platform/blocks.go` and the other non-test files directly in `internal/platform/` are frozen: a defect there is reported with its failing input, not fixed.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 5; "Fuzz runner" (`scripts/fuzz.sh --check`) and "Inventory format" (the rows of `docs/security/parsers.md`), both written by Task 1 and part of this wave's base; "Test prefixes" (`fz5`). Existing test helpers: `psSample` (`netfilter/parse_test.go`), `skTCP`, `skTCP6`, `skUDP` (`netstat/sockets_test.go`), `rtMessage`, `rtRoute` (`netstat/routes_test.go`), `stFrame` (`dockerapi/streams_test.go`). Existing production names: `nlAttr`, `nlUint32` (`netstat/routes.go`), `parseEntry`, `parseAddrPort`, `parseNetlink`, `parseRoute`, `scanMarkers`, `replaceBlock`, `removeBlock`, `splitLines`, `LogStream`, `maxFrame`, `compareVersions`, `parseVersion`, `Prefs`, `errExitNodeBoth`.
- Produces: the fuzz targets `FuzzParse` (extended), `FuzzParseSocketLine`, `FuzzParseNetlink`, `FuzzMarkedBlocks`, `FuzzLogStream`, `FuzzCompareVersions` and `FuzzPrefsJSON`, under the package and name of their inventory rows (Contracts "Fuzz targets"); no exported identifier.

**Items:** 38

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check. Task 1's inventory already lists the six new targets, so the check names each one missing:

```bash
mkdir -p /root/.cache/w9-tmp/5 && export TMPDIR=/root/.cache/w9-tmp/5 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg 'no target (FuzzParseSocketLine|FuzzParseNetlink|FuzzMarkedBlocks|FuzzLogStream|FuzzCompareVersions|FuzzPrefsJSON)$'
```

Expected: these six lines, in the inventory's order, and no line for `FuzzParse` or `FuzzParseRestore` of `./internal/platform/netfilter`:

```text
fuzz: ./internal/platform/netstat: no target FuzzParseSocketLine
fuzz: ./internal/platform/netstat: no target FuzzParseNetlink
fuzz: ./internal/platform: no target FuzzMarkedBlocks
fuzz: ./internal/platform/dockerapi: no target FuzzLogStream
fuzz: ./internal/platform/dockerapi: no target FuzzCompareVersions
fuzz: ./internal/platform/tslocal: no target FuzzPrefsJSON
```

Then create the four files. Each target asserts the property of its Contracts row besides not panicking, and a little more where the code gives it for free; each file keeps its own `fz5Head`, which cuts the input quoted in a failure message to 64 bytes.

`internal/platform/netstat/fuzz_test.go` reuses the table constants of `sockets_test.go` as seeds and the message builders of `routes_test.go`. `FuzzParseNetlink` takes the sequence number from the datagram's own first header, so most inputs reach the message loop instead of failing on a sequence mismatch.

`internal/platform/netstat/fuzz_test.go`:

```go
package netstat

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// fz5HasFamily reports whether a is an address of the table family f. An
// IPv6 table prints an IPv4-mapped peer as an IPv6 address.
func fz5HasFamily(a netip.Addr, f Family) bool {
	if f == IPv6 {
		return a.Is6()
	}
	return a.Is4()
}

// FuzzParseSocketLine feeds one /proc/net socket-table line, and each of its
// fields as an address:port, to parseEntry and parseAddrPort for either
// family and either protocol: neither may panic, and an accepted entry or
// address is of the family that was asked for (R74).
func FuzzParseSocketLine(f *testing.F) {
	for _, table := range []struct {
		text    string
		v6, udp bool
	}{{skTCP, false, false}, {skTCP6, true, false}, {skUDP, false, true}} {
		for _, line := range strings.Split(table.text, "\n")[1:] {
			if strings.TrimSpace(line) != "" {
				f.Add(line, table.v6, table.udp)
			}
		}
	}
	f.Fuzz(func(t *testing.T, line string, v6, udp bool) {
		family, proto := IPv4, ProtoTCP
		if v6 {
			family = IPv6
		}
		if udp {
			proto = ProtoUDP
		}
		fields := strings.Fields(line)
		if s, err := parseEntry(fields, proto, family); err == nil {
			if s.Family != family || s.Proto != proto || !fz5HasFamily(s.Local.Addr(), family) || !fz5HasFamily(s.Remote.Addr(), family) {
				t.Fatalf("entry %+v of %q is not of family %d and protocol %s", s, fz5Head(line), family, proto)
			}
			if udp {
				switch s.State {
				case StateEstablished, StateClose, StateUnknown:
				default:
					t.Fatalf("UDP state %q of %q", s.State, fz5Head(line))
				}
			}
		}
		for _, field := range append([]string{line}, fields...) {
			if ap, err := parseAddrPort(field, family); err == nil && !fz5HasFamily(ap.Addr(), family) {
				t.Fatalf("address %q read as %v is not of family %d", fz5Head(field), ap, family)
			}
		}
	})
}

// fz5CheckRoute fails when parseRoute accepts m with a prefix length other
// than the header's rtm_dst_len.
func fz5CheckRoute(t *testing.T, m []byte) {
	t.Helper()
	r, err := parseRoute(m)
	if err != nil {
		return
	}
	if r.Dst.Bits() != int(m[1]) {
		t.Fatalf("route of %q has prefix length %d, header says %d", fz5Head(string(m)), r.Dst.Bits(), m[1])
	}
}

// FuzzParseNetlink feeds one datagram to parseNetlink, and its messages and
// the datagram itself to parseRoute: none may panic, every returned message
// is no longer than the input and an error returns no messages, and an
// accepted route's prefix length is the header's rtm_dst_len (R74).
func FuzzParseNetlink(f *testing.F) {
	route4 := rtRoute(unix.AF_INET, 24, unix.RT_TABLE_MAIN, RouteProtoKernel, unix.RTN_UNICAST,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("192.0.2.0").AsSlice()), nlAttr(unix.RTA_OIF, nlUint32(2)))
	route6 := rtRoute(unix.AF_INET6, 64, unix.RT_TABLE_MAIN, RouteProtoRA, unix.RTN_UNICAST,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("2001:db8::").AsSlice()), nlAttr(unix.RTA_GATEWAY, netip.MustParseAddr("fe80::1").AsSlice()))
	f.Add(append(rtMessage(unix.RTM_NEWROUTE, 7, route4), rtMessage(unix.NLMSG_DONE, 7, nlUint32(0))...))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, route6))
	f.Add(rtMessage(unix.NLMSG_ERROR, 7, nlUint32(1<<32-101)))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 0xff, 0, 1, 0)))
	f.Add(rtMessage(unix.RTM_NEWROUTE, 7, rtRoute(unix.AF_INET, 33, 254, 0, 1)))
	f.Add([]byte{1, 2, 3})
	f.Add(route4)
	f.Fuzz(func(t *testing.T, b []byte) {
		var seq uint32
		if len(b) >= unix.SizeofNlMsghdr {
			seq = binary.NativeEndian.Uint32(b[8:])
		}
		msgs, _, err := parseNetlink(b, seq)
		if err != nil && msgs != nil {
			t.Fatalf("messages returned with error %v for %q", err, fz5Head(string(b)))
		}
		for _, m := range msgs {
			if len(m) > len(b) {
				t.Fatalf("message of %d bytes in a datagram of %d: %q", len(m), len(b), fz5Head(string(b)))
			}
			fz5CheckRoute(t, m)
		}
		fz5CheckRoute(t, b)
	})
}
```

`internal/platform/fuzz_test.go`. `replaceBlock` ends a file that lacks a final newline before it appends the block, so removing the block gives the file back with that newline; the target asserts exactly that form. The marker prefix is one of the three the modules declare, since an arbitrary prefix could hold a newline and no marker would ever match:

```go
package platform

import (
	"bytes"
	"testing"
)

// fz5Head returns at most the first 64 bytes of b, for a failure message.
func fz5Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzMarkedBlocks feeds arbitrary file content and block bodies to
// scanMarkers, replaceBlock, removeBlock and splitLines: joining the lines of
// a file gives the file back; a file scanMarkers refuses is refused by both
// edits; replaceBlock's output passes scanMarkers and holds the block, and
// replacing again with the same body changes nothing; removing a block that
// replaceBlock just added to a file without one gives the file back, with the
// final newline replaceBlock supplies (R74).
func FuzzMarkedBlocks(f *testing.F) {
	const module, blockID = "fw", "rules"
	f.Add([]byte(""), []byte("allow 22\n"), uint8(0))
	f.Add([]byte("a"), []byte("x"), uint8(0))
	f.Add([]byte("a\n# BEGIN DARKPANEL fw/rules\nold\n# END DARKPANEL fw/rules\nb\n"), []byte("new\n"), uint8(0))
	f.Add([]byte("# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n"), []byte("x\n"), uint8(1))
	f.Add([]byte("// BEGIN DARKPANEL fw/rules\n// END DARKPANEL fw/rules"), []byte(""), uint8(1))
	f.Add([]byte("# BEGIN DARKPANEL fw/rules\n"), []byte("x\n"), uint8(0))
	f.Add([]byte("# BEGIN DARKPANEL fw/rules \n# END DARKPANEL fw/rules\n"), []byte(nil), uint8(0))
	f.Add([]byte("a\n"), []byte("# END DARKPANEL fw/rules\n"), uint8(0))
	f.Fuzz(func(t *testing.T, content, body []byte, which uint8) {
		prefix := []string{"#", "//", ";"}[int(which)%3]
		id := module + "/" + blockID
		lines := splitLines(content)
		if !bytes.Equal(bytes.Join(lines, nil), content) {
			t.Fatalf("splitLines does not join back to %q", fz5Head(content))
		}
		spans, scanErr := scanMarkers(lines, prefix)
		_, had := spans[id]
		added, addErr := replaceBlock(content, prefix, module, blockID, body)
		removed, rmErr := removeBlock(content, prefix, module, blockID)
		if scanErr != nil {
			if addErr == nil || rmErr == nil {
				t.Fatalf("a file scanMarkers refuses was edited: %q", fz5Head(content))
			}
			return
		}
		if rmErr != nil {
			t.Fatalf("removeBlock refused a file scanMarkers accepts: %q: %v", fz5Head(content), rmErr)
		}
		if rs, err := scanMarkers(splitLines(removed), prefix); err != nil {
			t.Fatalf("removeBlock left markers scanMarkers refuses: %q", fz5Head(content))
		} else if _, ok := rs[id]; ok {
			t.Fatalf("removeBlock left the block in %q", fz5Head(content))
		}
		if addErr != nil {
			return
		}
		if as, err := scanMarkers(splitLines(added), prefix); err != nil {
			t.Fatalf("replaceBlock output is refused by scanMarkers: %q", fz5Head(content))
		} else if _, ok := as[id]; !ok {
			t.Fatalf("replaceBlock output lacks the block: %q", fz5Head(content))
		}
		again, err := replaceBlock(added, prefix, module, blockID, body)
		if err != nil || !bytes.Equal(again, added) {
			t.Fatalf("replaceBlock is not idempotent for %q: %v", fz5Head(content), err)
		}
		if had {
			return
		}
		if !bytes.HasPrefix(added, content) {
			t.Fatalf("replaceBlock changed the file it appended to: %q", fz5Head(content))
		}
		want := content
		if len(content) > 0 && content[len(content)-1] != '\n' {
			want = append(bytes.Clone(content), '\n')
		}
		if back, err := removeBlock(added, prefix, module, blockID); err != nil || !bytes.Equal(back, want) {
			t.Fatalf("removing the added block does not give %q back: %v", fz5Head(content), err)
		}
	})
}
```

`internal/platform/dockerapi/fuzz_test.go`. A multiplexed header can announce a frame of up to `maxFrame` (8 MiB), which `Next` allocates before it reads, so a run is slower than the other targets; that is expected:

```go
package dockerapi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// FuzzLogStream feeds arbitrary bytes to a log stream, multiplexed or raw:
// Next never panics, no frame is longer than the stream's frame limit or
// carries another stream than stdout or stderr, the frames together hold no
// more bytes than the input, and the stream ends (R74).
func FuzzLogStream(f *testing.F) {
	oversized := make([]byte, 8)
	oversized[0] = 1
	binary.BigEndian.PutUint32(oversized[4:], maxFrame+1)
	f.Add(append(stFrame(1, "out\n"), stFrame(2, "err\n")...), true)
	f.Add([]byte("plain tty output\n"), false)
	f.Add(stFrame(1, "abcdef")[:10], true)
	f.Add(stFrame(1, "abcdef")[:8], true)
	f.Add([]byte{1, 0, 0}, true)
	f.Add(stFrame(7, "x"), true)
	f.Add(oversized, true)
	f.Fuzz(func(t *testing.T, in []byte, multiplexed bool) {
		r := bytes.NewReader(in)
		s := &LogStream{body: io.NopCloser(r), r: bufio.NewReader(r), multiplexed: multiplexed}
		total := 0
		for range len(in) + 1 {
			frame, err := s.Next()
			if err != nil {
				return
			}
			if len(frame.Data) > maxFrame {
				t.Fatalf("frame of %d bytes exceeds the limit for %q", len(frame.Data), fz5Head(string(in)))
			}
			if frame.Stream != "stdout" && frame.Stream != "stderr" {
				t.Fatalf("frame of stream %q for %q", frame.Stream, fz5Head(string(in)))
			}
			if total += len(frame.Data); total > len(in) {
				t.Fatalf("frames hold %d bytes of an input of %d: %q", total, len(in), fz5Head(string(in)))
			}
		}
		t.Fatalf("no end of stream for %q", fz5Head(string(in)))
	})
}

// FuzzCompareVersions feeds two arbitrary strings to compareVersions and
// parseVersion: they never panic, a pair of which one fails to parse fails
// both ways, and a pair that parses compares to opposite signs when swapped
// and to zero when equal (R74).
func FuzzCompareVersions(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.44", "1.49"}, {"1.49", "1.49"}, {"1.50", "1.9"}, {"2.0", "1.99"}, {"1.-0", "1.0"},
		{"+1.+2", "1.2"}, {"1.2.3", "1.2"}, {"", "1.44"}, {"1.", ".1"}, {"99999999999999999999.1", "1.1"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		ab, errAB := compareVersions(a, b)
		ba, errBA := compareVersions(b, a)
		if (errAB == nil) != (errBA == nil) {
			t.Fatalf("compareVersions(%q, %q) and its swap disagree on whether both parse: %v, %v", fz5Head(a), fz5Head(b), errAB, errBA)
		}
		if errAB != nil {
			return
		}
		if ab != -ba || ab < -1 || ab > 1 || (a == b && ab != 0) {
			t.Fatalf("compareVersions(%q, %q) = %d, swapped %d", fz5Head(a), fz5Head(b), ab, ba)
		}
	})
}
```

`internal/platform/tslocal/fuzz_test.go`. `encoding/json` matches member names without regard to case and lets the later of two spellings win. `Prefs.MarshalJSON` lays the modelled fields over the kept members by exact name, so a document holding both `hostname` and `Hostname` marshals to both spellings, and the next read takes the one that sorts last. The seed `{"hostname":"a","Hostname":"b"}` and `TestFz5PrefsCaseVariantKeys` pin it; they fail on the current tree:

```go
package tslocal

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// fz5Modelled is p without the members kept as read, which are not modelled.
func fz5Modelled(p Prefs) Prefs {
	p.raw = nil
	return p
}

// TestFz5PrefsCaseVariantKeys: tailscaled's decoder matches member names
// without regard to case and lets the later one win, so a document holding
// two spellings of a modelled member decodes to the same value after it is
// encoded and decoded again.
func TestFz5PrefsCaseVariantKeys(t *testing.T) {
	var first Prefs
	if err := json.Unmarshal([]byte(`{"hostname":"a","Hostname":"b"}`), &first); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second Prefs
	if err := json.Unmarshal(out, &second); err != nil {
		t.Fatal(err)
	}
	if first.Hostname != "b" || second.Hostname != first.Hostname {
		t.Fatalf("Hostname %q, then %q after %s", first.Hostname, second.Hostname, out)
	}
}

// FuzzPrefsJSON feeds arbitrary JSON to Prefs.UnmarshalJSON: input it accepts
// marshals, or is refused only for naming an exit node by both ID and
// address, and the marshalled form unmarshals again to the same modelled
// fields (R74).
func FuzzPrefsJSON(f *testing.F) {
	f.Add(`{"ControlURL":"https://controlplane.tailscale.com","RouteAll":false,"ExitNodeID":"","ExitNodeIP":"",` +
		`"CorpDNS":true,"RunSSH":false,"WantRunning":false,"LoggedOut":true,"ShieldsUp":false,"AdvertiseTags":null,` +
		`"Hostname":"","AdvertiseRoutes":null,"NetfilterMode":2,"NoSNAT":false,"AutoUpdate":{"Check":true,"Apply":null},"Config":null}`)
	f.Add(`{"ExitNodeIP":"100.64.0.1","AdvertiseRoutes":["10.0.0.0/24","0.0.0.0/0"],"AdvertiseTags":["tag:web"]}`)
	f.Add(`{"ExitNodeID":"n1","ExitNodeIP":"100.64.0.1"}`)
	f.Add(`{"hostname":"a","Hostname":"b"}`)
	f.Add(`{"Hostname":"a","Hostname":"b"}`)
	f.Add(`{}`)
	f.Add(`null`)
	f.Add(`[]`)
	f.Fuzz(func(t *testing.T, in string) {
		var first Prefs
		if err := json.Unmarshal([]byte(in), &first); err != nil {
			return
		}
		out, err := json.Marshal(first)
		if err != nil {
			if !errors.Is(err, errExitNodeBoth) || first.ExitNodeID == "" || !first.ExitNodeIP.IsValid() {
				t.Fatalf("accepted prefs %q do not marshal: %v", fz5Head(in), err)
			}
			return
		}
		var second Prefs
		if err := json.Unmarshal(out, &second); err != nil {
			t.Fatalf("marshalled prefs of %q do not unmarshal: %v", fz5Head(in), err)
		}
		if !reflect.DeepEqual(fz5Modelled(first), fz5Modelled(second)) {
			t.Fatalf("modelled fields of %q changed over a round trip: %q", fz5Head(in), fz5Head(string(out)))
		}
	})
}
```


- [ ] **Step 2: Extend the test `internal/platform/netfilter/parse_fuzz_test.go::FuzzParse`**

The extension seeds every save file of the four fixture trees and has `Key` read every parsed rule for both families, twice. It adds seeds and a property that no existing line fails today, so its RED is the seed count: run this before saving the file and see `5` (the four `testdata/*.save` files and `psSample`):

```bash
timeout 300 go test -count=1 -v -run '^FuzzParse$' ./internal/platform/netfilter | rg -c '^    --- PASS: FuzzParse/seed#'
```

The fixtures hold `testdata/*.save` (4), `testdata/ufw/*/{iptables,ip6tables}.save` (8), `internal/modules/firewall/dockerfw/testdata/capture/*/*/save/*.txt` (160; the capture's `save` is a directory of one file per family and table, `nft-v4-filter.txt` and so on, not a file) and `internal/modules/firewall/eval/testdata/{docker28,tailscale}/*.save` (5). `fz5Seeds` fails the target when a pattern matches no file, so a moved fixture directory is noticed. Replace the whole file with:

```go
package netfilter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fz5Seeds adds the content of every file matching a pattern as a seed. A
// pattern that matches nothing fails the target, so a fixture directory that
// moved is noticed instead of silently shrinking the corpus.
func fz5Seeds(f *testing.F, patterns ...string) {
	f.Helper()
	for _, pattern := range patterns {
		names, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", pattern)
		}
		for _, name := range names {
			b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under a testdata/ directory
			if err != nil {
				f.Fatal(err)
			}
			f.Add(string(b))
		}
	}
}

// FuzzParse feeds arbitrary input to Parse: it must never panic, a ruleset it
// accepts holds at least one table, each known and named once, each rule
// jumps to a declared chain or a contract target or is opaque, and Key reads
// every rule of it, for both families, without panicking and with the same
// answer twice (spec 1.8, R74).
func FuzzParse(f *testing.F) {
	fz5Seeds(f,
		filepath.Join("testdata", "*.save"),
		filepath.Join("testdata", "ufw", "*", "*.save"),
		filepath.Join("..", "..", "modules", "firewall", "dockerfw", "testdata", "capture", "*", "*", "save", "*.txt"),
		filepath.Join("..", "..", "modules", "firewall", "eval", "testdata", "*", "*.save"),
	)
	f.Add(psSample)
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		if len(rs.Tables) == 0 {
			t.Fatal("accepted input produced no tables")
		}
		seen := map[string]bool{}
		for _, tb := range rs.Tables {
			if !slices.Contains(Tables(), tb.Name) {
				t.Fatalf("table name %q", tb.Name)
			}
			if seen[tb.Name] {
				t.Fatalf("table %q appears twice", tb.Name)
			}
			seen[tb.Name] = true
			for _, c := range tb.Chains {
				for _, r := range c.Rules {
					if r.Target.Chain && tb.Chain(r.Target.Name) == nil {
						t.Fatalf("jump to undeclared chain %q", r.Target.Name)
					}
					if _, ok := targets[r.Target.Name]; r.Target.Name != "" && !r.Target.Chain && !ok && !r.Opaque() {
						t.Fatalf("unknown target %q not opaque", r.Target.Name)
					}
					for _, v6 := range []bool{false, true} {
						first := Key(r, v6)
						if second := Key(r, v6); first != second {
							t.Fatalf("Key of a rule of %q differs between two calls", in[:min(len(in), 64)])
						}
					}
				}
			}
		}
	})
}
```

After saving it, the same command prints `178` (4 + 8 + 160 + 5 seeds and `psSample`).

- [ ] **Step 3: Run the tests to verify the RED**

```bash
timeout 300 go test -count=1 -run '^(FuzzPrefsJSON|TestFz5PrefsCaseVariantKeys)$' ./internal/platform/tslocal
timeout 300 go test -count=1 -run '^Fuzz' ./internal/platform ./internal/platform/netfilter ./internal/platform/netstat ./internal/platform/dockerapi
```

Expected from the first command: `--- FAIL: TestFz5PrefsCaseVariantKeys` with a message that starts `Hostname "b", then "a" after {` and ends in the marshalled prefs holding both `"Hostname":"b"` and `"hostname":"a"`, and `--- FAIL: FuzzPrefsJSON/seed#3` with `modelled fields of "{\"hostname\":\"a\",\"Hostname\":\"b\"}" changed over a round trip`; every other seed passes. The second command prints `ok` for the four packages. A `FAIL` there is a finding: go to Step 4 item 2.

- [ ] **Step 4: Implement**

The task adds test code; it changes production code only where a run fails (W9-R3), and the first failure is known.

1. `Prefs.MarshalJSON` in `internal/platform/tslocal/client.go`, the defect the seed of Step 1 shows. When it lays the modelled fields over the kept members, it also drops every kept member whose name equals a modelled field's JSON name under `strings.EqualFold`, the comparison `encoding/json` itself applies. Members that match no modelled field stay exactly as read, so the existing `TestPrefsRoundTripKeepsUnmodelledFields` keeps passing, and the order of the two steps (kept members first, typed fields over them) does not change. Both failures of Step 3 then pass.
2. Any other failure of a seed run or of a Step 5 fuzz run is a defect (W9-R3). In a non-frozen file of these packages, fix it in this task: save the failing input under `<package>/testdata/fuzz/<Target>/` (Go writes it there on a fuzz failure), add a unit test named `TestFz5<Defect>` to the package's `fuzz_test.go`, and fix the code. In a frozen file (`internal/platform/blocks.go`, or another non-test file directly in `internal/platform/`) stop and report the failing input; the integrator fixes it on `main`.

- [ ] **Step 5: Run the tests to verify the GREEN**

```bash
mkdir -p /root/.cache/w9-tmp/5 && export TMPDIR=/root/.cache/w9-tmp/5 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go test -count=1 -run '^(Fuzz|TestFz5)' ./internal/platform ./internal/platform/netfilter ./internal/platform/netstat ./internal/platform/dockerapi ./internal/platform/tslocal
timeout 300 go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s -parallel 4 ./internal/platform/netfilter 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseSocketLine$' -fuzztime 30s -parallel 4 ./internal/platform/netstat 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseNetlink$' -fuzztime 30s -parallel 4 ./internal/platform/netstat 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzMarkedBlocks$' -fuzztime 30s -parallel 4 ./internal/platform 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzLogStream$' -fuzztime 30s -parallel 4 ./internal/platform/dockerapi 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzCompareVersions$' -fuzztime 30s -parallel 4 ./internal/platform/dockerapi 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzPrefsJSON$' -fuzztime 30s -parallel 4 ./internal/platform/tslocal 2>&1 | tail -n 1
git status --short -- '*/testdata/fuzz/*'
```

Expected: `ok` for the five packages; the last line of each fuzz run begins `ok` and names its package (a `FAIL` line is a finding: Step 4 item 2); the last command prints nothing unless a run found a defect. Quote the seven last lines in the report.

- [ ] **Step 6: Verify the task**

```bash
mkdir -p /root/.cache/w9-tmp/5 && export TMPDIR=/root/.cache/w9-tmp/5 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/platform ./internal/platform/netfilter ./internal/platform/netstat ./internal/platform/dockerapi ./internal/platform/tslocal
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/platform(/(netfilter|netstat|dockerapi|tslocal))?: ' || echo "no problem left for this task"
git status --short
rm -rf "$TMPDIR" && go clean -testcache && go clean -fuzzcache
```
Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; `golangci-lint` prints `0 issues.`; `go mod tidy -diff` prints nothing; the check filter prints `no problem left for this task` (the check's other lines name the targets of tasks outside this one); `git status --short` lists only this task's files and prints nothing under `testdata/fuzz/` unless a run found a defect (Step 4). Record the real output in the report; a line of the full-suite constraint you skip is named with its reason.

- [ ] **Step 7: Commit**

The `tslocal` fix goes first, in its own commit; a finding fixed under item 2 goes in a `fix` commit of its own as well, and its `testdata/fuzz/` file goes into the test commit below.

```bash
git add internal/platform/tslocal/client.go
git commit -m "fix(tslocal): keep one spelling of a prefs key"
git add internal/platform/netfilter/parse_fuzz_test.go internal/platform/netstat/fuzz_test.go internal/platform/fuzz_test.go internal/platform/dockerapi/fuzz_test.go internal/platform/tslocal/fuzz_test.go
git commit -m "test(platform): fuzz clients and parsers"
```


### Task 6: fuzz: docker module parsers

**Files:**
- Create: `internal/modules/docker/compose/fuzz_test.go` (`FuzzParse`)
- Create: `internal/modules/docker/fuzz_test.go` (`FuzzContainerDetail`, `FuzzParseAPIVersion`)
- Create: `internal/modules/docker/logfilter/fuzz_test.go` (`FuzzFilterChunking`)
- Modify only for a defect a run finds (W9-R3): `internal/modules/docker/compose/config.go`, `internal/modules/docker/inspect.go`, `internal/modules/docker/preflight.go`, `internal/modules/docker/logfilter/logfilter.go`.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 6; "Fuzz runner" and "Inventory format" (Task 1, in this wave's base); "Test prefixes" (`fz6`). Existing test helpers: `rdWeb`, `rdOwned` (`docker/inspect_test.go`), `dkContainer` (`docker/fake_docker_test.go`); the fixtures `compose/testdata/*.json`. Existing production names: `Parse`, `Validate`, `Facts`, `MaxConfigBytes`, `containerDetailOf`, `shownLabels`, `parseAPIVersion`, `logfilter.New`, `Push`, `Flush`, `Mask`.
- Produces: the fuzz targets `FuzzParse` (in `compose`), `FuzzContainerDetail`, `FuzzParseAPIVersion` and `FuzzFilterChunking`, under the package and name of their inventory rows; no exported identifier.

**Items:** 38

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check. It names each of the four new targets missing:

```bash
mkdir -p /root/.cache/w9-tmp/6 && export TMPDIR=/root/.cache/w9-tmp/6 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/docker[a-z/]*: no target'
```

Expected, in the inventory's order:

```text
fuzz: ./internal/modules/docker/compose: no target FuzzParse
fuzz: ./internal/modules/docker: no target FuzzContainerDetail
fuzz: ./internal/modules/docker: no target FuzzParseAPIVersion
fuzz: ./internal/modules/docker/logfilter: no target FuzzFilterChunking
```

Then create the three files.

`compose/fuzz_test.go`. The fuzzer's inputs stay far below the 4 MiB limit, so the `over` argument pads an input with zero bytes to `MaxConfigBytes+1`, which makes the refusal reachable. A fake `Facts` finds an owner for names starting `own` and an image for references of even length, so `Validate` meets both of its fact callbacks:

```go
package compose

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fz6Head returns at most the first 64 bytes of b, for a failure message.
func fz6Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzParse feeds arbitrary bytes to Parse, and a configuration it accepts to
// Validate: input over MaxConfigBytes is refused, every refusal starts with
// the package name, an accepted configuration lists its keys sorted, and
// Validate over it never panics, answers only docker.owned or
// docker.compose_unavailable, and answers the same twice (R74).
func FuzzParse(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		f.Fatal(err)
	}
	if len(names) == 0 {
		f.Fatal("no seed under testdata/")
	}
	for _, name := range names {
		b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, "shop", false)
	}
	f.Add([]byte(`{"name":"darkpanel-x","services":{"web":{"image":"nginx","labels":{"darkpanel.module":"npm"}}}}`), "darkpanel-x", false)
	f.Add([]byte(`{"name":"x","services":{"a":{"container_name":"own-a","volumes_from":["container:own-b:ro"]}}}`), "x", false)
	f.Add([]byte(`{"name":"x"}`), "x", true)
	f.Add([]byte(`{"name":"x"} {}`), "x", false)
	f.Add([]byte(`{"services":{"a":{},"a":{}}}`), "x", false)
	f.Fuzz(func(t *testing.T, data []byte, project string, over bool) {
		if over && len(data) <= MaxConfigBytes {
			data = append(data[:len(data):len(data)], make([]byte, MaxConfigBytes+1-len(data))...)
		}
		c, err := Parse(data)
		if len(data) > MaxConfigBytes && err == nil {
			t.Fatalf("a configuration of %d bytes was accepted", len(data))
		}
		if err != nil {
			if !strings.HasPrefix(err.Error(), "compose:") {
				t.Fatalf("error %q of %q does not start with the package name", err, fz6Head(data))
			}
			return
		}
		lists := [][]string{c.TopKeys}
		for _, s := range c.Services {
			lists = append(lists, s.Keys, s.Networks, s.Volumes, s.ContainerRefs)
		}
		for _, objects := range []map[string]Object{c.Networks, c.Volumes, c.Configs, c.Secrets} {
			for _, o := range objects {
				lists = append(lists, o.Keys)
			}
		}
		for _, l := range lists {
			if !slices.IsSorted(l) {
				t.Fatalf("a key list of %q is not sorted", fz6Head(data))
			}
		}
		facts := Facts{
			ImagePresent: func(ref string) bool { return len(ref)%2 == 0 },
			Owner: func(_, name string) string {
				if strings.HasPrefix(name, "own") {
					return "npm"
				}
				return ""
			},
		}
		for _, fc := range []Facts{{}, facts} {
			err := Validate(project, c, fc)
			if err == nil {
				continue
			}
			var ce *errcode.Error
			if !errors.As(err, &ce) || (ce.Code != errcode.DockerOwned && ce.Code != errcode.DockerComposeUnavailable) {
				t.Fatalf("Validate of %q answered %v", fz6Head(data), err)
			}
			if again := Validate(project, c, fc); again == nil || again.Error() != err.Error() {
				t.Fatalf("Validate of %q answered %v, then %v", fz6Head(data), err, again)
			}
		}
	})
}
```

`docker/fuzz_test.go`. Searching the answer for the secret values would also find them where they belong (a value `a` occurs in a name), so the target checks structure instead: environment values and arguments after the first are the mask, labels outside `shownLabels` have the mask for their value, and the answer is deep-equal to the answer for a copy of the container whose secret values were replaced, which fails if any secret value reaches any other member of the answer:

```go
package docker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// fz6Head returns at most the first 64 bytes of s, for a failure message.
func fz6Head(s string) string { return s[:min(len(s), 64)] }

// fz6Scrub is c with every value that inspect must not show replaced by
// another: the value of each environment entry, each argument after the
// first and each label value outside shownLabels.
func fz6Scrub(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	out := c
	if c.Config.Env != nil {
		out.Config.Env = make([]string, len(c.Config.Env))
		for i, e := range c.Config.Env {
			if name, _, found := strings.Cut(e, "="); found {
				e = name + "=scrubbed"
			}
			out.Config.Env[i] = e
		}
	}
	args := func(in []string) []string {
		if in == nil {
			return nil
		}
		scrubbed := slices.Clone(in)
		for i := 1; i < len(scrubbed); i++ {
			scrubbed[i] = "scrubbed"
		}
		return scrubbed
	}
	out.Config.Cmd, out.Config.Entrypoint = args(c.Config.Cmd), args(c.Config.Entrypoint)
	if c.Config.Labels != nil {
		out.Config.Labels = make(map[string]string, len(c.Config.Labels))
		for k, v := range c.Config.Labels {
			if !shownLabels[k] {
				v = "scrubbed"
			}
			out.Config.Labels[k] = v
		}
	}
	return out
}

// FuzzContainerDetail feeds arbitrary inspect JSON to containerDetailOf: it
// never panics, every environment value of the answer is the mask, every
// argument after the first is the mask, every label outside shownLabels has
// the mask for its value, and the answer does not change when those values
// are replaced by others, so no such value reaches any other member (R74).
func FuzzContainerDetail(f *testing.F) {
	for _, c := range []dockerapi.ContainerJSON{rdWeb(), rdWeb(rdOwned), dkContainer("bare")} {
		b, err := json.Marshal(c)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"Id":"x","Created":"2026-10-01T00:00:00Z","State":{"StartedAt":"0001-01-01T00:00:00Z","FinishedAt":"0001-01-01T00:00:00Z"},` +
		`"Config":{"Env":["K=v","BARE"],"Cmd":["run","--token=secret"],"Entrypoint":["/e","x"],"Labels":{"a":"secret","com.docker.compose.project":"p"}}}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		var c dockerapi.ContainerJSON
		if json.Unmarshal(in, &c) != nil {
			return
		}
		d, err := containerDetailOf(c)
		if err != nil {
			return
		}
		fail := func(what string) { t.Helper(); t.Fatalf("%s for %q", what, fz6Head(string(in))) }
		if len(d.Env) != len(c.Config.Env) {
			fail("the environment changed length")
		}
		for i, e := range d.Env {
			if name, _, _ := strings.Cut(c.Config.Env[i], "="); e.Name != name || e.Value != logfilter.Mask {
				fail("an environment entry is not name and mask")
			}
		}
		for _, p := range [][2][]string{{d.Cmd, c.Config.Cmd}, {d.Entrypoint, c.Config.Entrypoint}} {
			if len(p[0]) != len(p[1]) {
				fail("an argument list changed length")
			}
			for i := range p[0] {
				if (i == 0 && p[0][i] != p[1][i]) || (i > 0 && p[0][i] != logfilter.Mask) {
					fail("an argument is shown or masked wrongly")
				}
			}
		}
		if len(d.Labels) != len(c.Config.Labels) {
			fail("the labels changed length")
		}
		for k, v := range c.Config.Labels {
			want := logfilter.Mask
			if shownLabels[k] {
				want = v
			}
			if d.Labels[k] != want {
				fail("a label value is shown or masked wrongly")
			}
		}
		other, err := containerDetailOf(fz6Scrub(c))
		if err != nil || !reflect.DeepEqual(d, other) {
			fail("the answer depends on a redacted value")
		}
	})
}

// FuzzParseAPIVersion feeds an arbitrary string to parseAPIVersion: it never
// panics, and when it parses, major and minor are non-negative and print and
// parse back to the same pair (R74).
func FuzzParseAPIVersion(f *testing.F) {
	for _, s := range []string{
		"1.44", "1.49", "1.43", "", "1", "1.", ".1", "1.-0", "+1.+2", "1.2.3", " 1.2", "99999999999999999999.1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := parseAPIVersion(s)
		if !ok {
			return
		}
		if v[0] < 0 || v[1] < 0 {
			t.Fatalf("parseAPIVersion(%q) = %v", fz6Head(s), v)
		}
		if back, ok := parseAPIVersion(fmt.Sprintf("%d.%d", v[0], v[1])); !ok || back != v {
			t.Fatalf("parseAPIVersion(%q) = %v does not parse back: %v, %v", fz6Head(s), v, back, ok)
		}
	})
}
```

`logfilter/fuzz_test.go`. `Mask` (`[redacted]`) can spell a secret made of its letters, so the leak check first replaces every `Mask` in the output with a NUL byte and then looks for the secret; only a secret that itself holds a NUL is left out, and the split-independence check covers every input:

```go
package logfilter

import (
	"bytes"
	"testing"
)

// fz6Head returns at most the first 64 bytes of b, for a failure message.
func fz6Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz6Run pushes the chunks to a new filter of secrets on one stream and
// returns everything it emits, the flush included.
func fz6Run(secrets [][]byte, chunks ...[]byte) []byte {
	f := New(secrets)
	var out []byte
	for _, c := range chunks {
		out = append(out, f.Push("stdout", c)...)
	}
	return append(out, f.Flush("stdout")...)
}

// FuzzFilterChunking feeds arbitrary log bytes and two arbitrary secrets to a
// Filter: the output is the same whether the bytes arrive whole, in three
// chunks or one byte at a time, and no secret appears in it once every Mask
// is replaced by a NUL byte, so Mask's own letters cannot hide or fake a leak;
// a secret holding a NUL is left out of that check (R74).
func FuzzFilterChunking(f *testing.F) {
	f.Add("hunter2", "tok", []byte("pw=hunter2 and tok\n"), uint16(5), uint16(11))
	f.Add("ab", "abc", []byte("ababc abab"), uint16(1), uint16(3))
	f.Add("aab", "b", []byte("aaab aab"), uint16(2), uint16(6))
	f.Add("", "", []byte("nothing to hide"), uint16(0), uint16(0))
	f.Add("QX9", "HUNT3R", []byte("key=QX9 pass=HUNT3R QX9HUNT3R\n"), uint16(4), uint16(17))
	f.Fuzz(func(t *testing.T, s1, s2 string, data []byte, c1, c2 uint16) {
		secrets := [][]byte{[]byte(s1), []byte(s2)}
		whole := fz6Run(secrets, data)
		p1, p2 := int(c1)%(len(data)+1), int(c2)%(len(data)+1)
		if p1 > p2 {
			p1, p2 = p2, p1
		}
		if got := fz6Run(secrets, data[:p1], data[p1:p2], data[p2:]); !bytes.Equal(got, whole) {
			t.Fatalf("output of %q depends on the split at %d and %d", fz6Head(data), p1, p2)
		}
		bytewise := make([][]byte, len(data))
		for i := range data {
			bytewise[i] = data[i : i+1]
		}
		if got := fz6Run(secrets, bytewise...); !bytes.Equal(got, whole) {
			t.Fatalf("output of %q depends on a split into single bytes", fz6Head(data))
		}
		masked := bytes.ReplaceAll(whole, []byte(Mask), []byte{0})
		for _, s := range secrets {
			if len(s) > 0 && bytes.IndexByte(s, 0) < 0 && bytes.Contains(masked, s) {
				t.Fatalf("a secret of %d bytes appears in the output for %q", len(s), fz6Head(data))
			}
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify the seeds**

```bash
timeout 600 go test -count=1 -run '^Fuzz' ./internal/modules/docker ./internal/modules/docker/compose ./internal/modules/docker/logfilter
```

Expected: `ok` for the three packages. A `FAIL` is a finding: Step 3.

- [ ] **Step 3: Implement**

The task adds test code only. A seed run or a Step 4 fuzz run that fails shows a defect (W9-R3): in `compose/config.go`, `inspect.go`, `preflight.go` or `logfilter/logfilter.go` (none is frozen) fix it in this task, save the failing input under `<package>/testdata/fuzz/<Target>/` (Go writes it there on a fuzz failure), add a unit test named `TestFz6<Defect>` to the package's `fuzz_test.go`, and commit the fix on its own before the test commit. No defect is known today.

- [ ] **Step 4: Run the tests to verify the GREEN**

```bash
mkdir -p /root/.cache/w9-tmp/6 && export TMPDIR=/root/.cache/w9-tmp/6 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go test -count=1 -run '^Fuzz' ./internal/modules/docker ./internal/modules/docker/compose ./internal/modules/docker/logfilter
timeout 300 go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s -parallel 4 ./internal/modules/docker/compose 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzContainerDetail$' -fuzztime 30s -parallel 4 ./internal/modules/docker 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseAPIVersion$' -fuzztime 30s -parallel 4 ./internal/modules/docker 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzFilterChunking$' -fuzztime 30s -parallel 4 ./internal/modules/docker/logfilter 2>&1 | tail -n 1
git status --short -- '*/testdata/fuzz/*'
```

Expected: `ok` for the three packages; the last line of each fuzz run begins `ok` and names its package; the last command prints nothing unless a run found a defect. Quote the four last lines in the report.

- [ ] **Step 5: Verify the task**

```bash
mkdir -p /root/.cache/w9-tmp/6 && export TMPDIR=/root/.cache/w9-tmp/6 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/docker ./internal/modules/docker/compose ./internal/modules/docker/logfilter
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/docker[a-z/]*: ' || echo "no problem left for this task"
git status --short
rm -rf "$TMPDIR" && go clean -testcache && go clean -fuzzcache
```
Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; `golangci-lint` prints `0 issues.`; `go mod tidy -diff` prints nothing; the check filter prints `no problem left for this task` (the check's other lines name the targets of tasks outside this one); `git status --short` lists only this task's files and prints nothing under `testdata/fuzz/` unless a run found a defect (Step 4). Record the real output in the report; a line of the full-suite constraint you skip is named with its reason.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/docker/compose/fuzz_test.go internal/modules/docker/fuzz_test.go internal/modules/docker/logfilter/fuzz_test.go
git commit -m "test(docker): fuzz compose, inspect and log filter"
```


### Task 7: fuzz: firewall module parsers

**Files:**
- Create: `internal/modules/firewall/model/fuzz_test.go` (`FuzzParseTag`, `TestFz7ParseTagTrailingSpace`)
- Create: `internal/modules/firewall/conf/fuzz_test.go` (`FuzzConfiguredIPv6`)
- Create: `internal/modules/firewall/loaded/fuzz_test.go` (`FuzzToolVariant`)
- Create: `internal/modules/firewall/fuzz_test.go` (`FuzzInstalledVersion`)
- Create: `internal/modules/firewall/dockerfw/fuzz_test.go` (`FuzzBlock`)
- Create: `internal/modules/firewall/predict/fuzz_test.go` (`FuzzSetDefault`)
- Create: `internal/modules/firewall/eval/fuzz_test.go` (`FuzzEvaluate`)
- Modify: `internal/modules/firewall/model/rule.go` (`ParseTag`, Step 4)
- Modify only for a defect a run finds (W9-R3): the other non-frozen production files of these packages.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 7; "Fuzz runner" and "Inventory format" (Task 1, in this wave's base); "Test prefixes" (`fz7`). Existing test names: `ruleTestID` (`model/rule_test.go`), `confTestDefault`, `confTestUFWConf` (`conf/conf_test.go`), `walkFilter` (`eval/eval_test.go`), `predictGolden` (`predict/render_test.go`). Existing fixtures: `firewall/testdata/golden/*/*/files/etc/...`, `firewall/dockerfw/testdata/capture/*/*/files/etc/ufw/after*.rules`, `firewall/eval/testdata/*/*.save` and `internal/platform/netfilter/testdata/**.save`. Existing production names: `ParseTag`, `Tag`, `TagPrefix`, `ulidRe`, `ConfiguredIPv6`, `SysctlKey`, `toolVariant`, `installedVersion`, `upstreamAtLeast`, `BlockOf`, `WithBlock`, `WithoutBlock`, `setDefault`, `ufwDefaults`, `ufwDefaultRe`, `Evaluate`, `Unmodelled`.
- Produces: the fuzz targets `FuzzParseTag`, `FuzzConfiguredIPv6`, `FuzzToolVariant`, `FuzzInstalledVersion`, `FuzzBlock`, `FuzzSetDefault` and `FuzzEvaluate`, under the package and name of their inventory rows; no exported identifier.

**Items:** 38

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check. It names each of the seven new targets missing:

```bash
mkdir -p /root/.cache/w9-tmp/7 && export TMPDIR=/root/.cache/w9-tmp/7 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/firewall[a-z/]*: no target'
```

Expected, in the inventory's order (the existing `FuzzParseTuple`, `FuzzReadFiles`, `FuzzParseNFTTable` and `FuzzEdit` are not named):

```text
fuzz: ./internal/modules/firewall/model: no target FuzzParseTag
fuzz: ./internal/modules/firewall/conf: no target FuzzConfiguredIPv6
fuzz: ./internal/modules/firewall/loaded: no target FuzzToolVariant
fuzz: ./internal/modules/firewall: no target FuzzInstalledVersion
fuzz: ./internal/modules/firewall/dockerfw: no target FuzzBlock
fuzz: ./internal/modules/firewall/predict: no target FuzzSetDefault
fuzz: ./internal/modules/firewall/eval: no target FuzzEvaluate
```

Then create the seven files.

`model/fuzz_test.go`. `Tag` writes no space after an id without a label, so a comment `darkpanel:<id>` followed by one space is not something `Tag` produces, yet `ParseTag` accepts it with an empty label and `Tag(id, "")` then drops the space. The seed and `TestFz7ParseTagTrailingSpace` fail on the current tree:

```go
package model

import "testing"

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// TestFz7ParseTagTrailingSpace: Tag never writes a space after an id without
// a label, so such a comment is a manual rule's and not a tag.
func TestFz7ParseTagTrailingSpace(t *testing.T) {
	comment := TagPrefix + ruleTestID + " "
	if id, label, ok := ParseTag(comment); ok {
		t.Fatalf("ParseTag(%q) = %q, %q, true", comment, id, label)
	}
}

// FuzzParseTag feeds an arbitrary rule comment to ParseTag: it never panics,
// a comment it refuses gives empty parts, and a comment it accepts holds a
// ULID and is exactly what Tag writes for the id and label it returns (R74).
func FuzzParseTag(f *testing.F) {
	for _, c := range []string{
		Tag(ruleTestID, ""), Tag(ruleTestID, "ssh from office"), Tag(ruleTestID, " two"),
		TagPrefix + ruleTestID + " ", TagPrefix + ruleTestID + "x", TagPrefix + "01J0000000000000000000000",
		"Darkpanel:" + ruleTestID, "hand rule", "",
	} {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, comment string) {
		id, label, ok := ParseTag(comment)
		if !ok {
			if id != "" || label != "" {
				t.Fatalf("ParseTag(%q) refused the comment and returned %q, %q", fz7Head(comment), id, fz7Head(label))
			}
			return
		}
		if !ulidRe.MatchString(id) {
			t.Fatalf("ParseTag(%q) returned the id %q", fz7Head(comment), id)
		}
		if got := Tag(id, label); got != comment {
			t.Fatalf("Tag(%q, %q) = %q, want the comment %q", id, fz7Head(label), fz7Head(got), fz7Head(comment))
		}
	})
}
```

`conf/fuzz_test.go`. A last `IPV6=yes` or `IPV6=no` line decides `ConfiguredIPv6` whatever precedes it, because `ufw.conf` is read after `/etc/default/ufw` and the last assignment wins; the target appends such a line and checks both files:

```go
package conf

import "testing"

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// FuzzConfiguredIPv6 feeds arbitrary contents of /etc/default/ufw and ufw.conf
// to ConfiguredIPv6 and an arbitrary name to SysctlKey. SysctlKey is
// idempotent and keeps the length of its input. A final IPV6=yes or IPV6=no
// line decides ConfiguredIPv6 whatever precedes it, in ufw.conf, which wins
// over /etc/default/ufw, and in /etc/default/ufw alone (R74).
func FuzzConfiguredIPv6(f *testing.F) {
	f.Add(confTestDefault, confTestUFWConf, "net.ipv4.conf.eth0/100.rp_filter")
	f.Add("IPV6=yes\n", "", "net/ipv4/ip_forward")
	f.Add("IPV6=\"yes\"\n", "IPV6=no\n", ".a/b.c")
	f.Add("IPV6='yes\n\tIPV6=maybe", "\n\n#IPV6=yes", "/")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, defaults, ufwconf, name string) {
		once := SysctlKey(name)
		if twice := SysctlKey(once); twice != once || len(once) != len(name) {
			t.Fatalf("SysctlKey(%q) = %q, then %q", fz7Head(name), fz7Head(once), fz7Head(twice))
		}
		for _, c := range []struct {
			set  string
			want bool
		}{{"\nIPV6=yes\n", true}, {"\nIPV6=no\n", false}} {
			if got := ConfiguredIPv6([]byte(defaults), []byte(ufwconf+c.set)); got != c.want {
				t.Fatalf("ufw.conf ending in %q gives %v for defaults %q, ufw.conf %q", c.set, got, fz7Head(defaults), fz7Head(ufwconf))
			}
			if got := ConfiguredIPv6([]byte(defaults+c.set), nil); got != c.want {
				t.Fatalf("/etc/default/ufw ending in %q gives %v for %q", c.set, got, fz7Head(defaults))
			}
		}
	})
}
```

`loaded/fuzz_test.go`. The fake runner answers every command alike; no process runs. The target does not assert that a leading blank line is read as `checkTools` reads it; that difference between the two readers is recorded elsewhere:

```go
package loaded

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fz7Runner is a core.Runner that answers every command alike.
type fz7Runner struct {
	res core.CmdResult
	err error
}

func (r fz7Runner) Run(context.Context, core.Cmd) (core.CmdResult, error) { return r.res, r.err }

// FuzzToolVariant feeds arbitrary output of `iptables --version`, as a
// successful run, as a failed one and as a tool that is not installed, to
// toolVariant: it never panics, a variant it reports is nft or legacy, and it
// reads the output as its first line ending in the variant's name. Only a
// tool that is not installed is neither a variant nor an error (R74).
func FuzzToolVariant(f *testing.F) {
	for _, s := range []string{
		"iptables v1.8.10 (nf_tables)\n", "iptables v1.8.7 (legacy)", "\niptables v1.8.10 (nf_tables)\n",
		"iptables v1.8.10", "iptables v1.8.10 (nf_tables)\nsecond line (legacy)", "",
	} {
		f.Add(s, uint8(0))
	}
	f.Add("iptables: not found", uint8(1))
	f.Add("", uint8(2))
	f.Fuzz(func(t *testing.T, out string, mode uint8) {
		x := fz7Runner{res: core.CmdResult{Stdout: []byte(out)}}
		switch mode % 3 {
		case 1:
			x.res = core.CmdResult{Stderr: []byte(out), ExitCode: 1}
		case 2:
			x.err = fs.ErrNotExist
		}
		variant, ok, err := toolVariant(context.Background(), x, "iptables")
		switch {
		case ok:
			if err != nil || (variant != "nft" && variant != "legacy") {
				t.Fatalf("variant %q, error %v for %q", variant, err, out[:min(len(out), 64)])
			}
		case err != nil:
			if variant != "" {
				t.Fatalf("variant %q with error %v for %q", variant, err, out[:min(len(out), 64)])
			}
		case mode%3 != 2:
			t.Fatalf("no variant and no error for %q although the tool ran", out[:min(len(out), 64)])
		}
		if mode%3 == 0 {
			line, _, _ := strings.Cut(out, "\n")
			if want := strings.HasSuffix(line, " (nf_tables)") || strings.HasSuffix(line, " (legacy)"); ok != want {
				t.Fatalf("ok is %v for the first line %q", ok, line[:min(len(line), 64)])
			}
		}
	})
}
```

`firewall/fuzz_test.go`:

```go
package firewall

import (
	"bytes"
	"strings"
	"testing"
)

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// FuzzInstalledVersion feeds an arbitrary dpkg status database to
// installedVersion and an arbitrary version to upstreamAtLeast: an installed
// version is non-empty, has no surrounding space and occurs in the database
// beside the package name and the word installed; a version that
// upstreamAtLeast reads is at least 0, and at least 0.34 when it is at least
// 0.35 (R74).
func FuzzInstalledVersion(f *testing.F) {
	f.Add([]byte("Package: ufw\nStatus: install ok installed\nVersion: 0.36.2-6\n"), "0.36.2-6")
	f.Add([]byte("Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: ufw\nStatus: install ok installed\nVersion: 2:0.35\nDescription: x\n y\n"), "2:0.35")
	f.Add([]byte("Package: ufw\nStatus: deinstall ok config-files\nVersion: 0.36.2-6\n"), "0.34.1-2")
	f.Add([]byte("Package: ufw-extras\nStatus: install ok installed\nVersion: 1.0\n"), "")
	f.Add([]byte("Package: ufw\r\nStatus: install ok installed\r\nVersion:  0.36 \r\n"), "-1")
	f.Add([]byte(""), "x.35")
	f.Add([]byte("Package: ufw\nStatus: install ok installed\n"), "0..35")
	f.Fuzz(func(t *testing.T, data []byte, version string) {
		v, ok := installedVersion(data, "ufw")
		if ok {
			if v == "" || v != strings.TrimSpace(v) {
				t.Fatalf("installed version %q for %q", fz7Head(v), fz7Head(string(data)))
			}
			for _, part := range []string{"ufw", "installed", v} {
				if !bytes.Contains(data, []byte(part)) {
					t.Fatalf("installed version %q is not in %q", fz7Head(v), fz7Head(string(data)))
				}
			}
		}
		for _, s := range []string{version, v} {
			atLeast, err := upstreamAtLeast(s, []int{0, 35})
			if err != nil {
				if atLeast {
					t.Fatalf("upstreamAtLeast(%q) is true with the error %v", fz7Head(s), err)
				}
				continue
			}
			if zero, err := upstreamAtLeast(s, []int{0}); err != nil || !zero {
				t.Fatalf("upstreamAtLeast(%q, 0) = %v, %v", fz7Head(s), zero, err)
			}
			if lower, err := upstreamAtLeast(s, []int{0, 34}); atLeast && (err != nil || !lower) {
				t.Fatalf("upstreamAtLeast(%q) holds for 0.35 but not for 0.34: %v, %v", fz7Head(s), lower, err)
			}
		}
	})
}
```

`dockerfw/fuzz_test.go`. `WithBlock` ends a block that lacks a final newline, and a file that lacks one, with a newline, so `BlockOf(WithBlock(f, b))` is `b` with that newline and `WithoutBlock(WithBlock(f, b))` is `f` with it; the target asserts those forms. The seeds are the after-rules files of the real captures and their blocks:

```go
package dockerfw

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fz7Head returns at most the first 64 bytes of b, for a failure message.
func fz7Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz7Terminated is b with the final newline WithBlock supplies.
func fz7Terminated(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(bytes.Clone(b), '\n')
	}
	return b
}

// FuzzBlock feeds an arbitrary after.rules content and block to BlockOf,
// WithBlock and WithoutBlock: a file that BlockOf refuses is refused by both
// edits; WithBlock's output holds the block, ended by a newline, and setting
// the same block again changes nothing; WithoutBlock of it gives the file
// back, with the newline WithBlock supplies, when the file had no block, and
// WithoutBlock of the file when it had one (R74).
func FuzzBlock(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("testdata", "capture", "*", "*", "files", "etc", "ufw", "after*.rules"))
	if err != nil {
		f.Fatal(err)
	}
	if len(names) == 0 {
		f.Fatal("no seed under testdata/capture")
	}
	for _, name := range names {
		file, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		block, ok, err := BlockOf(file)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		if !ok {
			block = []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n")
		}
		f.Add(file, block)
	}
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("a\nCOMMIT"), []byte("*filter\nCOMMIT"))
	f.Add([]byte("# BEGIN DARKPANEL docker\nx\n# END DARKPANEL docker"), []byte("y"))
	f.Add([]byte("# BEGIN DARKPANEL docker \nx\n# END DARKPANEL docker\n"), []byte("y\n"))
	f.Add([]byte("a\n"), []byte("# END DARKPANEL docker\n"))
	f.Fuzz(func(t *testing.T, file, block []byte) {
		_, had, scanErr := BlockOf(file)
		first, addErr := WithBlock(file, block)
		without, rmErr := WithoutBlock(file)
		if scanErr != nil {
			if addErr == nil || rmErr == nil {
				t.Fatalf("a file BlockOf refuses was edited: %q", fz7Head(file))
			}
			return
		}
		if rmErr != nil {
			t.Fatalf("WithoutBlock refused a file BlockOf accepts: %q: %v", fz7Head(file), rmErr)
		}
		if addErr != nil {
			return
		}
		got, ok, err := BlockOf(first)
		if err != nil || !ok || !bytes.Equal(got, fz7Terminated(block)) {
			t.Fatalf("BlockOf(WithBlock(%q, %q)) = %q, %v, %v", fz7Head(file), fz7Head(block), fz7Head(got), ok, err)
		}
		if again, err := WithBlock(first, block); err != nil || !bytes.Equal(again, first) {
			t.Fatalf("WithBlock is not idempotent for %q: %v", fz7Head(file), err)
		}
		want := without
		if !had {
			want = fz7Terminated(file)
		}
		if back, err := WithoutBlock(first); err != nil || !bytes.Equal(back, want) {
			t.Fatalf("WithoutBlock(WithBlock(%q, %q)) = %q, %v", fz7Head(file), fz7Head(block), fz7Head(back), err)
		}
	})
}
```

`predict/fuzz_test.go`. UFW's `set_default` appends a new assignment to a last line that has no newline by joining the two, and UFW reads `Key` and `key` as one key with the last spelling winning; `setDefault` and `ufwDefaults` copy both. The target therefore gives its input a final newline and leaves out a file that spells the key in two cases; it also leaves out values that are not one plain or double-quoted word, which is all the module passes:

```go
package predict

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// FuzzSetDefault feeds an arbitrary /etc/default/ufw or ufw.conf content, an
// upper-case key and a plain or double-quoted word to setDefault and
// ufwDefaults. Other input is left out: a value with a newline or an equals
// sign is not one setDefault is given, and UFW's set_default appends to a last
// line without a newline by joining the two, so the content gets its final
// newline first. A key that the file also spells in another case is left out
// too, because UFW reads both spellings as one key and the last one wins.
// setDefault is then idempotent and ufwDefaults reads back the value it set
// (R74).
func FuzzSetDefault(f *testing.F) {
	for _, seed := range []struct{ pattern, key, value string }{
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "default", "ufw"), "DEFAULT_INPUT_POLICY", `"REJECT"`},
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "ufw", "ufw.conf"), "ENABLED", "yes"},
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "ufw", "ufw.conf"), "LOGLEVEL", "high"},
	} {
		names, err := filepath.Glob(seed.pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", seed.pattern)
		}
		for _, name := range names {
			data, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data, seed.key, seed.value)
		}
	}
	f.Add([]byte("IPV6=yes\nIPV6=no\n"), "IPV6", "no")
	f.Add([]byte("ENABLED=no\nenabled=yes\n"), "ENABLED", "yes")
	f.Add([]byte("A=1"), "B", `"x"`)
	f.Add([]byte(nil), "A", "a")
	key, word := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`), regexp.MustCompile(`^(\w+|"\w+")$`)
	f.Fuzz(func(t *testing.T, data []byte, opt, value string) {
		if !key.MatchString(opt) || !word.MatchString(value) {
			return
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			data = append(bytes.Clone(data), '\n')
		}
		for _, line := range strings.Split(string(data), "\n") {
			if k, _, _ := strings.Cut(line, "="); ufwDefaultRe.MatchString(line) && k != opt && strings.EqualFold(k, opt) {
				return
			}
		}
		once := setDefault(data, opt, value)
		if twice := setDefault(once, opt, value); !bytes.Equal(once, twice) {
			t.Fatalf("setDefault(%q, %q, %q) is not idempotent", data[:min(len(data), 64)], opt, value)
		}
		want := strings.Trim(strings.ToLower(value), `"'`)
		if got, ok := ufwDefaults(once)[strings.ToLower(opt)]; !ok || got != want {
			t.Fatalf("ufwDefaults reads %q, %v after setDefault(%q, %q, %q); want %q", got, ok, data[:min(len(data), 64)], opt, value, want)
		}
	})
}
```

`eval/fuzz_test.go`. The packets cover both families, the three hooks, TCP, UDP and ICMP, an established connection and a marked packet, each with and without a router:

```go
package eval

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// fz7Packets is a fixed set of packets, both families, every hook, and the
// protocols and connection states the walk tells apart.
func fz7Packets() []fwstate.Packet {
	var out []fwstate.Packet
	for _, fam := range []struct {
		family   fwstate.Family
		src, dst string
	}{
		{fwstate.V4, "203.0.113.7", "192.0.2.1"},
		{fwstate.V6, "2001:db8::7", "2001:db8::1"},
	} {
		base := fwstate.Packet{
			Family: fam.family, Src: netip.MustParseAddr(fam.src), Dst: netip.MustParseAddr(fam.dst),
			SrcPort: 40000, DstPort: 22, Proto: "tcp", CtState: "NEW",
		}
		input, forward, output := base, base, base
		input.Path, input.InIface = "input", "eth0"
		forward.Path, forward.InIface, forward.OutIface, forward.DstPort = "forward", "eth0", "eth1", 80
		output.Path, output.OutIface = "output", "eth0"
		udp, established, marked, icmp := input, input, forward, input
		udp.Proto, udp.DstPort = "udp", 53
		established.CtState = "ESTABLISHED"
		marked.Mark = 0x1
		icmp.Proto, icmp.SrcPort, icmp.DstPort = "icmp", 0, 0
		out = append(out, input, forward, output, udp, established, marked, icmp)
	}
	return out
}

// FuzzEvaluate feeds arbitrary save output to netfilter.Parse and, when it is
// accepted, to Evaluate for a fixed set of packets, without and with a router,
// and to Unmodelled: none may panic, every verdict is one of the four, the
// same ruleset gives the same results twice, and Unmodelled lists only rules
// of the mangle and security tables (R74).
func FuzzEvaluate(f *testing.F) {
	for _, pattern := range []string{
		filepath.Join("testdata", "*", "*.save"),
		filepath.Join("..", "..", "..", "platform", "netfilter", "testdata", "*.save"),
		filepath.Join("..", "..", "..", "platform", "netfilter", "testdata", "ufw", "*", "*.save"),
	} {
		names, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", pattern)
		}
		for _, name := range names {
			b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under a testdata/ directory
			if err != nil {
				f.Fatal(err)
			}
			f.Add(string(b))
		}
	}
	f.Add(walkFilter)
	route := func(fwstate.Packet) (string, string, error) { return "forward", "eth1", nil }
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := netfilter.Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, p := range fz7Packets() {
			for _, r := range []fwstate.Router{nil, route} {
				got := Evaluate(rs, p, r)
				switch got.Verdict {
				case fwstate.Accept, fwstate.Drop, fwstate.Reject, fwstate.Unknown:
				default:
					t.Fatalf("verdict %q for %q", got.Verdict, in[:min(len(in), 64)])
				}
				if again := Evaluate(rs, p, r); !reflect.DeepEqual(got, again) {
					t.Fatalf("Evaluate gave %+v, then %+v for %q", got, again, in[:min(len(in), 64)])
				}
			}
		}
		gaps := Unmodelled(rs)
		for _, g := range gaps {
			if !strings.HasPrefix(g, "mangle/") && !strings.HasPrefix(g, "security/") {
				t.Fatalf("Unmodelled lists %q for %q", g, in[:min(len(in), 64)])
			}
		}
		if again := Unmodelled(rs); !reflect.DeepEqual(gaps, again) {
			t.Fatalf("Unmodelled gave %q, then %q for %q", gaps, again, in[:min(len(in), 64)])
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify the RED**

```bash
timeout 300 go test -count=1 -run '^(FuzzParseTag|TestFz7ParseTagTrailingSpace)$' ./internal/modules/firewall/model
timeout 900 go test -count=1 -run '^Fuzz' ./internal/modules/firewall ./internal/modules/firewall/conf ./internal/modules/firewall/loaded ./internal/modules/firewall/dockerfw ./internal/modules/firewall/predict ./internal/modules/firewall/eval
```

Expected from the first command: `--- FAIL: TestFz7ParseTagTrailingSpace` with `ParseTag("darkpanel:01J00000000000000000000000 ") = "01J00000000000000000000000", "", true`, and `--- FAIL: FuzzParseTag/seed#3` with `Tag("01J00000000000000000000000", "") = "darkpanel:01J00000000000000000000000", want the comment "darkpanel:01J00000000000000000000000 "`; every other seed passes. The second command prints `ok` for the six packages. A `FAIL` there is a finding: Step 3 item 2.

- [ ] **Step 3: Implement**

The task adds test code; it changes production code only where a run fails (W9-R3), and the first failure is known.

1. `ParseTag` in `internal/modules/firewall/model/rule.go`, the defect the seed of Step 1 shows. After it cuts the single space that separates id and label, an empty label means the comment was not written by `Tag`, so it returns `"", "", false` as it does for any other comment that is not a tag. A comment with a label, with the label's own leading spaces, and one without a label are unchanged. Both failures of Step 2 then pass, and `TestTag` keeps passing.
2. Any other failure of a seed run or of a Step 4 fuzz run is a defect (W9-R3): fix it in this task in a non-frozen file of these packages, save the failing input under `<package>/testdata/fuzz/<Target>/` (Go writes it there on a fuzz failure), add a unit test named `TestFz7<Defect>` to the package's `fuzz_test.go`, and commit the fix on its own before the test commit.

- [ ] **Step 4: Run the tests to verify the GREEN**

```bash
mkdir -p /root/.cache/w9-tmp/7 && export TMPDIR=/root/.cache/w9-tmp/7 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 900 go test -count=1 -run '^(Fuzz|TestFz7)' ./internal/modules/firewall ./internal/modules/firewall/model ./internal/modules/firewall/conf ./internal/modules/firewall/loaded ./internal/modules/firewall/dockerfw ./internal/modules/firewall/predict ./internal/modules/firewall/eval
timeout 300 go test -run '^$' -fuzz '^FuzzParseTag$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/model 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzConfiguredIPv6$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/conf 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzToolVariant$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/loaded 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzInstalledVersion$' -fuzztime 30s -parallel 4 ./internal/modules/firewall 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzBlock$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/dockerfw 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzSetDefault$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/predict 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzEvaluate$' -fuzztime 30s -parallel 4 ./internal/modules/firewall/eval 2>&1 | tail -n 1
git status --short -- '*/testdata/fuzz/*'
```

Expected: `ok` for the seven packages; the last line of each fuzz run begins `ok` and names its package; the last command prints nothing unless a run found a defect. Quote the seven last lines in the report.

- [ ] **Step 5: Verify the task**

```bash
mkdir -p /root/.cache/w9-tmp/7 && export TMPDIR=/root/.cache/w9-tmp/7 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/firewall ./internal/modules/firewall/model ./internal/modules/firewall/conf ./internal/modules/firewall/loaded ./internal/modules/firewall/dockerfw ./internal/modules/firewall/predict ./internal/modules/firewall/eval
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/firewall[a-z/]*: ' || echo "no problem left for this task"
git status --short
rm -rf "$TMPDIR" && go clean -testcache && go clean -fuzzcache
```
Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; `golangci-lint` prints `0 issues.`; `go mod tidy -diff` prints nothing; the check filter prints `no problem left for this task` (the check's other lines name the targets of tasks outside this one); `git status --short` lists only this task's files and prints nothing under `testdata/fuzz/` unless a run found a defect (Step 4). Record the real output in the report; a line of the full-suite constraint you skip is named with its reason.

- [ ] **Step 6: Commit**

The `ParseTag` fix goes first, in its own commit:

```bash
git add internal/modules/firewall/model/rule.go
git commit -m "fix(firewall): refuse a tag comment with a bare space"
git add internal/modules/firewall/model/fuzz_test.go internal/modules/firewall/conf/fuzz_test.go internal/modules/firewall/loaded/fuzz_test.go internal/modules/firewall/fuzz_test.go internal/modules/firewall/dockerfw/fuzz_test.go internal/modules/firewall/predict/fuzz_test.go internal/modules/firewall/eval/fuzz_test.go
git commit -m "test(firewall): fuzz firewall module parsers"
```


### Task 8: fuzz: tailscale, tailnet, derp, cloudflare and npm parsers

**Files:**
- Create: `internal/modules/tailscale/fuzz_test.go` (`FuzzReadOSRelease`, `FuzzDpkgStatus`, `FuzzKeyringFingerprints`)
- Create: `internal/modules/tailscale/stun/fuzz_test.go` (`FuzzParse`)
- Create: `internal/modules/tailnet/policy/fuzz_test.go` (`FuzzApply`, `FuzzCanonical`)
- Create: `internal/modules/derp/fuzz_test.go` (`FuzzParseDERPMap`)
- Create: `internal/modules/derp/artifact/fuzz_test.go` (`FuzzParseManifest`)
- Create: `internal/modules/derp/server/fuzz_test.go` (`FuzzParseCert`)
- Create: `internal/modules/cloudflare/ingress/fuzz_test.go` (`FuzzConfigJSON`)
- Create: `internal/modules/cloudflare/fuzz_test.go` (`FuzzServiceHost`)
- Create: `internal/modules/npm/proxy/fuzz_test.go` (`FuzzCheckUpstream`)
- Modify only for a defect a run finds (W9-R3): the non-frozen production files of these packages.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 8; "Fuzz runner" and "Inventory format" (Task 1, in this wave's base); "Test prefixes" (`fz8`). Existing test names: `thDpkgStatus` (`tailscale/testhost_test.go`), `snTx`, `snMessage`, `snAttr`, `snAddress`, `snBindingSuccess`, `snBindingError`, `snMapped`, `snXorMapped`, `snSoftware` (`stun/stun_test.go`), `edDoc` (`policy/policy_test.go`), `sdPolicyWithRegions` (`derp/derpmap_test.go`), `mfManifest`, `mfDoc`, `mfDarkpanel`, `mfDerper`, `mfArtifacts` (`artifact/manifest_test.go`), `inA`, `inEnd` (`ingress/ingress_test.go`), `pxReserved` (`proxy/upstream_test.go`). Existing production names: `readOSRelease`, `ts.installedVersion`, `deps.readFile`, `repoKeyring`, `keyringFingerprints`, `Parse`, `Request`, `decodeAddress`, `Apply`, `Edit`, `Canonical`, `Diff`, `parseDERPMap`, `ParseManifest`, `Manifest.Encode`, `ParseCert`, `Config`, `Rule`, `Hash`, `serviceHost`, `serviceProblem`, `legacyIPv4`, `CheckUpstream`, `upstreamAddr`, `parseNginxIPv4`, `parseInetAton`.
- Produces: the fuzz targets `FuzzReadOSRelease`, `FuzzDpkgStatus`, `FuzzKeyringFingerprints`, `FuzzParse` (in `stun`), `FuzzApply`, `FuzzCanonical`, `FuzzParseDERPMap`, `FuzzParseManifest`, `FuzzParseCert`, `FuzzConfigJSON`, `FuzzServiceHost` and `FuzzCheckUpstream`, under the package and name of their inventory rows; no exported identifier.

**Items:** 38

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check. It names each of the twelve new targets missing:

```bash
mkdir -p /root/.cache/w9-tmp/8 && export TMPDIR=/root/.cache/w9-tmp/8 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/(tailscale|tailnet|derp|cloudflare|npm)[a-z/]*: no target'
```

Expected, in the inventory's order:

```text
fuzz: ./internal/modules/tailscale: no target FuzzReadOSRelease
fuzz: ./internal/modules/tailscale: no target FuzzDpkgStatus
fuzz: ./internal/modules/tailscale: no target FuzzKeyringFingerprints
fuzz: ./internal/modules/tailscale/stun: no target FuzzParse
fuzz: ./internal/modules/tailnet/policy: no target FuzzApply
fuzz: ./internal/modules/tailnet/policy: no target FuzzCanonical
fuzz: ./internal/modules/derp: no target FuzzParseDERPMap
fuzz: ./internal/modules/derp/artifact: no target FuzzParseManifest
fuzz: ./internal/modules/derp/server: no target FuzzParseCert
fuzz: ./internal/modules/cloudflare/ingress: no target FuzzConfigJSON
fuzz: ./internal/modules/cloudflare: no target FuzzServiceHost
fuzz: ./internal/modules/npm/proxy: no target FuzzCheckUpstream
```

Then create the nine files. Each keeps its own `fz8Head`, which cuts the input quoted in a failure message to 64 bytes. No target reads a file of the host, opens a socket or runs a program: `/etc/os-release` and `/var/lib/dpkg/status` reach the code through the functions' own fake reader arguments, and STUN is parsed from bytes without `Probe`.

`tailscale/fuzz_test.go`. `fz8Installed` is an independent check for `FuzzDpkgStatus`: a non-empty version must be the `Version` of some stanza that names `tailscale` and has a `Status` ending in `installed`, which the target looks for with a plain line scan of its own:

```go
package tailscale

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzReadOSRelease feeds arbitrary /etc/os-release content to readOSRelease:
// it never panics, asks only for /etc/os-release, and reports a distribution
// id and a codename of one line each (R74).
func FuzzReadOSRelease(f *testing.F) {
	f.Add([]byte("NAME=\"Ubuntu\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n"))
	f.Add([]byte("PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=\"debian\"\nVERSION_CODENAME='bookworm'\n"))
	f.Add([]byte("# a comment\n\nID=debian\n  VERSION_CODENAME=trixie  \n#ID=ubuntu\n"))
	f.Add([]byte("ID=\"\nVERSION_CODENAME='\nID='x\"\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		var asked []string
		info, err := readOSRelease(func(name string) ([]byte, error) {
			asked = append(asked, name)
			return data, nil
		})
		if err != nil {
			t.Fatalf("readOSRelease of %q: %v", fz8Head(data), err)
		}
		if len(asked) != 1 || asked[0] != "/etc/os-release" {
			t.Fatalf("readOSRelease read %q", asked)
		}
		if strings.Contains(info.ID, "\n") || strings.Contains(info.Codename, "\n") {
			t.Fatalf("readOSRelease of %q returned %+v", fz8Head(data), info)
		}
	})
}

// fz8Installed reports whether some stanza of a dpkg status file, stanzas
// being separated by blank lines, names the package tailscale, has a Status
// ending in " installed" and has the given Version, reading only lines that do
// not start with white space.
func fz8Installed(status, version string) bool {
	var named, installed, versioned bool
	end := func() bool {
		ok := named && installed && versioned
		named, installed, versioned = false, false, false
		return ok
	}
	for line := range strings.Lines(status) {
		if strings.TrimSpace(line) == "" {
			if end() {
				return true
			}
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Package":
			named = named || value == "tailscale"
		case "Status":
			installed = installed || strings.HasSuffix(value, " installed")
		case "Version":
			versioned = versioned || value == version
		}
	}
	return end()
}

// FuzzDpkgStatus feeds an arbitrary dpkg status database, through a fake file
// reader, to installedVersion: it never panics or fails, and it returns a
// non-empty version only for a Package: tailscale stanza whose Status ends in
// installed and which holds that version (R74).
func FuzzDpkgStatus(f *testing.F) {
	f.Add(thDpkgStatus("1.104.1"))
	f.Add(thDpkgStatus(""))
	f.Add([]byte("Package: tailscale\nStatus: deinstall ok config-files\nVersion: 1.102.0\n"))
	f.Add([]byte("Package: tailscale\nStatus: install ok unpacked\nVersion: 1.102.0\n"))
	f.Add([]byte("Package: tailscaled-extra\nStatus: install ok installed\nVersion: 3.0\n"))
	f.Add([]byte("Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: tailscale\nStatus: install ok installed\nVersion: 1.2.3"))
	f.Add([]byte("Package: tailscale\n Status: install ok installed\nVersion: 1.0\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		tt := ts{d: deps{readFile: func(string) ([]byte, error) { return data, nil }}}
		version, err := tt.installedVersion(context.Background(), core.Env{})
		if err != nil {
			t.Fatalf("installedVersion of %q: %v", fz8Head(data), err)
		}
		if version != "" && !fz8Installed(string(data), version) {
			t.Fatalf("installedVersion of %q returned %q, which no installed tailscale stanza holds", fz8Head(data), fz8Head([]byte(version)))
		}
	})
}

// FuzzKeyringFingerprints feeds arbitrary bytes to keyringFingerprints, seeded
// with the embedded keyring: it never panics, an error returns no
// fingerprints, and every fingerprint is 40 upper-case hexadecimal
// characters (R74).
func FuzzKeyringFingerprints(f *testing.F) {
	fingerprint := regexp.MustCompile(`^[0-9A-F]{40}$`)
	f.Add(repoKeyring)
	f.Add(repoKeyring[:len(repoKeyring)/2])
	f.Add([]byte{0x99, 0x00, 0x05, 0x04, 1, 2, 3, 4})
	f.Add([]byte{0xc6, 0x05, 0x04, 1, 2, 3, 4})
	f.Add([]byte{0x00})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		fps, err := keyringFingerprints(b)
		if err != nil {
			if fps != nil {
				t.Fatalf("fingerprints returned with error %v for %q", err, fz8Head(b))
			}
			return
		}
		for _, fp := range fps {
			if !fingerprint.MatchString(fp) {
				t.Fatalf("fingerprint %q for %q", fp, fz8Head(b))
			}
		}
	})
}
```

`tailscale/stun/fuzz_test.go`:

```go
package stun

import (
	"net/netip"
	"testing"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzParse feeds arbitrary bytes to Parse, and as the value of a mapped
// address attribute to decodeAddress, with an arbitrary transaction id: no
// call panics; a binding request built by Request never parses as a success;
// a refused message returns no transaction id or address, and an accepted one
// a valid address and the id it holds; the XOR-mapped reading of a value
// differs from the plain one by the magic cookie and the transaction id, and
// both are refused or accepted together (R74).
func FuzzParse(f *testing.F) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	v6 := netip.MustParseAddrPort("[2001:db8:1234:5678:11:2233:4455:6677]:32853")
	f.Add(Request(snTx), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx))), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v6, nil)), snAttr(snSoftware, []byte("tailnode"))), snTx[:])
	f.Add(snMessage(snBindingError, snTx, snAttr(snXorMapped, snAddress(v4, &snTx))), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx), snTx[:])
	f.Add(snAddress(v4, nil), snTx[:])
	f.Add(snAddress(v6, &snTx), snTx[:])
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, msg, txBytes []byte) {
		var tx [12]byte
		copy(tx[:], txBytes)
		if _, _, err := Parse(Request(tx)); err == nil {
			t.Fatalf("a binding request with the id %x parsed as a success", tx)
		}

		gotTx, mapped, err := Parse(msg)
		switch {
		case err != nil && (gotTx != [12]byte{} || mapped.IsValid()):
			t.Fatalf("Parse of %x refused the message and returned %x, %v", fz8Head(msg), gotTx, mapped)
		case err == nil && (!mapped.IsValid() || string(gotTx[:]) != string(msg[8:headerSize])):
			t.Fatalf("Parse of %x returned %x, %v", fz8Head(msg), gotTx, mapped)
		}

		plain, errPlain := decodeAddress(attrMappedAddress, msg, nil)
		xored, errXored := decodeAddress(attrXORMappedAddress, msg, &tx)
		if (errPlain == nil) != (errXored == nil) {
			t.Fatalf("decodeAddress of %x: plain %v, XOR %v", fz8Head(msg), errPlain, errXored)
		}
		if errPlain != nil {
			return
		}
		mask := append([]byte{0x21, 0x12, 0xa4, 0x42}, tx[:]...)
		pb, xb := plain.Addr().AsSlice(), xored.Addr().AsSlice()
		if xored.Port() != plain.Port()^0x2112 || len(pb) != len(xb) {
			t.Fatalf("decodeAddress of %x: ports %d and %d, address lengths %d and %d", fz8Head(msg), plain.Port(), xored.Port(), len(pb), len(xb))
		}
		for i := range pb {
			if pb[i]^xb[i] != mask[i] {
				t.Fatalf("decodeAddress of %x: byte %d of the XOR reading is not masked by the cookie and the id", fz8Head(msg), i)
			}
		}
	})
}
```

`tailnet/policy/fuzz_test.go`. The convergence check applies each edit alone to its own result. It skips a document in which an object names a member twice: hujson keeps both spellings, and an edit such as `delete` reaches only the first, so a second `delete /a` on `{"a":1,"a":2}` still changes the text. The no-edit identity, the nil text on error and the re-parse still run for such a document:

```go
package policy

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tailscale/hujson"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz8Repeats reports whether an object anywhere in v names a member twice.
// hujson keeps both, and an edit reaches only the first, so such a document
// is outside the convergence Apply promises.
func fz8Repeats(v hujson.Value) bool {
	switch c := v.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, m := range c.Members {
			name := m.Name.Value.(hujson.Literal).String()
			if seen[name] || fz8Repeats(m.Value) {
				return true
			}
			seen[name] = true
		}
	case *hujson.Array:
		for _, e := range c.Elements {
			if fz8Repeats(e) {
				return true
			}
		}
	}
	return false
}

// FuzzApply feeds arbitrary HuJSON text and two arbitrary edits to Apply: it
// never panics, the text is nil on an error and parses as HuJSON otherwise, no
// edits give the text back byte for byte, and applying either edit to its own
// result changes nothing (R74).
func FuzzApply(f *testing.F) {
	f.Add([]byte(edDoc), "set", "/tagOwners/tag:db", `["autogroup:admin"]`, "addItem", "/acls", `{"action":"accept","src":["tag:web"],"dst":["tag:db:5432"]}`)
	f.Add([]byte(edDoc), "delete", "/derpMap/Regions/900", "", "removeItem", "/tagOwners/tag:web", `"autogroup:admin"`)
	f.Add([]byte(edDoc), "set", "/a~1b", `1`, "set", "/a~", `2`)
	f.Add([]byte(`{}`), "addItem", "/acls", `1`, "removeItem", "/acls", `1`)
	f.Add([]byte(`{"a":1,"a":2}`), "delete", "/a", "", "set", "/a", `3`)
	f.Add([]byte(`[1]`), "set", "/0", `2`, "bogus", "/x", `1`)
	f.Add([]byte(``), "set", "/a", `1`, "set", "/a", `1`)
	f.Fuzz(func(t *testing.T, text []byte, op1, path1, value1, op2, path2, value2 string) {
		edits := []Edit{
			{Op: op1, Path: path1, Value: json.RawMessage(value1)},
			{Op: op2, Path: path2, Value: json.RawMessage(value2)},
		}
		out, err := Apply(text, edits)
		if err != nil && out != nil {
			t.Fatalf("Apply of %q returned text with the error %v", fz8Head(text), err)
		}
		if err == nil {
			if _, err := hujson.Parse(bytes.Clone(out)); err != nil {
				t.Fatalf("Apply of %q returned text that does not parse: %v", fz8Head(text), err)
			}
		}
		doc, err := hujson.Parse(bytes.Clone(text))
		if err != nil {
			return
		}
		if same, err := Apply(text, nil); err != nil || !bytes.Equal(same, text) {
			t.Fatalf("Apply of %q without edits returned %q, %v", fz8Head(text), fz8Head(same), err)
		}
		if fz8Repeats(doc) {
			return
		}
		for _, e := range edits {
			once, err := Apply(text, []Edit{e})
			if err != nil {
				continue
			}
			if twice, err := Apply(once, []Edit{e}); err != nil || !bytes.Equal(twice, once) {
				t.Fatalf("edit %+v of %q does not converge: %v", e, fz8Head(text), err)
			}
		}
	})
}

// FuzzCanonical feeds arbitrary text to Canonical and two arbitrary texts to
// Diff: neither panics, Canonical returns nil on an error and otherwise valid
// JSON that is its own canonical form, Diff of a text with itself is empty,
// every line of a diff is a context, added or removed line, and the lines one
// direction adds are the lines the other removes (R74).
func FuzzCanonical(f *testing.F) {
	f.Add([]byte(edDoc), []byte(`{}`))
	f.Add([]byte("{\"b\":1,\"a\":[1.50,\"é\"], // c\n}"), []byte(`{"a":1}`))
	f.Add([]byte(`// only a comment`), []byte(``))
	f.Add([]byte("a\nb\nc\nd\ne\nf\ng\n"), []byte("a\nb\nX\nd\ne\nf\ng\n"))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		canon, err := Canonical(a)
		switch {
		case err != nil && canon != nil:
			t.Fatalf("Canonical of %q returned text with the error %v", fz8Head(a), err)
		case err == nil && !json.Valid(canon):
			t.Fatalf("Canonical of %q is not valid JSON", fz8Head(a))
		case err == nil:
			if again, err := Canonical(canon); err != nil || !bytes.Equal(again, canon) {
				t.Fatalf("Canonical of %q is not its own canonical form: %v", fz8Head(a), err)
			}
		}
		if d := Diff(a, a); len(d) != 0 {
			t.Fatalf("Diff of %q with itself has %d lines", fz8Head(a), len(d))
		}
		fwd, back := Diff(a, b), Diff(b, a)
		var added, removed, addedBack, removedBack int
		for _, l := range fwd {
			switch l.Op {
			case " ":
			case "+":
				added++
			case "-":
				removed++
			default:
				t.Fatalf("Diff of %q and %q has the operation %q", fz8Head(a), fz8Head(b), l.Op)
			}
		}
		for _, l := range back {
			switch l.Op {
			case "+":
				addedBack++
			case "-":
				removedBack++
			}
		}
		if added != removedBack || removed != addedBack || (len(fwd) > 0 && added+removed == 0) {
			t.Fatalf("Diff of %q and %q adds %d and removes %d lines, its reverse adds %d and removes %d",
				fz8Head(a), fz8Head(b), added, removed, addedBack, removedBack)
		}
	})
}
```

`derp/fuzz_test.go`:

```go
package derp

import (
	"strings"
	"testing"
)

// FuzzParseDERPMap feeds arbitrary policy text to parseDERPMap: it never
// panics, an error starts with the package name and returns an empty map, a
// map without a derpMap member has no regions, and every region of an
// accepted map is filed under its own RegionID, which the policy writes in
// decimal as the key (R74).
func FuzzParseDERPMap(f *testing.F) {
	f.Add(sdPolicyWithRegions)
	f.Add(`{"derpMap": {"Regions": {"abc": {"RegionID": 900}}}}`)
	f.Add(`{"derpMap": {"Regions": {"900": {"RegionID": 901}}}}`)
	f.Add(`{"derpMap": {"Regions": {"+900": {"RegionID": 900}}}}`)
	f.Add(`{"derpMap": {"Regions": {"-0": {"RegionID": 0}, "0": null}}}`)
	f.Add(`{"derpMap": 5}`)
	f.Add(`{"derpMap": null}`)
	f.Add(`{}`)
	f.Add(`{"derpMap": `)
	f.Fuzz(func(t *testing.T, text string) {
		m, err := parseDERPMap(text)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "derp:") || m.Present || m.OmitDefaultRegions || len(m.Regions) != 0 {
				t.Fatalf("parseDERPMap(%q) = %+v, %v", text[:min(len(text), 64)], m, err)
			}
			return
		}
		if !m.Present && (m.OmitDefaultRegions || len(m.Regions) != 0) {
			t.Fatalf("parseDERPMap(%q) holds regions without a derpMap: %+v", text[:min(len(text), 64)], m)
		}
		for id, r := range m.Regions {
			if r.RegionID != id {
				t.Fatalf("parseDERPMap(%q) files region %d under %d", text[:min(len(text), 64)], r.RegionID, id)
			}
		}
	})
}
```

`derp/artifact/fuzz_test.go`:

```go
package artifact

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// FuzzParseManifest feeds arbitrary bytes to ParseManifest: it never panics,
// an error starts with the package name, and a manifest it accepts encodes,
// parses back to itself and encodes to the same bytes (R74).
func FuzzParseManifest(f *testing.F) {
	enc, err := mfManifest().Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(enc)
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts)))
	f.Add([]byte(mfDoc(`"darkpanelVersion":"1.5.0-beta.1"`, mfDerper, mfArtifacts)))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper)))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts, `"extra":1`)))
	f.Add([]byte(`null`))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts) + `{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseManifest(b)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "artifact:") {
				t.Fatalf("ParseManifest(%q) error %q does not start with the package name", b[:min(len(b), 64)], err)
			}
			return
		}
		enc, err := m.Encode()
		if err != nil {
			t.Fatalf("a manifest ParseManifest accepted from %q does not encode: %v", b[:min(len(b), 64)], err)
		}
		back, err := ParseManifest(enc)
		if err != nil || !reflect.DeepEqual(back, m) {
			t.Fatalf("ParseManifest(Encode()) of %q gave %+v, %v; want %+v", b[:min(len(b), 64)], back, err, m)
		}
		if again, err := back.Encode(); err != nil || !bytes.Equal(again, enc) {
			t.Fatalf("Encode of %q is not stable: %v", b[:min(len(b), 64)], err)
		}
	})
}
```

`derp/server/fuzz_test.go`. `ParseCert` answers with one of two fixed texts, so its error can never hold input; a text that quoted the certificate would fail the check. The certificate seed is generated at run time with an ECDSA key, as `svCert` does:

```go
package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

const (
	fz8NoBlock  = "server: no CERTIFICATE block in the PEM data"
	fz8BadBlock = "server: the CERTIFICATE block is not a valid X.509 certificate"
)

// fz8CertPEM is a self-signed certificate that expires on 2027-10-20.
func fz8CertPEM(tb testing.TB) []byte {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "derpkey"},
		NotBefore:    time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2027, 10, 20, 9, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// FuzzParseCert feeds arbitrary bytes to ParseCert: it never panics; an error
// is one of the two fixed texts, so it never echoes the input, and returns
// nothing else; a certificate it accepts parses again from its DER, whose
// NotAfter is the time it returns, in UTC (R74).
func FuzzParseCert(f *testing.F) {
	cert := fz8CertPEM(f)
	params := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{1, 2, 3}})
	f.Add(cert)
	f.Add(append(append([]byte(nil), params...), cert...))
	f.Add(append(append([]byte(nil), cert...), cert...))
	f.Add(params)
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}))
	f.Add(append(append([]byte(nil), cert...), "trailing text"...))
	f.Add([]byte("not a pem file"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		der, notAfter, err := ParseCert(in)
		if err != nil {
			if msg := err.Error(); (msg != fz8NoBlock && msg != fz8BadBlock) || der != nil || !notAfter.IsZero() {
				t.Fatalf("ParseCert(%q) = %q, %v, %q", in[:min(len(in), 64)], der[:min(len(der), 8)], notAfter, msg[:min(len(msg), 64)])
			}
			return
		}
		parsed, err := x509.ParseCertificate(der)
		if err != nil || !parsed.NotAfter.Equal(notAfter) || notAfter.Location() != time.UTC {
			t.Fatalf("ParseCert(%q) returned a DER that does not give back %v in UTC: %v", in[:min(len(in), 64)], notAfter, err)
		}
	})
}
```

`cloudflare/ingress/fuzz_test.go`. A member the code does not model is kept as read, white space included, and written compact, so the first read is not equal to the second; the property is a fixed point after one write and read, with `Hash` equal throughout:

```go
package ingress

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz8CheckConfig fails unless c, read from in, reaches a fixed point after one
// write and read: that write and every later one give the same bytes, the
// second read equals the first, and Hash does not change.
func fz8CheckConfig(t *testing.T, in []byte, c Config) {
	t.Helper()
	first, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("a configuration read from %q does not marshal: %v", fz8Head(in), err)
	}
	var again Config
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("the marshalled configuration of %q does not unmarshal: %v", fz8Head(in), err)
	}
	second, err := again.MarshalJSON()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("the configuration of %q changes between two writes: %v", fz8Head(in), err)
	}
	var third Config
	if err := json.Unmarshal(second, &third); err != nil || !reflect.DeepEqual(again, third) {
		t.Fatalf("the configuration of %q changes between two reads: %v", fz8Head(in), err)
	}
	if h := Hash(c); h != Hash(c) || h != Hash(again) {
		t.Fatalf("Hash of the configuration of %q is not stable", fz8Head(in))
	}
}

// fz8CheckRule is fz8CheckConfig for one rule.
func fz8CheckRule(t *testing.T, in []byte, r Rule) {
	t.Helper()
	first, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("a rule read from %q does not marshal: %v", fz8Head(in), err)
	}
	var again Rule
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("the marshalled rule of %q does not unmarshal: %v", fz8Head(in), err)
	}
	second, err := again.MarshalJSON()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("the rule of %q changes between two writes: %v", fz8Head(in), err)
	}
	var third Rule
	if err := json.Unmarshal(second, &third); err != nil || !reflect.DeepEqual(again, third) {
		t.Fatalf("the rule of %q changes between two reads: %v", fz8Head(in), err)
	}
}

// FuzzConfigJSON feeds arbitrary JSON to Config.UnmarshalJSON and to
// Rule.UnmarshalJSON. Neither panics. A document either accepts reaches a
// fixed point after one write and read: the members it does not model keep
// their content but not their white space, so the first read is compared with
// the second and not with itself. Hash gives the same text for a
// configuration and for the one read back from its marshalled form (R74).
func FuzzConfigJSON(f *testing.F) {
	f.Add([]byte(`{"ingress":[` +
		`{"hostname":"a.example.com","originRequest":{"connectTimeout":30,"httpHostHeader":"in<&>.example","noTLSVerify":true},"service":"https://localhost:8443","x-extra":{"k":[1,2.50,"éé"]}},` +
		`{"service":"http_status:404"}],"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`))
	f.Add([]byte(`{"ingress":[` + inA + `,` + inEnd + `]}`))
	f.Add([]byte(`{ "ingress" : [ { "service" : "http://a:80" , "x" : { "k" : 1 } } ] , "y" : [ 1 , 2 ] }`))
	f.Add([]byte(inA))
	f.Add([]byte(`{"originRequest":null,"service":"s"}`))
	f.Add([]byte(`{"originRequest":{"noTLSVerify":null,"a":1},"service":"s"}`))
	f.Add([]byte(`{"ingress":[null]}`))
	f.Add([]byte(`{"ingress":null}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		var c Config
		if err := json.Unmarshal(in, &c); err == nil {
			fz8CheckConfig(t, in, c)
		}
		var r Rule
		if err := json.Unmarshal(in, &r); err == nil {
			fz8CheckRule(t, in, r)
		}
	})
}
```

`cloudflare/fuzz_test.go`:

```go
package cloudflare

import (
	"net/netip"
	"testing"
)

// fz8Head returns at most the first 64 bytes of s, for a failure message.
func fz8Head(s string) string { return s[:min(len(s), 64)] }

// FuzzServiceHost feeds an arbitrary tunnel service URL to serviceHost and
// serviceProblem, and the URL, and the host read from it, to legacyIPv4: none
// panics; a literal host has a valid address that is not IPv4-mapped; a
// service that serviceProblem accepts has no host in a legacy numeric form,
// so every literal host of it is one netip.ParseAddr reads; and a canonical
// dotted quad gives the same address through legacyIPv4 as through
// netip.ParseAddr (R74).
func FuzzServiceHost(f *testing.F) {
	for _, s := range []string{
		"http://127.0.0.1:80", "http://0x7f.1:80", "https://[::ffff:172.31.255.1]:443", "tcp://example.com:22",
		"http_status:404", "http://172.31.255.1.:80", "ssh://010.0.0.1:22", "http://2887778049:80", "",
		"http://:80", "://", "http://user@a:1/x?y#z", "http://[fe80::1%25eth0]:80", "1.2.3.4", "http://1.2.3.4:8080",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, service string) {
		host, addr, literal := serviceHost(service)
		switch {
		case literal && (host == "" || !addr.IsValid() || addr.Is4In6()):
			t.Fatalf("serviceHost(%q) = %q, %v, literal", fz8Head(service), fz8Head(host), addr)
		case !literal && addr.IsValid():
			t.Fatalf("serviceHost(%q) = %q, %v, not literal", fz8Head(service), fz8Head(host), addr)
		}
		if serviceProblem(service) == "" && literal {
			if _, err := netip.ParseAddr(host); err != nil {
				t.Fatalf("serviceProblem accepts %q, whose host %q is a legacy numeric form", fz8Head(service), fz8Head(host))
			}
		}
		for _, h := range []string{host, service} {
			want, err := netip.ParseAddr(h)
			if err != nil || !want.Is4() {
				continue
			}
			if got, ok := legacyIPv4(h); !ok || got != want {
				t.Fatalf("legacyIPv4(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(h), got, ok, want)
			}
		}
	})
}
```

`npm/proxy/fuzz_test.go`. The Contracts row follows `upstreamAddr`'s order. The existing row of `TestPxCheckUpstream` for `0177.0.0.1` allows it on purpose: nginx's own parser reads a leading zero as decimal and gives `177.0.0.1`, and only a host that parser rejects reaches `inet_aton`, which would read the same text as octal. The check follows `upstreamAddr`'s order, so the reading that counts is the first one that succeeds, and a host whose counting reading is loopback or unspecified, or that is a loopback name, is refused unless the caller is `npm.admin.expose`; the three readers are cross-checked against `netip.ParseAddr` on canonical dotted quads:

```go
package proxy

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fz8Head returns at most the first 64 bytes of s, for a failure message.
func fz8Head(s string) string { return s[:min(len(s), 64)] }

// FuzzCheckUpstream feeds an arbitrary forward host, a port and a calling
// action to CheckUpstream, parseNginxIPv4 and parseInetAton: none panics, a
// refusal is npm.reserved, and a host that nginx reads as a loopback or
// unspecified address, or is a loopback name, is refused unless the caller is
// npm.admin.expose. nginx tries its own parser first and getaddrinfo's
// inet_aton form only when that fails, so the reading that counts is the first
// one that succeeds; a host the later reading takes for loopback is left to the
// first. With nothing reserved and a port other than NPM's admin port, the
// loopback rule is the only one that refuses. A canonical dotted quad is read
// alike by both parsers and by netip.ParseAddr (R74).
func FuzzCheckUpstream(f *testing.F) {
	for _, h := range []string{
		"127.0.0.1", "127.1", "0x7f.1", "2130706433", "0127.0.0.1", "0177.0.0.1", "127...1", "localhost", "LocalHost.",
		"::1", "[::1]", "::ffff:127.0.0.1", "0.0.0.0", "172.18.0.2", "172.31.255.", "darkpanel-npm", "web.1", "10.1",
		"example.com", "", "[", "1.2.3.4",
	} {
		f.Add(h, uint16(81), uint8(0))
		f.Add(h, uint16(8080), uint8(1))
	}
	f.Fuzz(func(t *testing.T, host string, port uint16, which uint8) {
		parent := []string{"", panelOwner, adminOwner, "other.action"}[int(which)%4]
		name := normaliseHost(host)
		addr, isAddr := upstreamAddr(host, name)
		loopback := !isAddr && slices.Contains(loopbackNames, name) || isAddr && (addr.IsLoopback() || addr.IsUnspecified())
		if a, err := netip.ParseAddr(name); err == nil && (a.Unmap().IsLoopback() || a.Unmap().IsUnspecified()) && !loopback {
			t.Fatalf("upstreamAddr(%q) does not read the canonical loopback address it holds", fz8Head(host))
		}
		for _, r := range []Reserved{{}, pxReserved()} {
			err := CheckUpstream(r, host, int(port), parent)
			if err != nil {
				var ce *errcode.Error
				if !errors.As(err, &ce) || ce.Code != errcode.NPMReserved {
					t.Fatalf("CheckUpstream(%q, %d, %q) = %v", fz8Head(host), port, parent, err)
				}
			}
			if loopback && parent != adminOwner && err == nil {
				t.Fatalf("CheckUpstream(%q, %d, %q) lets a loopback host through", fz8Head(host), port, parent)
			}
		}
		if port != npmAdminPort {
			if err := CheckUpstream(Reserved{}, host, int(port), parent); (err != nil) != (loopback && parent != adminOwner) {
				t.Fatalf("CheckUpstream(%q, %d, %q) with nothing reserved = %v, loopback %v", fz8Head(host), port, parent, err, loopback)
			}
		}
		if want, err := netip.ParseAddr(host); err == nil && want.Is4() {
			if got, ok := parseNginxIPv4(host); !ok || got != want {
				t.Fatalf("parseNginxIPv4(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(host), got, ok, want)
			}
			if got, ok := parseInetAton(host); !ok || got != want {
				t.Fatalf("parseInetAton(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(host), got, ok, want)
			}
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify the seeds**

```bash
timeout 900 go test -count=1 -run '^Fuzz' ./internal/modules/tailscale ./internal/modules/tailscale/stun ./internal/modules/tailnet/policy ./internal/modules/derp ./internal/modules/derp/artifact ./internal/modules/derp/server ./internal/modules/cloudflare ./internal/modules/cloudflare/ingress ./internal/modules/npm/proxy
```

Expected: `ok` for the nine packages. A `FAIL` is a finding: Step 3.

- [ ] **Step 3: Implement**

The task adds test code only. A seed run or a Step 4 fuzz run that fails shows a defect (W9-R3): fix it in this task in a non-frozen file of these packages, save the failing input under `<package>/testdata/fuzz/<Target>/` (Go writes it there on a fuzz failure), add a unit test named `TestFz8<Defect>` to the package's `fuzz_test.go`, and commit the fix on its own before the test commit. No defect is known today. One adjacent finding is recorded rather than fixed: `policy.Apply` does not converge on a document with a repeated member name (Step 1, `tailnet/policy`); say so in the report so the wave close can file it.

- [ ] **Step 4: Run the tests to verify the GREEN**

```bash
mkdir -p /root/.cache/w9-tmp/8 && export TMPDIR=/root/.cache/w9-tmp/8 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 900 go test -count=1 -run '^Fuzz' ./internal/modules/tailscale ./internal/modules/tailscale/stun ./internal/modules/tailnet/policy ./internal/modules/derp ./internal/modules/derp/artifact ./internal/modules/derp/server ./internal/modules/cloudflare ./internal/modules/cloudflare/ingress ./internal/modules/npm/proxy
timeout 300 go test -run '^$' -fuzz '^FuzzReadOSRelease$' -fuzztime 30s -parallel 4 ./internal/modules/tailscale 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzDpkgStatus$' -fuzztime 30s -parallel 4 ./internal/modules/tailscale 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzKeyringFingerprints$' -fuzztime 30s -parallel 4 ./internal/modules/tailscale 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s -parallel 4 ./internal/modules/tailscale/stun 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzApply$' -fuzztime 30s -parallel 4 ./internal/modules/tailnet/policy 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzCanonical$' -fuzztime 30s -parallel 4 ./internal/modules/tailnet/policy 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseDERPMap$' -fuzztime 30s -parallel 4 ./internal/modules/derp 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseManifest$' -fuzztime 30s -parallel 4 ./internal/modules/derp/artifact 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzParseCert$' -fuzztime 30s -parallel 4 ./internal/modules/derp/server 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzConfigJSON$' -fuzztime 30s -parallel 4 ./internal/modules/cloudflare/ingress 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzServiceHost$' -fuzztime 30s -parallel 4 ./internal/modules/cloudflare 2>&1 | tail -n 1
timeout 300 go test -run '^$' -fuzz '^FuzzCheckUpstream$' -fuzztime 30s -parallel 4 ./internal/modules/npm/proxy 2>&1 | tail -n 1
git status --short -- '*/testdata/fuzz/*'
```

Expected: `ok` for the nine packages; the last line of each fuzz run begins `ok` and names its package; the last command prints nothing unless a run found a defect. Quote the twelve last lines in the report.

- [ ] **Step 5: Verify the task**

```bash
mkdir -p /root/.cache/w9-tmp/8 && export TMPDIR=/root/.cache/w9-tmp/8 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale ./internal/modules/tailscale/stun ./internal/modules/tailnet/policy ./internal/modules/derp ./internal/modules/derp/artifact ./internal/modules/derp/server ./internal/modules/cloudflare ./internal/modules/cloudflare/ingress ./internal/modules/npm/proxy
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
bash scripts/fuzz.sh --check 2>&1 | rg '^fuzz: \./internal/modules/(tailscale|tailnet|derp|cloudflare|npm)[a-z/]*: ' || echo "no problem left for this task"
git status --short
rm -rf "$TMPDIR" && go clean -testcache && go clean -fuzzcache
```
Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; `golangci-lint` prints `0 issues.`; `go mod tidy -diff` prints nothing; the check filter prints `no problem left for this task` (the check's other lines name the targets of tasks outside this one); `git status --short` lists only this task's files and prints nothing under `testdata/fuzz/` unless a run found a defect (Step 4). Record the real output in the report; a line of the full-suite constraint you skip is named with its reason.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailscale/fuzz_test.go internal/modules/tailscale/stun/fuzz_test.go internal/modules/tailnet/policy/fuzz_test.go internal/modules/derp/fuzz_test.go internal/modules/derp/artifact/fuzz_test.go internal/modules/derp/server/fuzz_test.go internal/modules/cloudflare/ingress/fuzz_test.go internal/modules/cloudflare/fuzz_test.go internal/modules/npm/proxy/fuzz_test.go
git commit -m "test(modules): fuzz tailscale, derp and npm parsers"
```

### Task 9: fuzz: update, release, transport, hostinfo and harness parsers

**Files:**
- Create: `internal/update/fuzz_test.go` (`FuzzParseVersion`, `FuzzChecksumFor`, `FuzzExtractBinary`)
- Create: `release/releasetool/fuzz_test.go` (`FuzzParseVersion`, `FuzzKeyID`, `TestFz9CompareBeyondUint64`)
- Create: `test/harness/nmap_fuzz_test.go` (`//go:build harness`; `FuzzParsePorts`)
- Modify: `internal/transport/token_fuzz_test.go` (append `FuzzParseToken`; `FuzzParseBootstrap` stays as it is)
- Modify: `internal/modules/hostinfo/parse_test.go` (`fz9Seeds` and the five existing targets)
- Modify: `release/releasetool/versions.go` (`compareIdentifier`, Step 3)
- Modify only for a defect a 30 s run finds (W9-R3): the other non-frozen production files of the five packages. Do not touch `internal/update/version_test.go` (Task 4 owns it) or `internal/update/download_test.go`; `FuzzParseVersion` and the extractor and checksum seeds live in the new file.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 9 (package, target, tags, property); "Fuzz runner" (`scripts/fuzz.sh --check`) and "Inventory format" (the rows of `docs/security/parsers.md`), both written by Task 1 and part of this wave's base. Unexported code under test: `update.ParseVersion`, `Version.Compare`, `checksumFor`, `extractBinary`; `releasetool.parseVersion`, `parseTag`, `compare`, `keyID` and the existing test helper `minisig`; `transport.ParseToken` and the existing test helpers `validToken`, `bdBundle`, `encode`; `harness.ParsePorts`, `m3dParsePortEntry`; the `hostinfo` parsers and `testdata/`.
- Produces: the targets `FuzzParseVersion`, `FuzzChecksumFor`, `FuzzExtractBinary` (`./internal/update`), `FuzzParseVersion`, `FuzzKeyID` (`./release/releasetool`), `FuzzParseToken` (`./internal/transport`), `FuzzParsePorts` (`./test/harness`, tag `harness`), and the five `hostinfo` targets seeded from every file under `testdata/`, each under the name of its inventory row. Every new identifier but the targets carries the prefix `fz9`/`Fz9`. No other task consumes anything of this one.

**Items:** `38`

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check. Task 1's inventory already lists the seven new targets, so the check names each one as missing; other waves' packages are filtered out. This output is the RED of the seven new targets:

```bash
mkdir -p /root/.cache/w9-tmp/9 && export TMPDIR=/root/.cache/w9-tmp/9 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg 'internal/update|release/releasetool|internal/transport|test/harness|internal/modules/hostinfo'
```

Expected, in the inventory's order, with no line for `FuzzParseBootstrap` and none for a `hostinfo` target, whose targets exist:

```text
fuzz: ./internal/update: no target FuzzParseVersion
fuzz: ./internal/update: no target FuzzChecksumFor
fuzz: ./internal/update: no target FuzzExtractBinary
fuzz: ./release/releasetool: no target FuzzParseVersion
fuzz: ./release/releasetool: no target FuzzKeyID
fuzz: ./internal/transport: no target FuzzParseToken
fuzz: ./test/harness: no target FuzzParsePorts
```

Then write the files. Each target asserts the property of its Contracts row besides not panicking, and fails with `t.Fatalf` quoting at most 64 bytes of the input through the file's own `fz9Head`. The properties of the three `update` targets and of `FuzzParseToken` hold on the current tree, so their seeds pass at once; the releasetool pair is where the property fails (Step 2).

`FuzzExtractBinary` takes the uncompressed tar as its input and gzips it itself, so mutations reach the tar reader instead of stopping at the gzip header. It walks the whole temporary directory afterwards: every file must be the tarball or the destination, and no directory but the output one may exist.

`internal/update/fuzz_test.go`:

```go
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var fz9HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseVersion checks that a version ParseVersion accepts prints and parses back to itself, and that Compare
// is reflexive and antisymmetric over any two accepted versions.
func FuzzParseVersion(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.0.0-rc.2", "1.0.0-rc.10"},
		{"1.0.0-rc.1", "1.0.0"},
		{"1.2.3", "1.10.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-beta.007", "1.0.0-beta.7"},
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"99999999999999999999.0.0", "0.0.0"},
		{"v1.2.3", "1.2"},
		{"1.2.3+build", "1.2.3-"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := ParseVersion(a)
		vb, errB := ParseVersion(b)
		for _, c := range []struct {
			in  string
			v   Version
			err error
		}{{a, va, errA}, {b, vb, errB}} {
			if c.err != nil {
				continue
			}
			if back, err := ParseVersion(c.v.String()); err != nil || back != c.v {
				t.Fatalf("ParseVersion(%q) = %+v prints as %q, which parses as %+v, %v", fz9Head(c.in), c.v, c.v.String(), back, err)
			}
			if got := c.v.Compare(c.v); got != 0 {
				t.Fatalf("%+v.Compare(itself) = %d, want 0", c.v, got)
			}
		}
		if errA != nil || errB != nil {
			return
		}
		if ab, ba := va.Compare(vb), vb.Compare(va); ab != -ba {
			t.Fatalf("Compare(%q, %q) = %d but Compare(%q, %q) = %d", fz9Head(a), fz9Head(b), ab, fz9Head(b), fz9Head(a), ba)
		}
	})
}

// FuzzChecksumFor checks that a hash checksumFor returns is 64 lower-case hexadecimal characters and is the one
// a line of the input lists for the requested name.
func FuzzChecksumFor(f *testing.F) {
	sum := strings.Repeat("ab", 32)
	const name = "darkpanel_1.3.0_linux_amd64.tar.gz"
	f.Add(sum+"  "+name+"\n", name)
	f.Add(sum+" *"+name+"\n", name)
	f.Add(sum+"  other.tar.gz\n"+sum+"  "+name, name)
	f.Add(sum+"  "+name+"\n"+sum+"  "+name+"\n", name)
	f.Add(strings.ToUpper(sum)+"  "+name+"\n", name)
	f.Add(sum+"\t"+name+"\n", name)
	f.Add("", name)
	f.Fuzz(func(t *testing.T, checksums, name string) {
		got, err := checksumFor([]byte(checksums), name)
		if err != nil {
			return
		}
		if !fz9HexPattern.MatchString(got) {
			t.Fatalf("checksumFor(%q, %q) = %q, want 64 lower-case hexadecimal characters", fz9Head(checksums), fz9Head(name), got)
		}
		lines := strings.Split(checksums, "\n")
		if !slices.Contains(lines, got+"  "+name) && !slices.Contains(lines, got+" *"+name) {
			t.Fatalf("checksumFor(%q, %q) = %q, which no line of the input lists for the name", fz9Head(checksums), fz9Head(name), got)
		}
	})
}

type fz9Entry struct {
	name string
	typ  byte
	body string
	link string
}

func fz9Tar(tb testing.TB, entries ...fz9Entry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Linkname: e.link}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzExtractBinary checks that extractBinary, given a gzipped tar, writes at most one file, and that file is the
// destination. The input is the uncompressed tar, which the target compresses itself so that mutations reach the
// tar reader.
func FuzzExtractBinary(f *testing.F) {
	const reg = tar.TypeReg
	for _, entries := range [][]fz9Entry{
		{{name: "darkpanel", typ: reg, body: "binary"}},
		{{name: "./darkpanel", typ: reg, body: "binary"}},
		{{name: "../darkpanel", typ: reg, body: "binary"}},
		{{name: "sub/darkpanel", typ: reg, body: "binary"}},
		{{name: "LICENSE", typ: reg, body: "text"}, {name: "darkpanel", typ: reg, body: "binary"}},
		{{name: "darkpanel", typ: reg, body: "one"}, {name: "darkpanel", typ: reg, body: "two"}},
		{{name: "darkpanel", typ: tar.TypeSymlink, link: "/etc/passwd"}},
		{{name: "darkpanel", typ: tar.TypeDir}},
		{{name: "LICENSE", typ: reg, body: "text"}},
		{},
	} {
		f.Add(fz9Tar(f, entries...))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		root := t.TempDir()
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		if _, err := zw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		tarball := filepath.Join(root, "release.tar.gz")
		if err := os.WriteFile(tarball, gz.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(root, "out")
		if err := os.Mkdir(out, 0o700); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(out, "darkpanel")

		err := extractBinary(tarball, dst)

		var files []string
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case !d.IsDir():
				files = append(files, path)
			case path != root && path != out:
				t.Fatalf("extractBinary(%q) created the directory %s", fz9Head(string(raw)), path)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
		for _, p := range files {
			if p != tarball && p != dst {
				t.Fatalf("extractBinary(%q) wrote %s, want at most the destination", fz9Head(string(raw)), p)
			}
		}
		if err == nil && !slices.Contains(files, dst) {
			t.Fatalf("extractBinary(%q) succeeded and wrote no destination", fz9Head(string(raw)))
		}
	})
}
```

`release/releasetool/fuzz_test.go`. `FuzzParseVersion` compares `releasetool`'s reading of a version with `update.ParseVersion`'s and `Version.Compare`'s, the property of its Contracts row. The pair of seeds with 20- and 21-digit numeric prerelease identifiers is a case where the two orderings differ on the current tree; `TestFz9CompareBeyondUint64` pins that defect as the named unit test W9-R3 asks for:

```go
package main

import (
	"cmp"
	"regexp"
	"testing"

	"github.com/darkraise/darkpanel/internal/update"
)

var fz9KeyIDPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseVersion checks that every version parseVersion accepts, with or without the v of parseTag, is also
// accepted by update.ParseVersion and orders against any other accepted version with the same sign, and that
// compare is antisymmetric.
func FuzzParseVersion(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.0.0-rc.2", "1.0.0-rc.10"},
		{"1.0.0-rc.1", "1.0.0"},
		{"1.2.3", "1.10.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"1.0.0-18446744073709551616", "1.0.0-18446744073709551615"},
		{"1.0.0-5", "1.0.0-18446744073709551616"},
		{"1.2.3-01", "1.2.3-1"},
		{"v1.2.3", "1.2"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := parseVersion(a)
		vb, errB := parseVersion(b)
		if _, errTag := parseTag("v" + a); (errA == nil) != (errTag == nil) {
			t.Fatalf("parseVersion(%q) error = %v but parseTag of its v form error = %v", fz9Head(a), errA, errTag)
		}
		ua, uerrA := update.ParseVersion(a)
		ub, uerrB := update.ParseVersion(b)
		if errA == nil && uerrA != nil {
			t.Fatalf("parseVersion accepts %q but update.ParseVersion refuses it: %v", fz9Head(a), uerrA)
		}
		if errB == nil && uerrB != nil {
			t.Fatalf("parseVersion accepts %q but update.ParseVersion refuses it: %v", fz9Head(b), uerrB)
		}
		if errA != nil || errB != nil {
			return
		}
		ab, ba := compare(va, vb), compare(vb, va)
		if ab != -ba {
			t.Fatalf("compare(%q, %q) = %d but compare(%q, %q) = %d", fz9Head(a), fz9Head(b), ab, fz9Head(b), fz9Head(a), ba)
		}
		if want := ua.Compare(ub); cmp.Compare(ab, 0) != want {
			t.Fatalf("compare(%q, %q) = %d but update.Version.Compare = %d", fz9Head(a), fz9Head(b), ab, want)
		}
	})
}

// TestFz9CompareBeyondUint64 pins that numeric prerelease identifiers too large for uint64 order by value, as
// update.Version.Compare orders them, and not as text.
func TestFz9CompareBeyondUint64(t *testing.T) {
	for _, tt := range []struct{ lo, hi string }{
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"1.0.0-9999999999999999999999", "1.0.0-10000000000000000000000"},
		{"1.0.0-18446744073709551615", "1.0.0-18446744073709551616"},
		{"1.0.0-18446744073709551616", "1.0.0-99999999999999999999"},
		{"1.0.0-5", "1.0.0-18446744073709551616"},
		{"1.0.0-99999999999999999999", "1.0.0-alpha"},
	} {
		lo, err := parseVersion(tt.lo)
		if err != nil {
			t.Fatal(err)
		}
		hi, err := parseVersion(tt.hi)
		if err != nil {
			t.Fatal(err)
		}
		if got := compare(lo, hi); got != -1 {
			t.Errorf("compare(%s, %s) = %d, want -1", tt.lo, tt.hi, got)
		}
		if got := compare(hi, lo); got != 1 {
			t.Errorf("compare(%s, %s) = %d, want 1", tt.hi, tt.lo, got)
		}
		ulo, _ := update.ParseVersion(tt.lo)
		uhi, _ := update.ParseVersion(tt.hi)
		if got := ulo.Compare(uhi); got != -1 {
			t.Errorf("update.Compare(%s, %s) = %d, want -1", tt.lo, tt.hi, got)
		}
	}
}

// FuzzKeyID checks that a key id keyID returns is 16 upper-case hexadecimal characters.
func FuzzKeyID(f *testing.F) {
	f.Add(minisig("ED", 0x0123456789ABCDEF))
	f.Add(minisig("ED", 1))
	f.Add(minisig("Ed", 1))
	f.Add([]byte("untrusted comment: x\nAAAA\n"))
	f.Add([]byte("untrusted comment: x\n!!!!\n"))
	f.Add([]byte("not a signature\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, sig []byte) {
		id, err := keyID(sig)
		if err == nil && !fz9KeyIDPattern.MatchString(id) {
			t.Fatalf("keyID(%q) = %q, want 16 upper-case hexadecimal characters", fz9Head(string(sig)), id)
		}
	})
}
```

`internal/transport/token_fuzz_test.go`: append the helper and the target below at the end of the file. The file already imports `errors`, `strings` and `errcode`, and `validToken`, `bdBundle` and `encode` are existing helpers of the package's tests.

```go
func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseToken feeds arbitrary input to ParseToken: every refusal is enroll.token_invalid, and an accepted
// token prints with String and parses back to itself.
func FuzzParseToken(f *testing.F) {
	tok := validToken()
	f.Add(tok.String())
	f.Add("  " + tok.String() + "\n")
	f.Add(bdBundle().String())
	f.Add(TokenPrefix + "e30")
	f.Add(encode(`{"a":"controller.example:7443"}`))
	f.Add(encode(`{"a":null,"f":"x","s":"y","h":"z","t":"w"}`))
	f.Add(encode(`{"a":"a<b>:1","f":"` + strings.Repeat("ab", 32) + `","s":"` + ControllerURI("01JCONTROLLER") + `","h":"01JHOST","t":"MDEyMzQ1Njc4OWFiY2RlZg"}`))
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		got, err := ParseToken(in)
		if err != nil {
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseToken(%q) error = %v, want enroll.token_invalid", fz9Head(in), err)
			}
			return
		}
		again, err := ParseToken(got.String())
		if err != nil || again != got {
			t.Fatalf("ParseToken(%q) = %+v, which prints as %q and parses as %+v, %v", fz9Head(in), got, got.String(), again, err)
		}
	})
}
```

`test/harness/nmap_fuzz_test.go` (it carries the `harness` build tag, as `nmap.go` and `nmap_test.go` do, and reads no `nmap` process: its input is text):

```go
//go:build harness

package harness

import (
	"testing"
)

// FuzzParsePorts feeds arbitrary text to ParsePorts and m3dParsePortEntry: every port they accept is from 1 to
// 65535, its protocol is tcp or udp and its state is not empty, and a refusal returns no ports.
func FuzzParsePorts(f *testing.F) {
	f.Add("# Nmap 7.95 scan initiated\nHost: 198.18.0.10 ()\tStatus: Up\n" +
		"Host: 198.18.0.10 ()\tPorts: 22/open/tcp//ssh///, 8080/closed/tcp//http-proxy///, 9090/filtered/tcp//websm///\tIgnored State: filtered (65532)\n# Nmap done\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 53/open|filtered/udp//domain///, 8053/open/udp//unknown///\tIgnored State: closed (1)\n")
	f.Add("Host: 2001:db8:d0::10 ()\tPorts: 443/open/tcp//https///\tIgnored State: filtered (1)\n")
	f.Add("Host: 198.18.0.10 ()\tIgnored State: filtered (65535)\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 0/open/tcp////\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 70000/open/tcp////\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22/open/sctp//ssh///\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22//tcp//ssh///\n")
	f.Add("Host: 198.18.0.10 ()\tPorts: 22/open\n")
	f.Add("22/open/tcp//ssh///")
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		ports, err := ParsePorts(in)
		if err != nil && ports != nil {
			t.Fatalf("ParsePorts(%q) returned %v with the error %v", fz9Head(in), ports, err)
		}
		for _, p := range ports {
			fz9CheckPort(t, in, p)
		}
		if p, err := m3dParsePortEntry(in); err == nil {
			fz9CheckPort(t, in, p)
		}
	})
}

func fz9CheckPort(t *testing.T, in string, p PortState) {
	t.Helper()
	if p.Port == 0 || (p.Proto != "tcp" && p.Proto != "udp") || p.State == "" {
		t.Fatalf("an input %q gave the port entry %+v, want a port from 1 to 65535, tcp or udp, and a state", fz9Head(in), p)
	}
}

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}
```

`internal/modules/hostinfo/parse_test.go`: the five existing targets gain the seeds of every file under `testdata/` (`os-release/*`, `proc-amd64/*`, `proc-arm64/*`) and a doc comment stating their property. Their bodies and their existing `f.Add` seeds stay. Make exactly these changes (the diff shows the first target; the other four get the same two lines, with the comments below):

```diff
--- a/internal/modules/hostinfo/parse_test.go
+++ b/internal/modules/hostinfo/parse_test.go
@@ imports @@
 import (
+	"io/fs"
 	"os"
 	"path/filepath"
 	"testing"
 )
@@ after readFixture @@
+// fz9Seeds adds every file under testdata to f, so each fuzz target starts from the real files of every OS and
+// architecture the fixtures cover.
+func fz9Seeds(f *testing.F) {
+	f.Helper()
+	err := filepath.WalkDir("testdata", func(path string, d fs.DirEntry, err error) error {
+		if err != nil || d.IsDir() {
+			return err
+		}
+		b, err := os.ReadFile(path) //nolint:gosec // G304: a fixture under testdata
+		if err != nil {
+			return err
+		}
+		f.Add(b)
+		return nil
+	})
+	if err != nil {
+		f.Fatal(err)
+	}
+}
@@ each of the five existing targets @@
+// FuzzParseOSRelease checks that parseOSRelease always returns an ID and a PrettyName, whatever the file holds.
 func FuzzParseOSRelease(f *testing.F) {
+	fz9Seeds(f)
 	f.Add([]byte("ID=debian\nPRETTY_NAME=\"Debian\"\n"))

```
The four other comments, each on the line above its `func`:

```go
// FuzzParseCPUInfo checks that parseCPUInfo always returns a model and a processor count of zero or more.
// FuzzParseMemInfo checks that an accepted meminfo never reports more memory or swap available than there is in total.
// FuzzParseLoadAvg checks that an accepted loadavg holds no negative average.
// FuzzParseUptime checks that an accepted uptime is not negative.
```

Every seed of the extended hostinfo targets passes on the current tree: an extension adds seeds only, so its RED is none by nature, and the 30 s runs in Step 4 are its evidence.

- [ ] **Step 2: Run test to verify it fails**

```bash
timeout 300 go test -count=1 -run '^(FuzzParseVersion|TestFz9CompareBeyondUint64)$' ./release/releasetool
```

Expected: FAIL. `releasetool`'s `compare` reads a numeric prerelease identifier that does not fit `uint64` as text, so it orders `99999999999999999999` above `100000000000000000000`, the reverse of `update.Version.Compare`:

```text
--- FAIL: TestFz9CompareBeyondUint64 (0.00s)
    fuzz_test.go:84: compare(1.0.0-99999999999999999999, 1.0.0-100000000000000000000) = 1, want -1
    ...
--- FAIL: FuzzParseVersion (0.00s)
    --- FAIL: FuzzParseVersion/seed#4 (0.00s)
        fuzz_test.go:59: compare("1.0.0-99999999999999999999", "1.0.0-100000000000000000000") = 1 but update.Version.Compare = -1
FAIL
```

The seed numbers and line numbers may differ by one or two; the quoted pair is what matters. Then run the seeds of the other targets, which pass: `timeout 300 go test -count=1 -run 'Fuzz' ./internal/update ./internal/transport ./internal/modules/hostinfo` and `timeout 300 go test -tags harness -count=1 -run '^FuzzParsePorts$' ./test/harness`.

- [ ] **Step 3: Implement**

1. `release/releasetool/versions.go`, `compareIdentifier(a, b string) int` (the root cause: `strconv.ParseUint` fails for a numeric identifier above `uint64`, and the failure sends it down the alphanumeric branch). Decide "numeric" by the text, not by a parse: an identifier is numeric when it is not empty and holds only ASCII digits, which `semverPattern` already guarantees has no leading zero. Two numeric identifiers compare by length first and then with `strings.Compare`, so any length orders by value, as `update.comparePrerelease` orders them; a numeric identifier sorts before an alphanumeric one; two alphanumeric ones compare with `strings.Compare`, as now. Add a small unexported helper `isDigits(s string) bool` for the test of "numeric", and update the doc comment of `compareIdentifier` to say that the order holds beyond `uint64`. `strconv` stays imported: `parseVersion` uses it for the core numbers.
2. Change nothing else. The other new targets and the extended `hostinfo` targets need no production change; if a 30 s run in Step 4 finds a failure, apply W9-R3 of Global Constraints: fix it in the same task, commit the input file the run left under `<package>/testdata/fuzz/<Target>/` as the regression seed, and add a unit test named with the prefix `Fz9`.

- [ ] **Step 4: Run test to verify it passes**

Seeds and the check first, then the 30 s runs, one target per run, anchored. Quote the last line of each run in the report:

```bash
export TMPDIR=/root/.cache/w9-tmp/9 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'Fuzz|Fz9' ./internal/update ./release/releasetool ./internal/transport ./internal/modules/hostinfo
timeout 300 go test -tags harness -count=1 -run '^FuzzParsePorts$' ./test/harness
bash scripts/fuzz.sh --check 2>&1 | rg 'internal/update|release/releasetool|internal/transport|test/harness|internal/modules/hostinfo'   # prints nothing; rg exits 1
for t in FuzzParseVersion FuzzChecksumFor FuzzExtractBinary; do timeout 120 go test -run '^$' -fuzz "^${t}\$" -fuzztime 30s -parallel 4 ./internal/update | tail -n 1; done
for t in FuzzParseVersion FuzzKeyID; do timeout 120 go test -run '^$' -fuzz "^${t}\$" -fuzztime 30s -parallel 4 ./release/releasetool | tail -n 1; done
timeout 120 go test -run '^$' -fuzz '^FuzzParseToken$' -fuzztime 30s -parallel 4 ./internal/transport | tail -n 1
timeout 120 go test -tags harness -run '^$' -fuzz '^FuzzParsePorts$' -fuzztime 30s -parallel 4 ./test/harness | tail -n 1
for t in FuzzParseOSRelease FuzzParseCPUInfo FuzzParseMemInfo FuzzParseLoadAvg FuzzParseUptime; do timeout 120 go test -run '^$' -fuzz "^${t}\$" -fuzztime 30s -parallel 4 ./internal/modules/hostinfo | tail -n 1; done
```

Expected: every line `ok  	github.com/darkraise/darkpanel/<package>	30.<n>s`. The `fuzz:` progress lines may show `0/sec` after the first seconds; the run still executes inputs. Then the full constraint list of Global Constraints, with the real output recorded:

```bash
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 300 go vet -tags harness ./test/... ./cmd/...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/update ./release/releasetool ./internal/transport ./internal/modules/hostinfo
timeout 600 go test -race -tags harness -count=1 -run 'Unit|^FuzzParsePorts$' ./test/harness
timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
timeout 300 go mod tidy -diff
```

Then remove what the task left: `rm -rf /root/.cache/w9-tmp/9; go clean -testcache; go clean -fuzzcache`. Run no VM, Docker or `nmap` command: every target of this task reads text or a temporary directory.

- [ ] **Step 5: Commit**

```bash
git add release/releasetool/versions.go release/releasetool/fuzz_test.go
git commit -m "fix(release): order huge numeric prerelease ids"
git add internal/update/fuzz_test.go
git commit -m "test(update): fuzz versions, checksums, tarballs"
git add internal/transport/token_fuzz_test.go test/harness/nmap_fuzz_test.go internal/modules/hostinfo/parse_test.go
git commit -m "test(fuzz): fuzz tokens, nmap output and hostinfo"
```

If a run left a regression seed under a `testdata/fuzz/` directory, commit it with the fix of its defect, in a commit of its own.


### Task 10: fuzz: backup, secrets, cloud client, events, CLI and install parsers

**Files:**
- Create: `internal/controller/backup/fuzz_test.go` (`FuzzReadHeader`, `FuzzFrames`, `FuzzDecodeJournal`)
- Create: `internal/core/secret/fuzz_test.go` (`FuzzOpen`, `FuzzRedactJSON`; a new test file in a frozen package is allowed)
- Create: `internal/controller/cloud/retryafter_test.go` (`FuzzRetryAfter`, `TestRaRetryAfterOverflow`)
- Create: `internal/controller/events/id_fuzz_test.go` (`FuzzParseID`; `internal/controller/events/**` is frozen for non-test files, and no non-test file changes)
- Create: `internal/cli/follow_fuzz_test.go` (`FuzzPrintEvents`, `TestFz10PrintEventsEscapesDEL`)
- Create: `internal/install/units_fuzz_test.go` (`FuzzCheckAgentAddress`)
- Modify: `internal/controller/cloud/client.go` (`(*Client).retryAfter`, Step 3)
- Modify: `internal/cli/follow.go` (`jsonSafe`, Step 3)
- Modify only for a defect a 30 s run finds (W9-R3): other non-frozen production files of these packages; a defect in a frozen file (`internal/core/**`, `internal/controller/events/**`) stops the task and goes to the integrator.

The parsers sit where the exploration found them: `formatReadHeader`, `formatEncodeHeader`, `frameNewReader`, `frameNewWriter` and `formatReadMembers` are all in `internal/controller/backup/format.go` (there is no `frame.go`), and `checkJournal` is in `recover.go` beside `decodeJournal` in `journal.go`.

**Interfaces:**
- Consumes: Contracts "Fuzz targets" rows of Task 10; "Fuzz runner" and "Inventory format" (Task 1, part of this wave's base); the Contracts entry "Test prefixes", which names `TestRaRetryAfterOverflow` for this task and `fz10` for its helpers; W9-R3 (the `retryAfter` fix). Existing code under test: `backup.formatReadHeader`, `formatEncodeHeader`, `frameNewWriter`, `frameNewReader`, `formatReadMembers`, `decodeJournal`, `checkJournal`, `Targets`, `JournalPath`, `journalPhases`, `Params.Validate`, `Host`; `secret.Sealer.Seal`, `Open`, `RedactJSON` and the test constant `inSchema`; `cloud.Client.retryAfter`, `maxRetryAfter` and the test helpers `clClient`, `clOK`, `clEqualDurations`; `events.ParseID`, `FormatID`; `cli.printEvents`; `install.CheckAgentAddress`.
- Produces: the targets `FuzzReadHeader`, `FuzzFrames`, `FuzzDecodeJournal`, `FuzzOpen`, `FuzzRedactJSON`, `FuzzRetryAfter`, `FuzzParseID`, `FuzzPrintEvents`, `FuzzCheckAgentAddress` under the names of their inventory rows; `TestRaRetryAfterOverflow` (Contracts "Test prefixes"); the `retryAfter` fix (any `Retry-After` above 60 seconds is refused as longer than `maxRetryAfter`, W9-R3); `jsonSafe` escaping DEL. No other task consumes anything of this one.

**Items:** `38`

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Before saving any file below, run the inventory check; the nine new targets are in Task 1's inventory, so it names each as missing. This output is the RED of the targets whose property holds on the current tree:

```bash
mkdir -p /root/.cache/w9-tmp/10 && export TMPDIR=/root/.cache/w9-tmp/10 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
bash scripts/fuzz.sh --check 2>&1 | rg 'internal/controller/backup|internal/core/secret|internal/controller/cloud|internal/controller/events|internal/cli|internal/install'
```

Expected, in the inventory's order:

```text
fuzz: ./internal/controller/backup: no target FuzzReadHeader
fuzz: ./internal/controller/backup: no target FuzzFrames
fuzz: ./internal/controller/backup: no target FuzzDecodeJournal
fuzz: ./internal/core/secret: no target FuzzOpen
fuzz: ./internal/core/secret: no target FuzzRedactJSON
fuzz: ./internal/controller/cloud: no target FuzzRetryAfter
fuzz: ./internal/controller/events: no target FuzzParseID
fuzz: ./internal/cli: no target FuzzPrintEvents
fuzz: ./internal/install: no target FuzzCheckAgentAddress
```

Then write the files. Every file has its own `fz10Head`, which cuts the input quoted in a failure to 64 bytes. No target runs Argon2id, opens a file outside `testdata/` or a temporary directory, or touches the host: `FuzzFrames` opens the frames with a fixed key, and `FuzzDecodeJournal` and `FuzzOpen` work on bytes.

Three decisions the code carries:

- `FuzzFrames` takes the input as a frame stream or, with its second argument `seal` set, as the plaintext archive that the target seals first, so that mutations reach the tar reader. Fuzz inputs cannot forge a tag, so an unsealed stream is accepted only when it is exactly what the frame writer produces for the members read from it; the target seals those members again and compares. The sink wraps its read errors in `ErrMembers`, because a member cut short inside authenticated frames is a malformed archive.
- `FuzzOpen` seals the plaintext itself and derives the ciphertexts it must refuse from it (one byte changed, cut short, extended, other associated data, junk). `Seal` uses a random nonce, and the fuzz engine runs the target in worker processes, so a set of ciphertexts built beforehand would differ from process to process.
- `FuzzDecodeJournal` builds its `Host` over `paths.Under("/fz10")`, a layout nobody reads: `checkJournal` only compares strings, and the target never opens a path. Its seeds are marshalled `Journal` values, one per phase, with and without the four files and a rename.
- `FuzzRedactJSON` marks every secret value of the existing `inSchema` with the prefix `SECRETMARKER` and requires that the prefix never reaches the output; it skips an input whose plain strings contain the marker. It also requires valid JSON and an unmodified input for any document and schema.

`internal/controller/backup/fuzz_test.go`:

```go
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/darkraise/darkpanel/internal/paths"
)

const fz10Tx = "01JTXAAAAAAAAAAAAAAAAAAAAA"

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

func fz10Vector(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// FuzzReadHeader checks that formatReadHeader refuses a header with ErrNotBackup, ErrHeader or ErrTruncated, and
// that a header it accepts passes Params.Validate and encodes back to the bytes it was read from.
func FuzzReadHeader(f *testing.F) {
	good := formatEncodeHeader(Header{Params: TestParams})
	f.Add(good)
	f.Add(formatEncodeHeader(Header{Params: DefaultParams, Salt: [16]byte{1}, NoncePrefix: [16]byte{2}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 10, M: 1 << 20, P: 16}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 0, M: 65536, P: 1}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 1, M: 65535, P: 1}}))
	f.Add(append(bytes.Clone(good), "trailing"...))
	f.Add(good[:HeaderSize-1])
	f.Add(good[:5])
	f.Add([]byte("DPBK"))
	f.Add([]byte("NOTADPBKFILE"))
	f.Add(fz10Vector(f, "short.dpbk")[:HeaderSize])
	f.Add(fz10Vector(f, "multi.dpbk")[:HeaderSize])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		raw := make([]byte, HeaderSize)
		h, err := formatReadHeader(bytes.NewReader(in), raw)
		if err != nil {
			if !errors.Is(err, ErrNotBackup) && !errors.Is(err, ErrHeader) && !errors.Is(err, ErrTruncated) {
				t.Fatalf("formatReadHeader(%q) error = %v, want ErrNotBackup, ErrHeader or ErrTruncated", fz10Head(string(in)), err)
			}
			return
		}
		if err := h.Params.Validate(); err != nil {
			t.Fatalf("formatReadHeader(%q) accepted the parameters %+v, which Validate refuses: %v", fz10Head(string(in)), h.Params, err)
		}
		if got := formatEncodeHeader(h); !bytes.Equal(got, in[:HeaderSize]) || !bytes.Equal(got, raw) {
			t.Fatalf("formatReadHeader(%q) accepted a header that encodes as %q", fz10Head(string(in)), fz10Head(string(got)))
		}
	})
}

// The key and header FuzzFrames uses: argon2id would run once per input under the real derivation, so the target
// opens the frames with a fixed key instead.
func fz10Stream(tb testing.TB) (cipher.AEAD, []byte) {
	tb.Helper()
	aead, err := chacha20poly1305.NewX(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		tb.Fatal(err)
	}
	return aead, formatEncodeHeader(Header{Params: TestParams, Salt: [16]byte{3}, NoncePrefix: [16]byte{4}})
}

// fz10Tar is the tar Write produces for a database member and a key member.
func fz10Tar(tb testing.TB, db, key []byte) []byte {
	tb.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range []struct {
		name string
		body []byte
	}{{MemberDB, db}, {MemberKey, key}} {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.body)), Mode: 0o600}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatal(err)
		}
		if _, err := tw.Write(m.body); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// fz10Seal seals plain into frames with the fixed key, as the frame writer does for the archive Write builds.
func fz10Seal(tb testing.TB, plain []byte) []byte {
	tb.Helper()
	aead, raw := fz10Stream(tb)
	var buf bytes.Buffer
	fw := frameNewWriter(&buf, aead, raw)
	if _, err := fw.Write(plain); err != nil {
		tb.Fatal(err)
	}
	if err := fw.finish(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzFrames checks that frameNewReader and formatReadMembers refuse, with ErrAuth, ErrTruncated or ErrMembers,
// every frame stream the frame writer did not produce. The input is a frame stream as it is, or, with seal set,
// the plaintext archive that the target seals first so that mutations reach the tar reader; an unsealed stream
// that is accepted must equal what the writer produces for the members read from it.
func FuzzFrames(f *testing.F) {
	db, key := bytes.Repeat([]byte("db"), 100), []byte("darkpanel-key-v1:placeholder\n")
	whole := fz10Seal(f, fz10Tar(f, db, key))
	f.Add(whole, false)
	f.Add(whole[:len(whole)-1], false)
	f.Add(whole[:len(whole)/2], false)
	f.Add(append(bytes.Clone(whole), 0), false)
	f.Add(fz10Seal(f, nil), false)
	f.Add(fz10Vector(f, "short.dpbk")[HeaderSize:], false)
	f.Add(fz10Vector(f, "multi.dpbk")[HeaderSize:], false)
	f.Add(fz10Tar(f, db, key), true)
	f.Add(append(fz10Tar(f, db, key), 'x'), true)
	f.Add(fz10Tar(f, db, bytes.Repeat([]byte("k"), MaxKeyMember+1)), true)
	f.Add([]byte{}, true)
	f.Fuzz(func(t *testing.T, in []byte, seal bool) {
		stream := in
		if seal {
			stream = fz10Seal(t, in)
		}
		aead, raw := fz10Stream(t)
		fr := frameNewReader(bufio.NewReaderSize(bytes.NewReader(stream), formatReadBuf), aead, raw)
		var members [][]byte
		err := formatReadMembers(fr, func(_ string, _ int64, body io.Reader) error {
			b, err := io.ReadAll(body)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrMembers, err)
			}
			members = append(members, b)
			return nil
		})
		if err == nil {
			err = fr.drainZero()
		}
		if err != nil {
			if !errors.Is(err, ErrAuth) && !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrMembers) {
				t.Fatalf("frames %q (sealed %v) error = %v, want ErrAuth, ErrTruncated or ErrMembers", fz10Head(string(in)), seal, err)
			}
			return
		}
		if seal {
			return
		}
		if len(members) != 2 || !bytes.Equal(fz10Seal(t, fz10Tar(t, members[0], members[1])), stream) {
			t.Fatalf("frames %q were accepted but are not what the frame writer produces for their members", fz10Head(string(in)))
		}
	})
}

// FuzzDecodeJournal checks that decodeJournal accepts only format 1 with a known phase, and that checkJournal
// accepts only a journal whose tx is an upper-case ULID that its path carries and whose files, when it lists any,
// are the controller's four targets in order with the key recorded as present.
func FuzzDecodeJournal(f *testing.F) {
	h := Host{Paths: paths.Under("/fz10")}
	good := JournalPath(h.Paths.ControllerDir, fz10Tx)
	var files []JournalFile
	for _, target := range Targets(h.Paths) {
		files = append(files, JournalFile{Path: target, Existed: true})
	}
	for _, phase := range journalPhases {
		for _, withFiles := range []bool{false, true} {
			j := Journal{Format: 1, Tx: fz10Tx, Phase: phase}
			if withFiles {
				j.Files = files
				j.Renames = []Rename{{Phase: phase, From: files[0].Path, To: files[0].Path + ".pre", State: RenameIntended}}
			}
			data, err := json.Marshal(j)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data, good)
		}
	}
	f.Add([]byte(`{"format":2,"tx":"`+fz10Tx+`","phase":"begun"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"unknown"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+strings.ToLower(fz10Tx)+`","phase":"begun"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"begun"}`), "/fz10/controller/restore-other.journal")
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"journaled","files":[{"path":"/etc/passwd","existed":true}]}`), good)
	f.Add([]byte(`null`), good)
	f.Add([]byte(``), good)
	f.Fuzz(func(t *testing.T, data []byte, path string) {
		j, err := decodeJournal(path, data)
		if err != nil {
			return
		}
		if j.Format != 1 || !slices.Contains(journalPhases, j.Phase) {
			t.Fatalf("decodeJournal(%q) accepted format %d and phase %q", fz10Head(string(data)), j.Format, j.Phase)
		}
		if checkJournal(h, path, j) != nil {
			return
		}
		if len(j.Tx) != 26 || strings.Trim(j.Tx, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" || JournalPath(h.Paths.ControllerDir, j.Tx) != path {
			t.Fatalf("checkJournal accepted the tx %q for the path %q", j.Tx, fz10Head(path))
		}
		if len(j.Files) == 0 {
			return
		}
		targets := Targets(h.Paths)
		if len(j.Files) != len(targets) {
			t.Fatalf("checkJournal accepted %d files, want %d", len(j.Files), len(targets))
		}
		for i, jf := range j.Files {
			if jf.Path != targets[i] || (jf.Path == h.Paths.ControllerKey && !jf.Existed) {
				t.Fatalf("checkJournal accepted file %d as %+v, want %s present", i, jf, targets[i])
			}
		}
	})
}
```

`internal/core/secret/fuzz_test.go`:

```go
package secret

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzOpen checks that Open opens only what Seal produced. Seal's nonce is random, so the target seals plain
// itself, requires the round trip, and then requires that the ciphertext with one byte changed, cut short,
// extended, or opened under other associated data, and any junk bytes, do not open.
func FuzzOpen(f *testing.F) {
	s, err := NewSealer(bytes.Repeat([]byte{1}, KeySize))
	if err != nil {
		f.Fatal(err)
	}
	const seedAD = "core_users.totp_secret_ct.01JROW"
	f.Add(seedAD, []byte("JBSWY3DPEHPK3PXP"), []byte("junk"), uint(0), byte(1))
	f.Add(seedAD, []byte{}, []byte{}, uint(1), byte(0x80))
	f.Add(seedAD, bytes.Repeat([]byte{0xff}, 300), []byte{0}, uint(40), byte(0xff))
	f.Add("", []byte("x"), []byte{1}, uint(9), byte(1))
	f.Fuzz(func(t *testing.T, ad string, plain, junk []byte, pos uint, mask byte) {
		ct, err := s.Seal(ad, plain)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.Open(ad, ct); err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("Open(Seal(%q)) = %q, %v", fz10Head(string(plain)), fz10Head(string(got)), err)
		}
		at := int(pos % uint(len(ct))) //nolint:gosec // G115: the remainder is below len(ct)
		if mask != 0 {
			changed := bytes.Clone(ct)
			changed[at] ^= mask
			if _, err := s.Open(ad, changed); err == nil {
				t.Fatalf("Open accepted a ciphertext with byte %d changed", at)
			}
		}
		if _, err := s.Open(ad, ct[:at]); err == nil {
			t.Fatalf("Open accepted a ciphertext cut to %d bytes", at)
		}
		if _, err := s.Open(ad+"x", ct); err == nil {
			t.Fatal("Open accepted other associated data")
		}
		if len(junk) > 0 {
			if _, err := s.Open(ad, append(bytes.Clone(ct), junk...)); err == nil {
				t.Fatal("Open accepted a ciphertext with bytes appended")
			}
		}
		if _, err := s.Open(ad, junk); err == nil {
			t.Fatalf("Open accepted the bytes %q that Seal did not produce", fz10Head(string(junk)))
		}
	})
}

const fz10Marker = "SECRETMARKER"

// fz10Document is an inSchema document whose every secret value starts with fz10Marker and whose other strings
// are plain.
func fz10Document(tb testing.TB, plain, secret string) []byte {
	tb.Helper()
	cred := map[string]any{"user": plain, "password": fz10Marker + secret}
	doc, err := json.Marshal(map[string]any{
		"name":  plain,
		"creds": []any{cred, cred},
		"main":  cred,
		"tags":  map[string]any{plain: plain},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return doc
}

// FuzzRedactJSON checks that RedactJSON returns valid JSON for any document and schema and leaves its inputs
// alone, and that no value the schema marks secret appears in the output of a document built from fuzzed strings.
func FuzzRedactJSON(f *testing.F) {
	f.Add("web", "p1", fz10Document(f, "web", "p1"), []byte(inSchema))
	f.Add("", "", []byte(`{"name":"web","creds":[{"user":"a","password":"p1"}],"main":null,"tags":{"env":"prod"},"extra":{"k":[1,2]}}`), []byte(inSchema))
	f.Add("a", "b", []byte(`{"main":{"user":"c","password":{"nested":["x"]}},"name":"web"}`), []byte(`{"$ref":"#/$defs/Missing"}`))
	f.Add("a", "b", []byte(`[1,2,3]`), []byte(`true`))
	f.Add("a", "b", []byte(`{"a":1}{"b":2}`), []byte(inSchema))
	f.Add("a", "b", []byte(`not json`), []byte(`{`))
	f.Add("a", "b", []byte(`{"n":1e999}`), []byte(`{"properties":{"n":{"x-secret":false}}}`))
	f.Fuzz(func(t *testing.T, plain, secret string, raw, schema []byte) {
		rawBefore, schemaBefore := bytes.Clone(raw), bytes.Clone(schema)
		for _, out := range []json.RawMessage{
			RedactJSON(raw, schema),
			RedactJSON(raw, []byte(inSchema)),
			RedactJSON(fz10Document(t, plain, secret), schema),
		} {
			if !json.Valid(out) {
				t.Fatalf("RedactJSON(%q, %q) = %q, which is not valid JSON", fz10Head(string(raw)), fz10Head(string(schema)), fz10Head(string(out)))
			}
		}
		if !bytes.Equal(raw, rawBefore) || !bytes.Equal(schema, schemaBefore) {
			t.Fatalf("RedactJSON(%q, %q) modified its input", fz10Head(string(raw)), fz10Head(string(schema)))
		}
		if strings.Contains(plain, fz10Marker) {
			return
		}
		if out := RedactJSON(fz10Document(t, plain, secret), []byte(inSchema)); bytes.Contains(out, []byte(fz10Marker)) {
			t.Fatalf("RedactJSON left a secret value in %q", fz10Head(string(out)))
		}
	})
}
```

`internal/controller/cloud/retryafter_test.go`. `TestRaRetryAfterOverflow` drives a 429 with the given `Retry-After` through `Do`: a value above 60 seconds must end the retries at once (`cloud.rate_limited`, no sleep, one request) however many digits it has, and 60 seconds still waits. The `slices.Equal` call, not `clEqualDurations`, avoids a gosec G602 report on the older helper. `FuzzRetryAfter` calls `retryAfter` on a client that has only a fixed clock; no request is sent:

```go
package cloud

import (
	"math/big"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestRaRetryAfterOverflow pins that a Retry-After over the 60 second bound ends the retries at once, however
// large the number: one whose seconds overflow time.Duration, or int, must not become a wait of a nanosecond or
// the default backoff.
func TestRaRetryAfterOverflow(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, retryAfter string
		refused          bool
	}{
		{"at the bound", "60", false},
		{"one second over", "61", true},
		{"overflows Duration", "9223372037", true},
		{"largest int64", "9223372036854775807", true},
		{"beyond int64", "99999999999999999999", true},
		{"padded with zeros", "0000000000000000000000000000000061", true},
		{"far future date", "Fri, 31 Dec 9999 23:59:59 GMT", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(http.MethodGet, "/api/x", clOK)
			fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 429, RetryAfter: tc.retryAfter})
			c, sleeps := clClient(t, fake, func(cfg *Config) { cfg.Clock = coretest.NewFakeClock(start) })
			_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
			if !tc.refused {
				if err != nil {
					t.Fatal(err)
				}
				if got := sleeps.got(); !slices.Equal(got, []time.Duration{60 * time.Second}) {
					t.Fatalf("delays = %v, want one of 60s", got)
				}
				return
			}
			if errcode.From(err).Code != errcode.CloudRateLimited {
				t.Fatalf("err = %v, want cloud.rate_limited without waiting", err)
			}
			if got := sleeps.got(); len(got) != 0 {
				t.Fatalf("delays = %v, want none", got)
			}
			if n := len(fake.Requests()); n != 1 {
				t.Fatalf("sent %d requests, want 1", n)
			}
		})
	}
}

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzRetryAfter checks that retryAfter never returns a negative wait, and that a Retry-After of more than 60
// whole seconds, however many digits it has, gives a wait above maxRetryAfter.
func FuzzRetryAfter(f *testing.F) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, v := range []string{
		"", "0", "7", "60", "61", "3600", "+61", "0061", "-1", "soon",
		"9223372036", "9223372037", "9223372036854775807", "9223372036854775808", "99999999999999999999",
		start.Add(30 * time.Second).Format(http.TimeFormat), start.Add(-time.Hour).Format(http.TimeFormat),
		"Fri, 31 Dec 9999 23:59:59 GMT",
	} {
		f.Add(v)
	}
	c := &Client{cfg: Config{Clock: coretest.NewFakeClock(start)}}
	sixty := big.NewInt(60)
	f.Fuzz(func(t *testing.T, v string) {
		got := c.retryAfter(v)
		if got < 0 {
			t.Fatalf("retryAfter(%q) = %v, want a wait of zero or more", fz10Head(v), got)
		}
		if n, ok := new(big.Int).SetString(v, 10); ok && n.Cmp(sixty) > 0 && got <= maxRetryAfter {
			t.Fatalf("retryAfter(%q) = %v, want more than %v", fz10Head(v), got, maxRetryAfter)
		}
	})
}
```

`internal/controller/events/id_fuzz_test.go`:

```go
package events

import (
	"regexp"
	"testing"
)

var fz10EpochPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseID checks that ParseID gives back the epoch and sequence number FormatID was given when the epoch is
// 16 lower-case hexadecimal characters and refuses it otherwise, and that an id it accepts has such an epoch and
// formats and parses back to the same pair.
func FuzzParseID(f *testing.F) {
	const epoch = "0123456789abcdef"
	f.Add(FormatID(epoch, 1), epoch, uint64(1))
	f.Add(FormatID(epoch, 0), epoch, uint64(0))
	f.Add(FormatID(epoch, 1<<64-1), epoch, uint64(1<<64-1))
	f.Add(epoch+"-007", "0123456789abcde", uint64(7))
	f.Add(epoch+"-18446744073709551616", "0123456789ABCDEF", uint64(2))
	f.Add(epoch+"-1-2", epoch+"-1", uint64(3))
	f.Add("-1", "", uint64(4))
	f.Add(epoch, epoch+"0", uint64(5))
	f.Add("", "-", uint64(6))
	f.Fuzz(func(t *testing.T, id, e string, seq uint64) {
		gotE, gotSeq, ok := ParseID(FormatID(e, seq))
		if want := fz10EpochPattern.MatchString(e); ok != want || (ok && (gotE != e || gotSeq != seq)) {
			t.Fatalf("ParseID(FormatID(%q, %d)) = %q, %d, %v", fz10Head(e), seq, fz10Head(gotE), gotSeq, ok)
		}
		parsedE, parsedSeq, ok := ParseID(id)
		if !ok {
			if parsedE != "" || parsedSeq != 0 {
				t.Fatalf("ParseID(%q) refused the id and returned %q, %d", fz10Head(id), parsedE, parsedSeq)
			}
			return
		}
		if !fz10EpochPattern.MatchString(parsedE) {
			t.Fatalf("ParseID(%q) accepted the epoch %q", fz10Head(id), parsedE)
		}
		if againE, againSeq, ok := ParseID(FormatID(parsedE, parsedSeq)); !ok || againE != parsedE || againSeq != parsedSeq {
			t.Fatalf("ParseID(%q) = %q, %d does not survive FormatID", fz10Head(id), parsedE, parsedSeq)
		}
	})
}
```

`internal/cli/follow_fuzz_test.go`. `fz10Hazard` follows the CLI's own definition in `sanitize`: every control character (C0, DEL and C1), the line and paragraph separators and the bidirectional controls, except the line feed that ends a line. `TestFz10PrintEventsEscapesDEL` names the defect the seeds find (W9-R3):

```go
package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// fz10Hazard reports whether r is one a terminal would act on: a control character other than the line feed
// that ends a line (C0, DEL and C1), a line or paragraph separator, or a bidirectional control.
func fz10Hazard(r rune) bool {
	switch {
	case r == '\n':
		return false
	case unicode.IsControl(r), r == 0x061c, r == 0x2028, r == 0x2029, r == 0x200e, r == 0x200f:
		return true
	}
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// FuzzPrintEvents checks that whatever a controller sends, printEvents writes only complete lines of valid JSON,
// none holding a control or bidirectional character a terminal would act on.
func FuzzPrintEvents(f *testing.F) {
	end := "event: end\ndata: {\"reason\":\"done\"}\n\n"
	f.Add("event: data\ndata: {\"line\":\"a\"}\n\n: ping\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"a\u009b31mb\u202ec\u00e9\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"a\u007fb\u2028c\u0085d\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\r\"a\",\t\"n\"\ndata: :\r1}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"\\u001b[31m\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\n\n")
	f.Add("event: end\ndata: {\"reason\":\"error\",\"error\":{\"code\":\"not_found\",\"detail\":\"no container c1\"}}\n\n")
	f.Add("event: data\ndata: \"\x1b[2J\"\n\n" + end)
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		var out strings.Builder
		_ = printEvents(strings.NewReader(in), &out)
		text := out.String()
		if text == "" {
			return
		}
		if !strings.HasSuffix(text, "\n") {
			t.Fatalf("printEvents(%q) wrote %q, which does not end a line", fz10Head(in), fz10Head(text))
		}
		for _, r := range text {
			if fz10Hazard(r) {
				t.Fatalf("printEvents(%q) wrote %q, which holds U+%04X", fz10Head(in), fz10Head(text), r)
			}
		}
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("printEvents(%q) wrote the line %q, which is not JSON", fz10Head(in), fz10Head(line))
			}
		}
	})
}

// TestFz10PrintEventsEscapesDEL pins that a DEL in an event reaches the terminal escaped, as the C1 controls do.
func TestFz10PrintEventsEscapesDEL(t *testing.T) {
	in := "event: data\ndata: {\"line\":\"a\x7fb\"}\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n"
	var out strings.Builder
	if err := printEvents(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if want := `{"line":"a\u007fb"}` + "\n"; out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}
}
```

`internal/install/units_fuzz_test.go`:

```go
package install

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzCheckAgentAddress checks that CheckAgentAddress accepts only host:port with a port from 1 to 65535 and
// no whitespace, quote, percent sign or dollar sign, so the value can sit unquoted in a unit file.
func FuzzCheckAgentAddress(f *testing.F) {
	for _, s := range []string{
		"10.77.0.10:7443", "[2001:db8::1]:7443", "host.example:1", "host.example:65535", "a:65536", "a:0", ":7443",
		"host:7443\n", "host :7443", `ho"st:1`, "ho%st:1", "ho$st:1", "ho'st:1", "host:+80", "host:0443", "[::1]", "", "[]:80",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if CheckAgentAddress(s) != nil {
			return
		}
		if strings.ContainsAny(s, "\"'`%$") || strings.ContainsFunc(s, unicode.IsSpace) {
			t.Fatalf("CheckAgentAddress accepted %q, which holds a character a unit file would read", fz10Head(s))
		}
		host, port, err := net.SplitHostPort(s)
		if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
			t.Fatalf("CheckAgentAddress accepted %q, which is not host:port with a port from 1 to 65535", fz10Head(s))
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
timeout 300 go test -count=1 -run '^(TestRaRetryAfterOverflow|FuzzRetryAfter)$' ./internal/controller/cloud
timeout 300 go test -count=1 -run '^(TestFz10PrintEventsEscapesDEL|FuzzPrintEvents)$' ./internal/cli
```

Expected: FAIL in both packages. In `cloud`, a `Retry-After` of `9223372037` (its seconds overflow `time.Duration` to a negative number that `max` turns into 1 ns) and of `9223372036854775807` makes the client retry at once, and one beyond `int` (`99999999999999999999`, where `strconv.Atoi` fails) is read as none given and gets the default one-second backoff:

```text
--- FAIL: TestRaRetryAfterOverflow (0.00s)
    --- FAIL: TestRaRetryAfterOverflow/overflows_Duration (0.00s)
        retryafter_test.go:48: err = <nil>, want cloud.rate_limited without waiting
    --- FAIL: TestRaRetryAfterOverflow/largest_int64 (0.00s)
    --- FAIL: TestRaRetryAfterOverflow/beyond_int64 (0.00s)
--- FAIL: FuzzRetryAfter (0.00s)
    --- FAIL: FuzzRetryAfter/seed#11 (0.00s)
        retryafter_test.go:87: retryAfter("9223372037") = 1ns, want more than 1m0s
    ...
```

In `cli`, `jsonSafe` escapes controls from U+0080 up, so a DEL (U+007F) inside a JSON string reaches the terminal raw:

```text
--- FAIL: FuzzPrintEvents/seed#2 (0.00s)
    follow_fuzz_test.go:54: printEvents("event: data\ndata: {\"line\":\"a\x7fb\u2028c\u0085d\"}\n\nevent: end\ndata: {\"reas") wrote "{\"line\":\"a\x7fb\\u2028c\\u0085d\"}\n", which holds U+007F
--- FAIL: TestFz10PrintEventsEscapesDEL (0.00s)
```

The seeds of the other seven targets pass on the current tree; their RED is the check of Step 1.

- [ ] **Step 3: Implement**

1. `internal/controller/cloud/client.go`, `(*Client).retryAfter` (W9-R3: bound the parsed seconds before converting them). Parse `v` with `strconv.Atoi` once and keep its error. When it succeeds with a value of zero or more, clamp the seconds to `int(maxRetryAfter/time.Second)+1` (61) before multiplying by `time.Second`, so a larger value gives 61 s, which is over `maxRetryAfter`, and the multiplication can never overflow; keep `max(..., time.Nanosecond)`. When it fails with `strconv.ErrRange` and `v` does not start with `-`, the value is a positive integer beyond `int`: return `maxRetryAfter + time.Second`. Every other value takes the HTTP-date branch and then returns 0, as now. Put a comment at the clamp saying that the multiplication would overflow `Duration` for a large value. `Do` needs no change: `wait > maxRetryAfter` already ends the retries. `errors` and `strings` are already imported.
2. `internal/cli/follow.go`, `jsonSafe`: escape every control or format hazard from U+007F, not U+0080, as a `\u` escape (change the `r >= 0x80` guard to `r >= 0x7f`), and say in the doc comment that DEL is among them, as `sanitize` already counts it. JSON means the same with the escape; `isFormatHazard` and `sanitize` stay as they are.
3. The seven other targets need no production change. If a 30 s run in Step 4 finds a failure, apply W9-R3 of Global Constraints: fix it in a non-frozen file in this task, commit the input file the run left under `<package>/testdata/fuzz/<Target>/` as the regression seed and add a unit test named with the prefix `Fz10`; for a frozen file, stop and report.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w9-tmp/10 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'Fuzz|^TestRa|^TestFz10' ./internal/controller/backup ./internal/core/secret ./internal/controller/cloud ./internal/controller/events ./internal/cli ./internal/install
bash scripts/fuzz.sh --check 2>&1 | rg 'internal/controller/backup|internal/core/secret|internal/controller/cloud|internal/controller/events|internal/cli|internal/install'   # prints nothing; rg exits 1
for t in FuzzReadHeader FuzzFrames FuzzDecodeJournal; do timeout 120 go test -run '^$' -fuzz "^${t}\$" -fuzztime 30s -parallel 4 ./internal/controller/backup | tail -n 1; done
for t in FuzzOpen FuzzRedactJSON; do timeout 120 go test -run '^$' -fuzz "^${t}\$" -fuzztime 30s -parallel 4 ./internal/core/secret | tail -n 1; done
timeout 120 go test -run '^$' -fuzz '^FuzzRetryAfter$' -fuzztime 30s -parallel 4 ./internal/controller/cloud | tail -n 1
timeout 120 go test -run '^$' -fuzz '^FuzzParseID$' -fuzztime 30s -parallel 4 ./internal/controller/events | tail -n 1
timeout 120 go test -run '^$' -fuzz '^FuzzPrintEvents$' -fuzztime 30s -parallel 4 ./internal/cli | tail -n 1
timeout 120 go test -run '^$' -fuzz '^FuzzCheckAgentAddress$' -fuzztime 30s -parallel 4 ./internal/install | tail -n 1
```

Quote the last line of each of the nine runs in the report; each reads `ok  	github.com/darkraise/darkpanel/<package>	30.<n>s`. The `fuzz:` progress lines may show `0/sec` after the first seconds; the run still executes inputs. Then the full constraint list of Global Constraints, with the real output recorded:

```bash
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/controller/backup ./internal/core/secret ./internal/controller/cloud ./internal/controller/events ./internal/cli ./internal/install
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Then remove what the task left: `rm -rf /root/.cache/w9-tmp/10; go clean -testcache; go clean -fuzzcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/cloud/client.go internal/controller/cloud/retryafter_test.go
git commit -m "fix(cloud): refuse an overflowing Retry-After"
git add internal/cli/follow.go internal/cli/follow_fuzz_test.go
git commit -m "fix(cli): escape DEL in streamed events"
git add internal/controller/backup/fuzz_test.go internal/core/secret/fuzz_test.go internal/controller/events/id_fuzz_test.go internal/install/units_fuzz_test.go
git commit -m "test(fuzz): fuzz backup, secret and event parsers"
```

If a run left a regression seed under a `testdata/fuzz/` directory, commit it with the fix of its defect, in a commit of its own.

### Task 11: harness: TestUpgradeMatrix and the vm-upg job

**Files:**
- Create: `test/harness/upgrade_matrix_test.go` (`upgFlag`, `TestUpgradeMatrix` and the Upg-prefixed helpers `upgRunSource`, `upgServeReleases`, `upgInstall`, `upgUpdate`, `upgRollback`)
- Create: `test/harness/upgrade_sources.go` (`//go:build harness`; the sources parser and the build and release lists; a non-test file in the package, as `nmap.go` is, so that the unit tests of Step 1 fail to build until it exists)
- Create: `test/harness/upgrade_sources.txt` (the sources file, Contracts "Upgrade matrix")
- Create: `test/harness/upgrade_matrix_unit_test.go` (`TestUpgUnit*`)
- Create: `test/harness/upgw_workflow_unit_test.go` (`TestUpgwUnitWorkflow`, `TestUpgwUnitWorkflowFile`)
- Modify: `.github/workflows/harness.yml` (the `build` job and the new `vm-upg` job; only Tasks 11 and 21 edit this file, in different waves)

No M2 helper is refactored: the version-parameterised forms are the new Upg-prefixed helpers, and `m2_test.go`, `m2_update_test.go` and `lab_helpers_test.go` stay as they are. `hostsAt`, `startRollout`, `followRollout`, `rolloutHost`, `requireSettled`, `requireFleetVersion`, `oneMinorBehind`, `lastRecord`, `newestTransaction`, `requireNoLocks`, `requireRolledBackFor`, `requireBinaryVersion`, `recordTook` and `updateBudget` already take their versions as arguments and are reused as they are.

**Interfaces:**
- Consumes: Contracts "Upgrade matrix" (flag, sources file format, builds under `bin/harness/`, job `vm-upg`, step and its flags) and W9-R7 and W9-R8; the existing harness test code `fleet`, `m5Configs(lab, o, targetImg, scannerImg, key)` and `Lab.Boot` (the lab pattern of `m5dLab`, which boots the controller, the agent and the scanner on links of the test's own), `harnessImages`, `NewLab`, `copyTree`, `installRole`, `createAdmin`, `login`, `waitHosts`, `localHost`, `writeToken`, `removeToken`, `eventually`, `pollInterval`, `osFlag`, `m2Flag`, `harnessKey`, `releaseURL`, `releaseDir`, `releaseUnit`, `installScript`, `enrollFile`, `agentAddress`, `releaseChannel`, `scannerIPv4`; `release.LoadKey` and `release.Publish`; for the workflow check, the helpers of the existing workflow tests `m3dxExpoJob`, `m3dxExpoLimitRE`, `m3dxExpoTimeoutRE`, `m4lStep`, `m4lStepBefore`, `m4lSetup`, `m4lJobMax` (`m6pw_workflow_unit_test.go` is the newest precedent for the check and its mutation table).
- Produces: `var upgFlag = flag.Bool("upg", ...)` exactly as in Contracts; `TestUpgradeMatrix`; the sources file `test/harness/upgrade_sources.txt`; the `build` job's builds `bin/harness/darkpanel-<source version>` (from each source's commit, tags `harnesskeys`), `darkpanel-1.1.0` (tags `harnesskeys`) and `darkpanel-1.1.1` (tags `harnesskeys,harnessbroken`); the `vm-upg` job with the four-OS matrix, `timeout-minutes: 90` and the step `go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/ -args -os <os> -upg -bin ... -m2 ... -commit ... -workdir "$RUNNER_TEMP/harness-upg" -cache ...`. Task 21 later adds its own steps to the `build` job and its own job; it does not change these.

**Items:** `39`

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Design in short (W9-R7, W9-R8). `TestUpgradeMatrix` reads `upgrade_sources.txt` and, for each source, runs a subtest `from-<version>` that boots a lab of its own (controller, agent and scanner on the test's links), serves the source, `1.1.0` and `1.1.1` as the harness releases, and runs three steps: `install` (install.sh of the build under test installs the source version on the controller and the agent, enrolled through the API), `update` (a rollout to `1.1.0` updates the controller's host, then the agent's, each within `updateBudget`, while `hostinfo.summary` answers on the agent host one minor behind; no lock is left; both hosts report `1.1.0`) and `rollback` (a rollout to `1.1.1`, the build with `harnessbroken`, fails at the controller's host, rolls back within `updateBudget`, keeps the new binary as `darkpanel.failed`, skips the agent host and leaves the fleet at `1.1.0`). The `update` and `rollback` steps are `scenarioUpdateGood` and `scenarioHealthFailure`/`failedUpdate` of `m2_update_test.go` with the versions as parameters. The sources run one after another, so `upgDeadline` (60 minutes, which the step's `-timeout` of 70 minutes covers with the 10 minutes for the VM cleanups) holds `upgDeadline / upgSourceDeadline` sources; `TestUpgUnitSourcesFile` fails when the file lists more, so adding a source then raises the deadline, the step's `-timeout` and the job's `timeout-minutes` together.

The parser, the build list and the release list have their own unit tests, and the workflow has a structure check with a mutation table, so each fails on the current tree without a VM. Create the three test files.

`test/harness/upgrade_matrix_test.go`:

```go
//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var upgFlag = flag.Bool("upg", false, "run TestUpgradeMatrix; needs -os and -m2, and is a go test run of its own")

const (
	// upgSourcesFile is the sources file, read from the package directory.
	upgSourcesFile = "upgrade_sources.txt"
	// upgSourceDeadline bounds one source's run: boot, install, two rollouts.
	// upgDeadline bounds the whole test, so it holds upgDeadline /
	// upgSourceDeadline sources; the workflow step that runs it must keep its
	// -timeout at least upgDeadline plus 10 minutes, for the cleanups that
	// stop the VMs.
	upgSourceDeadline = 25 * time.Minute
	upgDeadline       = 60 * time.Minute
)

// TestUpgradeMatrix is S5's upgrade path (spec 9, Upgrade matrix): for each
// source of upgrade_sources.txt, a controller and an agent installed at the
// source version are updated to the build under test, labelled 1.1.0, while
// hostinfo.summary answers on the agent host one minor behind; a rollout of
// the broken 1.1.1 then rolls back within two minutes and leaves the fleet at
// 1.1.0. Each source boots a lab of its own, and the workflow selects the
// test with -run, -os, -m2 and -upg.
func TestUpgradeMatrix(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*upgFlag {
		t.Skip("no -os, -m2 or -upg given: the VM tests run in .github/workflows/harness.yml")
	}
	sources, err := upgLoadSources(upgSourcesFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgCheckBuilds(*m2Flag, sources); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), upgDeadline)
	t.Cleanup(cancel)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	for _, src := range sources {
		t.Run("from-"+src.Version, func(t *testing.T) {
			sctx, cancel := context.WithTimeout(ctx, upgSourceDeadline)
			t.Cleanup(cancel)
			upgRunSource(sctx, t, o, targetImg, scannerImg, src)
		})
	}
}

// upgRunSource boots a lab, serves the releases of src, and runs the three
// steps of the matrix on it.
func upgRunSource(ctx context.Context, t *testing.T, o OS, targetImg, scannerImg string, src upgSource) {
	t.Helper()
	lab := NewLab(t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m5Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	upgServeReleases(ctx, t, f, upgReleases(*m2Flag, src))
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { upgInstall(ctx, t, f, src.Version) }},
		{"update", true, func(t *testing.T) { upgUpdate(ctx, t, f, src.Version) }},
		{"rollback", false, func(t *testing.T) { upgRollback(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// upgServeReleases signs versions with the harness key, copies them to the
// scanner and serves them there as the release base URL, as serveReleases
// does for the M2 versions.
func upgServeReleases(ctx context.Context, t *testing.T, f *fleet, versions []release.Version) {
	t.Helper()
	key, err := release.LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := release.Publish(dir, key, versions); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(ctx, f.scanner, dir, releaseDir); err != nil {
		t.Fatal(err)
	}
	cmd := fmt.Sprintf("sudo systemd-run --unit=%s /usr/bin/python3 -m http.server 8000 --bind %s --directory %s",
		releaseUnit, scannerIPv4, releaseDir)
	if _, err := f.scanner.Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := f.controller.Run(ctx, "curl -fsS --max-time 10 -o /dev/null "+releaseURL+"/index/releases.json")
		return err
	})
	if err != nil {
		journal, _ := f.scanner.Run(ctx, "sudo journalctl -u "+releaseUnit+" --no-pager -n 30")
		t.Fatalf("the release server does not answer: %v\n%s journal:\n%s", err, releaseUnit, journal)
	}
}

// upgInstall is scenarioInstall at version: install.sh of the build under
// test installs version as the controller and as an agent enrolled with a
// token the API created, and both report version.
func upgInstall(ctx context.Context, t *testing.T, f *fleet, version string) {
	for _, v := range []*vm.VM{f.controller, f.agent} {
		if err := v.CopyFile(ctx, filepath.Join(*m2Flag, "install.sh"), installScript, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRole(ctx, t, f.controller, version, "--role controller --agent-listen "+agentAddress)
	for unit, user := range map[string]string{update.ControllerUnit: install.ServiceUser, update.AgentUnit: "root"} {
		out, err := f.controller.Run(ctx, `stat -c %U "/proc/$(systemctl show -p MainPID --value `+unit+`)"`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != user {
			t.Errorf("%s runs as %q, want %q", unit, got, user)
		}
	}
	createAdmin(ctx, t, f)
	login(ctx, t, f)
	hosts := waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		h, ok := localHost(hosts)
		if !ok {
			return errors.New("no local host listed")
		}
		return hostsAt(version, h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	writeToken(ctx, t, f.agent, grant.Token)
	installRole(ctx, t, f.agent, version, "--role agent --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(version, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), version)
	}
}

// upgUpdate is scenarioUpdateGood from version to the build under test: a
// rollout updates the controller's host, then the agent host, each within two
// minutes, and hostinfo.summary on the agent host works while it is one minor
// behind. No reservation is left.
func upgUpdate(ctx context.Context, t *testing.T, f *fleet, from string) {
	requireSettled(ctx, t, f, from)
	ro := startRollout(ctx, t, f, upgUnderTest)
	var behindErr error
	behind := false
	// The agent host is one minor behind only from the controller host's
	// commit to the agent's restart, a few seconds, so the rollout is polled
	// faster than pollInterval.
	ro = followRollout(ctx, t, f, ro.ID, 250*time.Millisecond, func(cur core.Rollout) {
		ctl, _ := rolloutHost(cur, f.localID)
		ag, _ := rolloutHost(cur, f.agentID)
		if !behind && ctl.State == core.RolloutHostCommitted && ag.State != core.RolloutHostCommitted {
			if behindErr = oneMinorBehind(ctx, f, from, upgUnderTest); behindErr == nil {
				behind = true
			}
		}
	})
	if ro.State != core.RolloutSucceeded {
		t.Fatalf("rollout %s from %s to %s is %s (%v), want %s", ro.ID, from, upgUnderTest, ro.State, ro.Error, core.RolloutSucceeded)
	}
	if !behind {
		t.Errorf("hostinfo.summary never answered on the agent host while it ran %s and the controller %s; last error: %v", from, upgUnderTest, behindErr)
	}
	for _, id := range []string{f.localID, f.agentID} {
		h, _ := rolloutHost(ro, id)
		if h.State != core.RolloutHostCommitted || h.OperationID == nil {
			t.Errorf("rollout host %s is %s, want %s with an operation", id, h.State, core.RolloutHostCommitted)
			continue
		}
		rec := lastRecord(ctx, t, f, id, *h.OperationID)
		if rec.State != core.UpdateCommitted || rec.ToVersion != upgUnderTest {
			t.Errorf("host %s's update ended %s at %s, want %s at %s", id, rec.State, rec.ToVersion, core.UpdateCommitted, upgUnderTest)
		}
		if took := recordTook(rec); took > updateBudget {
			t.Errorf("host %s's update took %v, over %v", id, took, updateBudget)
		}
	}
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireNoLocks(ctx, t, v)
	}
	requireFleetVersion(ctx, t, f, upgUnderTest)
}

// upgRollback is scenarioHealthFailure on the updated fleet: the controller
// of the broken 1.1.1 reports degraded, so its host rolls back within two
// minutes, the rollout stops there, and both hosts keep running 1.1.0.
func upgRollback(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, upgUnderTest)
	agentBefore := newestTransaction(ctx, t, f, f.agentID)
	ro := startRollout(ctx, t, f, upgBroken)
	ro = followRollout(ctx, t, f, ro.ID, pollInterval, nil)
	if ro.State != core.RolloutFailed {
		t.Errorf("rollout %s to %s is %s, want %s", ro.ID, upgBroken, ro.State, core.RolloutFailed)
	}
	ctl, _ := rolloutHost(ro, f.localID)
	if ctl.State != core.RolloutHostFailed || ctl.OperationID == nil {
		t.Fatalf("the controller's host is %s in rollout %s, want %s with an operation", ctl.State, ro.ID, core.RolloutHostFailed)
	}
	if ag, _ := rolloutHost(ro, f.agentID); ag.State != core.RolloutHostSkipped {
		t.Errorf("the agent host is %s in rollout %s, want %s", ag.State, ro.ID, core.RolloutHostSkipped)
	}
	rec := lastRecord(ctx, t, f, f.localID, *ctl.OperationID)
	if rec.State != core.UpdateRolledBack {
		t.Errorf("the controller host's update to %s ended %s, want %s", upgBroken, rec.State, core.UpdateRolledBack)
	}
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
	requireBinaryVersion(ctx, t, f.controller, paths.Production().FailedBinary(), upgBroken)
	requireNoLocks(ctx, t, f.controller)
	if after := newestTransaction(ctx, t, f, f.agentID); after != agentBefore {
		t.Errorf("the agent host ran update %s; the rollout must stop at the first failure", after)
	}
	requireFleetVersion(ctx, t, f, upgUnderTest)
}
```

`test/harness/upgrade_matrix_unit_test.go`:

```go
//go:build harness

package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/release"
)

const upgUnitCommit = "3c5e837d976666d556d0fa1939b7636c67be2a64"

func TestUpgUnitParseSources(t *testing.T) {
	other := strings.Repeat("ab", 20)
	ok := func(lines ...string) string { return strings.Join(lines, "\n") + "\n" }
	baseline := "1.0.0 " + upgUnitCommit
	for _, tc := range []struct {
		name, text string
		want       []upgSource
	}{
		{"the baseline", ok(baseline), []upgSource{{"1.0.0", upgUnitCommit}}},
		{"comments and blank lines", ok("# sources", "", baseline, "   ", "  # indented comment"), []upgSource{{"1.0.0", upgUnitCommit}}},
		{"ascending sources", ok(baseline, "1.0.1 "+other, "1.0.2-rc.1 "+upgUnitCommit),
			[]upgSource{{"1.0.0", upgUnitCommit}, {"1.0.1", other}, {"1.0.2-rc.1", upgUnitCommit}}},
		{"no final newline", baseline, []upgSource{{"1.0.0", upgUnitCommit}}},
		{"windows line ends", "1.0.0 " + upgUnitCommit + "\r\n", []upgSource{{"1.0.0", upgUnitCommit}}},
	} {
		got, err := upgParseSources(tc.text)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: upgParseSources = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, text, want string }{
		{"empty", "", "lists no source"},
		{"comments only", "# nothing\n\n", "lists no source"},
		{"one field", "1.0.0\n", `line 1: want <version> <commit>, got "1.0.0"`},
		{"three fields", "1.0.0 " + upgUnitCommit + " extra\n", "line 1: want <version> <commit>"},
		{"a v prefix", "v1.0.0 " + upgUnitCommit + "\n", "line 1: update: "},
		{"build metadata", "1.0.0+build " + upgUnitCommit + "\n", "line 1: update: "},
		{"not a version", "latest " + upgUnitCommit + "\n", "line 1: update: "},
		{"short commit", "1.0.0 3c5e837\n", `line 1: commit "3c5e837" is not 40 lower-case hexadecimal characters`},
		{"upper-case commit", "1.0.0 " + strings.ToUpper(upgUnitCommit) + "\n", "is not 40 lower-case hexadecimal characters"},
		{"a pre-1.0 source", "0.3.0 " + upgUnitCommit + "\n", "version 0.3.0 is not of major 1"},
		{"a 2.x source", "2.0.0 " + upgUnitCommit + "\n", "version 2.0.0 is not of major 1"},
		{"the build under test", "1.0.0 " + upgUnitCommit + "\n1.1.0 " + other + "\n", "line 2: version 1.1.0 is not older than the build under test, 1.1.0"},
		{"above the build under test", "1.0.0 " + upgUnitCommit + "\n1.2.0 " + other + "\n", "version 1.2.0 is not older"},
		{"first is not the baseline", "1.0.1 " + upgUnitCommit + "\n", "the first source is 1.0.1, want the baseline 1.0.0"},
		{"listed twice", "1.0.0 " + upgUnitCommit + "\n1.0.0 " + other + "\n", "line 2: version 1.0.0 is listed twice"},
		{"descending", "1.0.0 " + upgUnitCommit + "\n1.0.2 " + other + "\n1.0.1 " + upgUnitCommit + "\n", "line 3: version 1.0.1 is not above 1.0.2"},
		{"a pre-release below its release", "1.0.0 " + upgUnitCommit + "\n1.0.1 " + other + "\n1.0.1-rc.1 " + upgUnitCommit + "\n", "line 3: version 1.0.1-rc.1 is not above 1.0.1"},
	} {
		got, err := upgParseSources(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: upgParseSources = %v, %v; want an error naming %q", tc.name, got, err, tc.want)
		}
	}
}

// TestUpgUnitSourcesFile checks the committed sources file: it parses, and
// its sources fit TestUpgradeMatrix's deadline.
func TestUpgUnitSourcesFile(t *testing.T) {
	sources, err := upgLoadSources(upgSourcesFile)
	if err != nil {
		t.Fatal(err)
	}
	if sources[0].Version != "1.0.0" {
		t.Errorf("the first source is %s, want 1.0.0", sources[0].Version)
	}
	if time.Duration(len(sources))*upgSourceDeadline > upgDeadline {
		t.Errorf("%d sources of %v each do not fit the %v deadline: raise upgDeadline, the workflow step's -timeout and the job's timeout-minutes together, or split the matrix",
			len(sources), upgSourceDeadline, upgDeadline)
	}
	if _, err := upgLoadSources(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Error("upgLoadSources of a missing file succeeded")
	}
}

func TestUpgUnitReleases(t *testing.T) {
	src := upgSource{Version: "1.0.0", Commit: upgUnitCommit}
	got := upgReleases("/m2", src)
	want := []release.Version{
		{Version: "1.0.0", Channel: "stable", Binary: "/m2/darkpanel-1.0.0"},
		{Version: "1.1.0", Channel: "stable", Binary: "/m2/darkpanel-1.1.0"},
		{Version: "1.1.1", Channel: "stable", Binary: "/m2/darkpanel-1.1.1"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("upgReleases = %+v, want %+v", got, want)
	}
	if names := upgBuildNames(src); !slices.Equal(names, []string{"darkpanel-1.0.0", "darkpanel-1.1.0", "darkpanel-1.1.1"}) {
		t.Errorf("upgBuildNames = %v", names)
	}
}

func TestUpgUnitCheckBuilds(t *testing.T) {
	dir := t.TempDir()
	sources := []upgSource{{"1.0.0", upgUnitCommit}}
	err := upgCheckBuilds(dir, sources)
	if err == nil || !strings.Contains(err.Error(), "needs darkpanel-1.0.0 in "+dir) {
		t.Fatalf("upgCheckBuilds of an empty directory = %v, want it to name darkpanel-1.0.0", err)
	}
	for _, name := range []string{"darkpanel-1.0.0", "darkpanel-1.1.0"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := upgCheckBuilds(dir, sources); err == nil || !strings.Contains(err.Error(), "needs darkpanel-1.1.1") {
		t.Fatalf("upgCheckBuilds without the broken twin = %v, want it to name darkpanel-1.1.1", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "darkpanel-1.1.1"), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upgCheckBuilds(dir, sources); err != nil {
		t.Errorf("upgCheckBuilds with every build = %v", err)
	}
}
```

`test/harness/upgw_workflow_unit_test.go`:

```go
//go:build harness

package harness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	upgwYmlSourcesStep = "Build the upgrade sources"
	upgwYmlTargetsStep = "Build the upgrade targets"
	upgwYmlRunStep     = "Run the upgrade matrix"
	upgwYmlJob         = "vm-upg"
	// upgwYmlStepTimeout is the -timeout in minutes of the TestUpgradeMatrix
	// step and upgwYmlJobLimit the job's timeout-minutes.
	upgwYmlStepTimeout = 70
	upgwYmlJobLimit    = 90
)

// upgwYmlCheck checks the text of harness.yml for TestUpgradeMatrix: the build
// job checks out the full history, builds every source of the sources file
// from its commit in a worktree, and builds the build under test and its
// broken twin, all before it uploads bin/harness; the vm-upg job has the vm
// jobs' setup, a timeout-minutes of upgwYmlJobLimit, and a step that runs the
// test with the lab flags, a work directory of its own, a -timeout of
// upgwYmlStepTimeout minutes and its failure logs; and nothing outside the job
// names the test, its flag or its work directory.
func upgwYmlCheck(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return fmt.Errorf("harness: the workflow has no build job")
	}
	if !strings.Contains(build, "fetch-depth: 0") {
		return fmt.Errorf("harness: the build job does not check out with fetch-depth: 0, so the source commits are missing")
	}
	for _, s := range []struct {
		name  string
		wants []string
	}{
		{upgwYmlSourcesStep, []string{
			"test/harness/upgrade_sources.txt", "git worktree add --detach", `make -C "$worktree" harness-builds`,
			`HARNESS_BUILDS="$version:harnesskeys"`, "HARNESS_DIR=", `"bin/harness/darkpanel-$version"`, "git worktree remove",
		}},
		{upgwYmlTargetsStep, []string{
			fmt.Sprintf(`make harness-builds HARNESS_BUILDS="%s:harnesskeys %s:harnesskeys,harnessbroken"`, upgUnderTest, upgBroken),
		}},
	} {
		step, ok := m4lStep(build, s.name)
		if !ok {
			return fmt.Errorf("harness: the build job has no step named %q", s.name)
		}
		for _, want := range s.wants {
			if !strings.Contains(step, want) {
				return fmt.Errorf("harness: the step %q lacks %s", s.name, want)
			}
		}
		if !m4lStepBefore(build, step, "name: darkpanel-harness") {
			return fmt.Errorf("harness: the build job does not run the step %q before it uploads the darkpanel-harness artifact", s.name)
		}
	}

	job, ok := m3dxExpoJob(yml, upgwYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", upgwYmlJob)
	}
	for _, text := range []string{"TestUpgradeMatrix", "harness-upg", " -upg"} {
		if strings.Contains(strings.Replace(yml, job, "", 1), text) {
			return fmt.Errorf("harness: the workflow holds %s outside the %s job", text, upgwYmlJob)
		}
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", upgwYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", upgwYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != upgwYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", upgwYmlJob, have, upgwYmlJobLimit)
	}

	run, ok := m4lStep(job, upgwYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", upgwYmlJob, upgwYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestUpgradeMatrix$'", " -upg", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-upg"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", upgwYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", upgwYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != upgwYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", upgwYmlRunStep, m[1], upgwYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < upgDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the deadline %s plus 10 minutes", upgwYmlRunStep, m[1], upgDeadline)
	}
	for _, want := range []string{"name: logs-upg-${{ matrix.os }}", "harness-upg/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", upgwYmlJob, want)
		}
	}
	return nil
}

// upgwYmlDoc has the parts of harness.yml that upgwYmlCheck reads.
const upgwYmlDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@x
        with:
          persist-credentials: false
          fetch-depth: 0
      - run: make harness-builds
      - name: Build the upgrade sources
        run: |
          while read -r version commit <&3; do
            git worktree add --detach "$worktree" "$commit"
            make -C "$worktree" harness-builds HARNESS_BUILDS="$version:harnesskeys" HARNESS_DIR="$scratch/out-$version"
            cp "$scratch/out-$version/darkpanel-$version" "bin/harness/darkpanel-$version"
            git worktree remove --force "$worktree"
          done 3< test/harness/upgrade_sources.txt
      - name: Build the upgrade targets
        run: make harness-builds HARNESS_BUILDS="1.1.0:harnesskeys 1.1.1:harnesskeys,harnessbroken"
      - uses: actions/upload-artifact@x
        with:
          name: darkpanel-harness
          path: bin/harness
  vm-m7r:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
  vm-upg:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 90
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: test -e /dev/kvm
      - run: sudo apt-get install -y qemu-system-x86
      - uses: actions/checkout@x
      - uses: actions/setup-go@x
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-linux-amd64
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-harness
      - uses: actions/cache@x
      - run: go vet -tags harness ./test/...
      - name: Run the upgrade matrix
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/
          -args -os ${{ matrix.os }} -upg
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-upg"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-upg-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-upg/**/*.log
`

func TestUpgwUnitWorkflow(t *testing.T) {
	if err := upgwYmlCheck(upgwYmlDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no build job", "  build:\n", "  other:\n", "no build job"},
		{"shallow checkout", "          fetch-depth: 0\n", "", "fetch-depth: 0"},
		{"no sources step", "name: Build the upgrade sources", "name: Build sources", `no step named "Build the upgrade sources"`},
		{"sources not read from the file", "done 3< test/harness/upgrade_sources.txt", "done 3< sources.txt", "test/harness/upgrade_sources.txt"},
		{"no worktree", "git worktree add --detach", "git checkout", "git worktree add --detach"},
		{"built from the checkout", `make -C "$worktree" harness-builds`, "make harness-builds", `make -C "$worktree" harness-builds`},
		{"source built without the harness key", `HARNESS_BUILDS="$version:harnesskeys"`, `HARNESS_BUILDS="$version:"`, `HARNESS_BUILDS="$version:harnesskeys"`},
		{"source built into the checkout", `HARNESS_DIR="$scratch/out-$version"`, "", "HARNESS_DIR="},
		{"source not copied", `"bin/harness/darkpanel-$version"`, `"bin/other"`, `"bin/harness/darkpanel-$version"`},
		{"worktree kept", "git worktree remove --force", "true", "git worktree remove"},
		{"no targets step", "name: Build the upgrade targets", "name: Build targets", `no step named "Build the upgrade targets"`},
		{"wrong label", `"1.1.0:harnesskeys 1.1.1`, `"1.2.0:harnesskeys 1.1.1`, `HARNESS_BUILDS="1.1.0:harnesskeys 1.1.1:harnesskeys,harnessbroken"`},
		{"twin not broken", "1.1.1:harnesskeys,harnessbroken", "1.1.1:harnesskeys", "harnessbroken"},
		{"steps after the upload", "      - run: make harness-builds\n", "      - uses: actions/upload-artifact@x\n        with:\n          name: darkpanel-harness\n      - run: make harness-builds\n", "before it uploads"},
		{"no vm-upg job", "  vm-upg:\n", "  vm-other:\n", "no vm-upg job"},
		{"limit under 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 80", "timeout-minutes 80, want 90"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 90"},
		{"no needs", "  vm-upg:\n    needs: build\n", "  vm-upg:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no matrix step", "Run the upgrade matrix", "Run upgrades", `no step named "Run the upgrade matrix"`},
		{"wrong test", "'^TestUpgradeMatrix$'", "'^TestUpgradeOther$'", "TestUpgradeMatrix"},
		{"no flag", " -upg\n", "\n", " -upg"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n          -commit \"$GITHUB_SHA\"\n          -workdir \"$RUNNER_TEMP/harness-upg\"", "          -commit \"$GITHUB_SHA\"\n          -workdir \"$RUNNER_TEMP/harness-upg\"", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-upg\"\n", "", "-workdir"},
		{"workdir of vm-m7r", "-workdir \"$RUNNER_TEMP/harness-upg\"", "-workdir \"$RUNNER_TEMP/harness-m7r\"", "harness-upg"},
		{"short timeout", "-timeout 70m -v -run '^TestUpgradeMatrix$'", "-timeout 60m -v -run '^TestUpgradeMatrix$'", "-timeout 60m"},
		{"long timeout", "-timeout 70m -v -run '^TestUpgradeMatrix$'", "-timeout 80m -v -run '^TestUpgradeMatrix$'", "-timeout 80m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-upg/**/*.log\n", "", "harness-upg/**/*.log"},
		{"artifact name of vm-m7r", "name: logs-upg-${{ matrix.os }}", "name: logs-m7r-${{ matrix.os }}", "logs-upg-"},
		{"test added to vm-m7r", "-run '^TestM7Restore$'", "-run '^TestM7Restore$|^TestUpgradeMatrix$'", "TestUpgradeMatrix outside the vm-upg job"},
		{"flag added to vm-m7r", "-args -os ${{ matrix.os }} -m7r", "-args -os ${{ matrix.os }} -m7r -upg", " -upg outside the vm-upg job"},
	} {
		if n := strings.Count(upgwYmlDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(upgwYmlDoc, tc.from, tc.to, 1)
		if err := upgwYmlCheck(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestUpgwUnitWorkflowFile checks the committed workflow, which is the only
// place TestUpgradeMatrix runs.
func TestUpgwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := upgwYmlCheck(string(b)); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w9-tmp/11 && export TMPDIR=/root/.cache/w9-tmp/11 GOTOOLCHAIN=local
df -h / | tail -n 1   # at 90 % or more, run: go clean -cache
timeout 300 go vet -tags harness ./test/harness/
```

Expected: a build failure naming the first undefined identifier of the files that `upgrade_sources.go` will define (the line number may differ):

```text
# github.com/darkraise/darkpanel/test/harness
vet: test/harness/upgrade_matrix_test.go:69:94: undefined: upgSource
```

- [ ] **Step 3: Implement**

1. `test/harness/upgrade_sources.go`, build tag `harness`, package `harness`, imports `errors`, `fmt`, `os`, `path/filepath`, `regexp`, `strings`, `internal/update` and `test/harness/release`. Every identifier is unexported and carries a doc comment that says what it is for.
   - Constants `upgBaseline = "1.0.0"` (the version of the first source), `upgUnderTest = "1.1.0"` (the label of the build under test) and `upgBroken = "1.1.1"` (its twin that fails its health check); `var upgCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)`.
   - `type upgSource struct { Version, Commit string }`.
   - `upgParseSources(text string) ([]upgSource, error)`: splits `text` on newlines, trims each line with `strings.TrimSpace` (so a carriage return goes), ignores a blank line and a line that starts with `#`, and for every other line (numbered from 1 over the whole text, comments and blank lines included) applies these checks in this order, returning the first failure as `harness: upgrade sources: line <n>: <detail>`: not exactly two whitespace-separated fields: `want <version> <commit>, got "<the trimmed line, %q>"`; a version `update.ParseVersion` refuses: its error text as it is (it starts with `update: `); a major other than the major of `upgUnderTest`: `version <v> is not of major <m>, and the updater refuses a step across majors`; a version that is not below `upgUnderTest` by `Version.Compare`: `version <v> is not older than the build under test, 1.1.0`; a commit that does not match `upgCommitPattern`: `commit "<c>" is not 40 lower-case hexadecimal characters`; a version text equal to that of an earlier source: `version <v> is listed twice`; a version that is not strictly above the previous source by `Version.Compare`: `version <v> is not above <previous>, which the line before names`. After the loop: no source gives `harness: upgrade sources: the file lists no source`, and a first source other than `upgBaseline` gives `harness: upgrade sources: the first source is <v>, want the baseline 1.0.0`. It returns the sources in file order.
   - `upgLoadSources(path string) ([]upgSource, error)`: reads the file (`//nolint:gosec // G304: the harness's own sources file`), wraps a read error as `harness: %w`, and returns `upgParseSources` of its text.
   - `upgBuildNames(src upgSource) []string`: `darkpanel-<src.Version>`, `darkpanel-1.1.0`, `darkpanel-1.1.1`, in this order.
   - `upgCheckBuilds(dir string, sources []upgSource) error`: for each source, for each name of `upgBuildNames`, `os.Stat` of `filepath.Join(dir, name)`; the first failure is `harness: the upgrade matrix needs <name> in <dir>; build it as the build job does: <the stat error, wrapped with %w>`.
   - `upgReleases(dir string, src upgSource) []release.Version`: the releases the scanner serves for `src`, ascending: the source, `upgUnderTest` and `upgBroken`, each with `Channel: releaseChannel` and `Binary: filepath.Join(dir, "darkpanel-"+version)`.
2. `test/harness/upgrade_sources.txt`, the sources file. Its commit is the one wave 9 starts from: confirm `git rev-parse 3c5e837^{commit}` prints `3c5e837d976666d556d0fa1939b7636c67be2a64` and `git merge-base --is-ancestor 3c5e837d976666d556d0fa1939b7636c67be2a64 HEAD` succeeds; if either differs, stop and report. Content:

```text
# Upgrade sources of TestUpgradeMatrix, ascending: <version> <commit>.
# Each source is built from its commit with the harness key and installed at
# its version, then updated to the build under test (1.1.0). The first source
# is the baseline, 1.0.0, built from the commit wave 9 started from; add one
# line for each later release tag.
1.0.0 3c5e837d976666d556d0fa1939b7636c67be2a64
```

3. `.github/workflows/harness.yml`, three edits.

   a. In the `build` job's checkout step (the first `actions/checkout` of the file), add `fetch-depth: 0` below `persist-credentials: false`, so that every source commit is in the clone:

```yaml
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
          fetch-depth: 0
```

   b. In the `build` job, directly after the step `- run: make harness-builds` and before the step `Build the dockerapi live test`, add these two steps. The loop reads the sources file on file descriptor 3, so that nothing `make` or `git` starts can consume it; each source is built in a worktree of its own, with the harness key, stamped with its own version (`HARNESS_BUILDS` and `HARNESS_DIR` are the Makefile variables, which a command-line assignment overrides), and only `darkpanel-<version>` is copied, because the worktree's own `install.sh` must not replace the one of the build under test:

```yaml
      # Every upgrade source is built from its own commit, in a worktree of
      # its own, with the harness key and stamped with its own version
      # (test/harness/upgrade_sources.txt); fetch-depth 0 above makes the
      # commits available. The build under test follows as 1.1.0 and its broken
      # twin as 1.1.1, which the upgrade matrix rolls back.
      - name: Build the upgrade sources
        run: |
          set -euo pipefail
          scratch="$(mktemp -d)"
          while read -r version commit <&3; do
            case "$version" in '' | '#'*) continue ;; esac
            worktree="$scratch/src-$version"
            git worktree add --detach "$worktree" "$commit"
            make -C "$worktree" harness-builds HARNESS_BUILDS="$version:harnesskeys" HARNESS_DIR="$scratch/out-$version"
            cp "$scratch/out-$version/darkpanel-$version" "bin/harness/darkpanel-$version"
            git worktree remove --force "$worktree"
          done 3< test/harness/upgrade_sources.txt
          rm -rf "$scratch"
      - name: Build the upgrade targets
        run: make harness-builds HARNESS_BUILDS="1.1.0:harnesskeys 1.1.1:harnesskeys,harnessbroken"
```

   c. At the end of the file, after the `vm-m7r` job, add the `vm-upg` job. It is the `vm-m7r` job with its own step, flags, work directory and logs; its limit is the step's `-timeout` of 70 minutes plus 20 minutes of setup, and the step's `-timeout` covers `upgDeadline` plus the 10 minutes for the VM cleanups:

```yaml
  vm-upg:
    # The upgrade matrix's job, beside vm-m7r. The limit is the step's -timeout
    # plus 20 minutes of setup. TestUpgradeMatrix boots a controller, an agent
    # and a scanner for each source of test/harness/upgrade_sources.txt, one
    # source after another, and does not read -derper.
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 90
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: |
          if [ ! -e /dev/kvm ]; then
            echo "::error::/dev/kvm is missing on this runner; the harness needs KVM (ADR 0008)"
            exit 1
          fi
          echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
          sudo udevadm control --reload-rules
          sudo udevadm trigger --name-match=kvm
      - name: Install QEMU and cloud-image-utils
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends qemu-system-x86 qemu-utils cloud-image-utils
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-linux-amd64
          path: bin
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-harness
          path: bin/harness
      - uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: ~/.cache/darkpanel-harness
          key: harness-images-${{ matrix.os }}-${{ hashFiles('test/harness/images.json') }}
      - run: go vet -tags harness ./test/...
      # TestUpgradeMatrix installs each source's controller and agent, updates
      # them to the build under test while the agent host is one minor behind,
      # and rolls the broken 1.1.1 back. Its own deadline is 60 minutes, and the
      # extra 10 leave its VM cleanups time to finish.
      - name: Run the upgrade matrix
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/
          -args -os ${{ matrix.os }} -upg
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-upg"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-upg-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-upg/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

Then run the unit tests, the YAML check and the build the job runs for the baseline (the build runs the harness build for one source in a scratch directory, no VM, Docker or service involved):

```bash
timeout 300 go test -tags harness -count=1 -run 'TestUpgUnit|TestUpgwUnit' -v ./test/harness/
python3 -I -c 'import sys, yaml; print(sorted(yaml.safe_load(open(sys.argv[1]))["jobs"]))' .github/workflows/harness.yml
python3 -I -c 'import sys, yaml; steps = yaml.safe_load(open(sys.argv[1]))["jobs"]["build"]["steps"]; print(next(s["run"] for s in steps if s.get("name") == "Build the upgrade sources"))' .github/workflows/harness.yml | bash -n
scratch="$(mktemp -d -p /root/.cache/w9-tmp/11)"
git worktree add --detach "$scratch/src-1.0.0" 3c5e837d976666d556d0fa1939b7636c67be2a64
timeout 600 make -C "$scratch/src-1.0.0" harness-builds HARNESS_BUILDS="1.0.0:harnesskeys" HARNESS_DIR="$scratch/out-1.0.0"
"$scratch/out-1.0.0/darkpanel-1.0.0" version --json
git worktree remove --force "$scratch/src-1.0.0"; rm -rf "$scratch"
```

Expected: the unit tests pass (every `TestUpgUnit*` and `TestUpgwUnit*` line `PASS`); the job list ends with `vm-upg`; `bash -n` prints nothing; `version --json` prints a JSON object with `"version":"1.0.0"`.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w9-tmp/11 GOTOOLCHAIN=local
timeout 300 go test -tags harness -count=1 -run 'TestUpgUnit|TestUpgwUnit' -v ./test/harness/
timeout 300 go test -tags harness -count=1 -run '^TestUpgradeMatrix$' -v ./test/harness/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 300 go vet -tags harness ./test/... ./cmd/...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
timeout 300 go mod tidy -diff
```

Expected: the unit tests pass; `TestUpgradeMatrix` prints `--- SKIP` with `no -os, -m2 or -upg given`; every other command as in Global Constraints. Then `rm -rf /root/.cache/w9-tmp/11; go clean -testcache`.

`TestUpgradeMatrix` itself runs only on the harness VMs: this host runs no VM test, so say in the report that the VM run is not done here and is left to the gates at the plan's end (dispatch `harness.yml` on the pushed branch, Global Constraints).

- [ ] **Step 5: Commit**

```bash
git add test/harness/upgrade_sources.go test/harness/upgrade_sources.txt test/harness/upgrade_matrix_test.go test/harness/upgrade_matrix_unit_test.go test/harness/upgw_workflow_unit_test.go .github/workflows/harness.yml
git commit -m "test(harness): add upgrade matrix and vm-upg job"
```


### Task 12: docs: documentation check helpers

**Files:**
- Create: `internal/docs/helpers_check_test.go` (package `docs_test`; tests of the helpers)
- Create: `internal/docs/helpers_test.go` (package `docs_test`; the Contracts "Docs checks" helpers)

**Interfaces:**
- Consumes: `registry.Build()` (existing, `internal/registry/registry.go`) and the `Spec.Internal` flag of `core.ActionSpec` (`internal/core/contract.go`), read, not edited. `internal/docs` is a new test-only package: it has no non-test file.
- Produces: Contracts "Docs checks" (`dcRoot`, `dcRead`, `dcMentions`, `dcModuleNames`, `dcLinks`), plus the unexported `dcMissing` and `dcBroken` behind them. Tasks 16-19 and 24 use the `dc` helpers; Tasks 16-19 each run them on the pages they write.

**Items:** 42

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/docs/helpers_check_test.go`:

````go
package docs_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDcRootIsTheRepositoryRoot(t *testing.T) {
	root := dcRoot(t)
	for _, rel := range []string{"go.mod", filepath.Join("internal", "docs")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("dcRoot = %s, which has no %s: %v", root, rel, err)
		}
	}
}

func TestDcReadReturnsTheFile(t *testing.T) {
	if got := dcRead(t, "go.mod"); !strings.HasPrefix(got, "module github.com/darkraise/darkpanel\n") {
		t.Errorf("dcRead(go.mod) starts %.60q, want the module line", got)
	}
}

func TestDcMentionsAcceptsAFileThatNamesEveryName(t *testing.T) {
	dcMentions(t, "go.mod", "module github.com/darkraise/darkpanel", "go 1.27")
}

func TestDcMissingListsEachAbsentName(t *testing.T) {
	got := dcMissing("alpha beta", []string{"alpha", "gamma", "Beta", "delta", "gamma"})
	if want := []string{"gamma", "Beta", "delta", "gamma"}; !slices.Equal(got, want) {
		t.Errorf("dcMissing = %q, want %q: every absent name in order, matched verbatim and case-sensitively", got, want)
	}
	if got := dcMissing("alpha", nil); len(got) != 0 {
		t.Errorf("dcMissing with no names = %q, want none", got)
	}
}

func TestDcModuleNames(t *testing.T) {
	if got, want := dcModuleNames(t, "hostinfo"), []string{"hostinfo.summary"}; !slices.Equal(got, want) {
		t.Errorf("dcModuleNames(hostinfo) = %q, want %q: a resource counts", got, want)
	}
	cf := dcModuleNames(t, "cloudflare")
	if !slices.IsSorted(cf) || len(slices.Compact(slices.Clone(cf))) != len(cf) {
		t.Errorf("dcModuleNames(cloudflare) = %q, want sorted names without duplicates", cf)
	}
	if !slices.Contains(cf, "cloudflare.tunnel.list") {
		t.Errorf("dcModuleNames(cloudflare) = %q, want cloudflare.tunnel.list among them", cf)
	}
	if slices.Contains(cf, "cloudflare.tunnel.token") {
		t.Errorf("dcModuleNames(cloudflare) lists cloudflare.tunnel.token, an Internal action")
	}
	if core := dcModuleNames(t, "core"); !slices.Contains(core, "core.token.create") {
		t.Errorf("dcModuleNames(core) = %q, want core.token.create among them", core)
	}
}

func TestDcBrokenLinks(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"docs/user/a.md", "docs/user/b.md", "docs/README.md", "docs/api/openapi.json"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Join([]string{
		"[ok](b.md) [up](../README.md#top) [rooted](/docs/README.md) [missing](nope.md) [dir](../api/)",
		"[web](https://example.com/x) [mail](mailto:a@example.com) [anchor](#here) [titled](b.md \"A title\")",
		"[angled](<b.md>) [fragment](gone.md#sec) [query](b.md?x=1) [rootless](/docs/gone.md)",
		"![image](pic.png) and text [not a link] (b.md)",
		"```sh",
		"[hidden](hidden.md)",
		"```",
		"[after the fence](after.md)",
	}, "\n")
	got := dcBroken(root, "docs/user/a.md", text)
	want := []string{"nope.md", "gone.md#sec", "/docs/gone.md", "pic.png", "after.md"}
	if !slices.Equal(got, want) {
		t.Errorf("dcBroken = %q, want %q", got, want)
	}
}
````

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
mkdir -p /root/.cache/w9-tmp/12 && export TMPDIR=/root/.cache/w9-tmp/12 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestDc' ./internal/docs/
```

Expected: a build failure naming the first undefined helper (`undefined: dcRoot`) and `FAIL ... [build failed]`. Quote it.

- [ ] **Step 3: Implement**

The helpers are test code, so the plan gives them whole: create the file below exactly as written.

Create `internal/docs/helpers_test.go`. These are the Contracts "Docs checks" helpers. `dcMissing` and `dcBroken` hold the logic of `dcMentions` and `dcLinks` as functions that return their findings, so `helpers_check_test.go` can test the failure paths without failing a test. Reference-style Markdown links (`[text][label]`) are not read: pages use inline links.

````go
package docs_test

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/registry"
)

// dcRoot is the repository root, found from the test's working directory.
func dcRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod in the working directory or any directory above it")
		}
		dir = parent
	}
}

// dcRead returns the file at the slash path rel under the root, failing when it is missing.
func dcRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dcRoot(t), filepath.FromSlash(rel))) //nolint:gosec // G304: a path under the repository root
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// dcMissing returns the names that text does not contain verbatim, in order.
func dcMissing(text string, names []string) []string {
	var missing []string
	for _, name := range names {
		if !strings.Contains(text, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// dcMentions fails once per name that the file at rel does not contain verbatim, naming both.
func dcMentions(t *testing.T, rel string, names ...string) {
	t.Helper()
	for _, name := range dcMissing(dcRead(t, rel), names) {
		t.Errorf("%s does not mention %q", rel, name)
	}
}

// dcModuleNames returns the sorted names of every action and resource of module id in registry.Build() whose Spec is not Internal.
func dcModuleNames(t *testing.T, id string) []string {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	m, ok := reg.Module(id)
	if !ok {
		t.Fatalf("the registry has no module %q", id)
	}
	var names []string
	for _, a := range m.Actions {
		if !a.Spec.Internal {
			names = append(names, a.Spec.Name)
		}
	}
	for _, r := range m.Resources {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// dcLinkPattern matches the target of an inline Markdown link or image: the
// text after "](" up to the closing parenthesis, a space before a title, or
// the closing angle bracket of a <target>.
var dcLinkPattern = regexp.MustCompile(`\]\(\s*(<[^>]*>|[^)\s]*)`)

// dcSchemePattern matches a URL scheme such as https: or mailto:.
var dcSchemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// dcBroken returns, in order of appearance and as written, the relative link
// targets in text, the content of the file at rel under root, that name no
// existing file or directory. Links inside fenced code blocks, links with a
// URL scheme and links to an anchor of the same file are skipped; a leading
// slash means the repository root.
func dcBroken(root, rel, text string) []string {
	var broken []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range dcLinkPattern.FindAllStringSubmatch(line, -1) {
			written := m[1]
			target := strings.TrimSuffix(strings.TrimPrefix(written, "<"), ">")
			if i := strings.IndexAny(target, "#?"); i >= 0 {
				target = target[:i]
			}
			if target == "" || dcSchemePattern.MatchString(target) {
				continue
			}
			var file string
			if strings.HasPrefix(target, "/") {
				file = filepath.Join(root, filepath.FromSlash(target))
			} else {
				file = filepath.Join(root, filepath.FromSlash(path.Dir(rel)), filepath.FromSlash(target))
			}
			if _, err := os.Stat(file); err != nil {
				broken = append(broken, strings.TrimSuffix(strings.TrimPrefix(written, "<"), ">"))
			}
		}
	}
	return broken
}

// dcLinks fails once per relative Markdown link in the file at rel whose target file does not exist.
func dcLinks(t *testing.T, rel string) {
	t.Helper()
	for _, target := range dcBroken(dcRoot(t), rel, dcRead(t, rel)) {
		t.Errorf("%s links %s, which does not exist", rel, target)
	}
}
````

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
export TMPDIR=/root/.cache/w9-tmp/12 GOTOOLCHAIN=local
timeout 300 go test -count=1 -v -run 'TestDc' ./internal/docs/
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/docs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: every `TestDc*` test passes; `go vet` prints nothing; every line of the full suite is `ok` or `?`; `0 issues.`; no output from `go mod tidy -diff`. Quote the results. Then `rm -rf /root/.cache/w9-tmp/12` and `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/docs
git commit -m "test(docs): add helpers that check documentation"
```

### Task 13: web a11y: login, hosts, host page and create-host dialog

**Files:**
- Create: `web/src/pages/login.a11y.test.tsx`, `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`
- Modify: `web/src/pages/login.tsx`, `web/src/pages/hosts.tsx`, `web/src/pages/host.tsx`, `web/src/pages/create-host-dialog.tsx`, `web/src/pages/audit.tsx`, `web/src/pages/tokens.tsx`
- Read, do not change: the existing `*.test.tsx` of these pages, `web/src/test/utils.tsx` (`renderWithProviders`, `stubFetch`), `web/src/modules/tailnet/host-create.tsx` (the module input the create-host form renders) and `web/src/pages/revoke-token-dialog.tsx`.

**Interfaces:**
- Consumes: Contracts "Accessibility helpers" (`expectNoAxeViolations(el)`, `tabSequence(user, count, opts?)`, `focusedName()` of `web/src/test/a11y.ts`) and "Page title" (`usePageTitle(title)` of `web/src/lib/page-title.ts`, whose result is `<title> · Darkpanel`), both written by Task 2 and part of this task's base; W9-R6. Existing: `renderWithProviders`, `stubFetch`, `SessionProvider`, the pages and `ChangeDialog`.
- Produces: the page titles `Sign in` (login), `Hosts`, `<host name> · <module label>` (host page; see Step 3 for the states without one), `Audit log` and `API tokens`; axe, keyboard and title tests for the pages that Task 23's conformance record cites (`login.a11y.test.tsx`, `hosts.a11y.test.tsx`, `host.a11y.test.tsx`, `create-host-dialog.a11y.test.tsx`); the fixes below. Nothing else consumes these.

**Items:** `41`

**Depends on:** Task 2
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing test**

What the six files check (W9-R6): axe on every state of the page (loading, loaded, empty, error, and every dialog state), the keyboard order of the login form and of the Add host form, that Escape and Done in the Add host dialogs return the focus to the "Add host" button, and the page title. `audit` and `tokens` are not among the core flows; they get the title and axe tests only. The checks that pass on the current tree stay as guards: Escape, Done, the focus order and every axe state except the ones below. The failures on the current tree are the page titles, the heading order of the states that render darkraise-ui's `EmptyState` (its title is an `h3` straight under the page's `h1`) or an `AlertTitle` (an `h5`), and the unlabelled input that shows the enrollment token. Axe in jsdom does not check contrast; Task 22 does. If Task 2's `expectNoAxeViolations` runs axe with options other than its defaults, the set of violations named below may differ; the fixes are for what axe reports.

`web/src/pages/login.a11y.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { SessionProvider } from "@/session/session"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { LoginPage } from "./login"

function problem(status: number, code: string, headers: Record<string, string> = {}): StubResponse {
  return { status, headers, body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code } }
}

function renderLogin(login: StubResponse = problem(401, "auth.invalid_credentials")) {
  stubFetch({ "GET /api/v1/auth/session": problem(401, "auth.required"), "POST /api/v1/auth/login": login })
  return renderWithProviders(
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
      </Routes>
    </SessionProvider>,
    { path: "/login" },
  )
}

test("the login form passes axe", async () => {
  const { container } = renderLogin()
  await screen.findByLabelText("Username")
  await expectNoAxeViolations(container)
})

test("the login form passes axe while the session is being read", async () => {
  stubFetch({ "GET /api/v1/auth/session": () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(
    <SessionProvider>
      <LoginPage />
    </SessionProvider>,
  )
  await screen.findByLabelText("Loading")
  await expectNoAxeViolations(container)
})

test("the login form passes axe with its error shown", async () => {
  const { container } = renderLogin()
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Username"), "admin")
  await user.type(screen.getByLabelText("Password"), "wrong horse")
  await user.type(screen.getByLabelText("Authentication code"), "123456")
  await user.click(screen.getByRole("button", { name: "Log in" }))
  expect((await screen.findByRole("alert")).textContent).toBe("Login failed. Check the username, password and code, then try again.")
  await expectNoAxeViolations(container)
})

test("the keyboard reaches the fields in reading order, starting on the username", async () => {
  renderLogin()
  const user = userEvent.setup()
  await screen.findByLabelText("Username")
  expect(focusedName()).toBe("Username")
  expect(await tabSequence(user, 3)).toEqual(["Password", "Authentication code", "Log in"])
  expect(await tabSequence(user, 2, { backwards: true })).toEqual(["Authentication code", "Password"])
})

test("the keyboard alone submits the form", async () => {
  renderLogin()
  const user = userEvent.setup()
  await screen.findByLabelText("Username")
  await user.keyboard("admin")
  await user.tab()
  await user.keyboard("correct horse battery")
  await user.tab()
  await user.keyboard("123456{Enter}")
  expect((await screen.findByRole("alert")).textContent).toContain("Login failed")
})

test("the page title is Sign in", async () => {
  renderLogin()
  await screen.findByLabelText("Username")
  expect(document.title).toBe("Sign in · Darkpanel")
})
```

`web/src/pages/hosts.a11y.test.tsx`:

```tsx
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Host } from "@/api/types"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"

const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, online: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  {
    id: "h2", name: "db-1", isLocal: false, online: false, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "agent unreachable" },
  },
]

test("the host list passes axe with local, offline and failing-ingestion hosts", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByRole("link", { name: "web-1" })
  await expectNoAxeViolations(container)
})

test("the host list passes axe while it loads", async () => {
  stubFetch({ [HOSTS]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByLabelText("Loading the hosts")
  await expectNoAxeViolations(container)
})

test("the empty host list passes axe", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [] } } } })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByText("No hosts yet")
  await expectNoAxeViolations(container)
})

test("the host list error passes axe", async () => {
  stubFetch({ [HOSTS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } } })
  const { container } = renderWithProviders(<HostsPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the Add host dialog passes axe", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />)
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(screen.getByRole("button", { name: "Add host" }))
  await expectNoAxeViolations(await screen.findByRole("dialog", { name: "Add a host" }))
})

test("the host list has one level-one heading, the page title and a table with named columns", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />)
  await screen.findByRole("link", { name: "web-1" })
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["Hosts"])
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Status", "Modules"])
  expect(document.title).toBe("Hosts · Darkpanel")
})
```

`web/src/pages/host.a11y.test.tsx`:

```tsx
import { screen, waitFor } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Host, ModuleState } from "@/api/types"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { HostPage } from "./host"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const SUMMARY = "GET /api/v1/hosts/h1/resources/hostinfo/summary"

const summary: Summary = {
  hostname: "web-1", os: { id: "debian", versionId: "13", prettyName: "Debian GNU/Linux 13 (trixie)" }, kernel: "6.12.0",
  arch: "amd64", uptimeSeconds: 60, bootTime: "2026-10-19T08:00:00Z", cpu: { model: "QEMU", logicalCount: 1 },
  memory: { totalBytes: 1073741824, availableBytes: 536870912 }, swap: { totalBytes: 0, freeBytes: 0 },
  load: { one: 0, five: 0, fifteen: 0 }, rootFs: { totalBytes: 1073741824, availableBytes: 536870912 }, darkpanelVersion: "0.1.0",
}

function state(id: string, enabled: boolean, available: boolean, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

function host(modules: ModuleState[]): Host {
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules, ingestion: null }
}

function renderHost(path: string, list: StubResponse | (() => Promise<StubResponse>)) {
  stubFetch({ [HOSTS]: list, [SUMMARY]: { body: summary } })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId" element={<HostPage />} />
      <Route path="/hosts/:hostId/:moduleId/*" element={<HostPage />} />
    </Routes>,
    { path },
  )
}

const withModules = (modules: ModuleState[]): StubResponse => ({ body: { output: { hosts: [host(modules)] } } })

test("the host page passes axe with the module's own page", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]))
  await screen.findByText("Debian GNU/Linux 13 (trixie)")
  await expectNoAxeViolations(container)
})

test("the host page passes axe while the host list loads", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", () => new Promise<StubResponse>(() => undefined))
  await screen.findByLabelText("Loading the host")
  await expectNoAxeViolations(container)
})

test("the host page passes axe when the host list fails", async () => {
  const problem = { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" }
  const { container } = renderHost("/hosts/h1/hostinfo", { status: 503, body: problem })
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for an unknown host", async () => {
  const { container } = renderHost("/hosts/nope/hostinfo", withModules([state("hostinfo", true, true)]))
  await screen.findByText("Host not found")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for a host without modules", async () => {
  const { container } = renderHost("/hosts/h1", withModules([state("hostinfo", false, true)]))
  await screen.findByText("No modules are enabled on this host.")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for a module that is not enabled", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", false, true)]))
  await screen.findByText("This module is not enabled on this host.")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for an unavailable module", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, false, "/proc is not mounted")]))
  await screen.findByText("/proc is not mounted")
  await expectNoAxeViolations(container)
})

test("the host page names the host in its heading and marks the current tab", async () => {
  renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]))
  const tab = await screen.findByRole("link", { name: "Host information" })
  expect(tab.getAttribute("aria-current")).toBe("page")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["web-1"])
})

test.each([
  ["a module page", "/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]), "web-1 · Host information · Darkpanel"],
  ["an unavailable module", "/hosts/h1/hostinfo", withModules([state("hostinfo", true, false, "/proc is not mounted")]), "web-1 · Host information · Darkpanel"],
  ["a module that is not enabled", "/hosts/h1/hostinfo", withModules([state("hostinfo", false, true)]), "web-1 · Host information · Darkpanel"],
  ["a host without modules", "/hosts/h1", withModules([state("hostinfo", false, true)]), "web-1 · Darkpanel"],
  ["an unknown host", "/hosts/nope/hostinfo", withModules([state("hostinfo", true, true)]), "Host not found · Darkpanel"],
])("the page title of %s", async (_name, path, list, title) => {
  renderHost(path, list)
  await waitFor(() => expect(document.title).toBe(title))
})

test("the page title is Host while the host list loads", async () => {
  renderHost("/hosts/h1/hostinfo", () => new Promise<StubResponse>(() => undefined))
  await screen.findByLabelText("Loading the host")
  expect(document.title).toBe("Host · Darkpanel")
})
```

`web/src/pages/create-host-dialog.a11y.test.tsx`:

```tsx
import { lazy } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Info } from "lucide-react"
import { expect, test } from "vitest"
import type { Change, Host, Plan } from "@/api/types"
import type { ModuleEntry } from "@/modules/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { CreateHostDialog } from "./create-host-dialog"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PLAN = "POST /api/v1/actions/core.host.create/v1/plan"
const APPLY = "POST /api/v1/actions/core.host.create/v1/apply"

function host(modules: Host["modules"]): Host {
  return { id: "h1", name: "controller", isLocal: true, online: true, modules, ingestion: null }
}

const plan: Plan = {
  planId: "p1", hostId: null, action: "core.host.create", version: 1, inputHash: "h",
  diff: [{ op: "+", text: "host web-2: pending" }], guards: [{ id: "name-free", ok: true, message: "no host is named web-2", overridable: false, overridden: false }],
  expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function change(output: unknown): Change {
  return {
    id: "c1", hostId: null, action: "core.host.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
}

const grant = { host: { id: "h2", name: "web-2" }, token: "dpe_example_enrollment", expiresAt: "2026-10-23T09:00:00Z" }

const tailnet = { id: "tailnet", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

// A module input with a field of its own, so the form's keyboard order covers an extension.
const joinInput = lazy(async () => ({
  default: () => (
    <div>
      <label htmlFor="cha-extra">Extra input</label>
      <input id="cha-extra" />
    </div>
  ),
}))
const entries: ModuleEntry[] = [
  { id: "cha", label: "Extra", icon: Info, routes: lazy(async () => ({ default: () => null })), hostCreate: { id: "cha.extra", render: joinInput } },
]
const extra = { id: "cha", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

async function openForm(modules: Host["modules"] = [], answer: unknown = grant, entriesOf?: ModuleEntry[]) {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [host(modules)] } } },
    [PLAN]: { body: plan },
    [APPLY]: { body: change(answer) },
  })
  const view = renderWithProviders(entriesOf === undefined ? <CreateHostDialog /> : <CreateHostDialog entries={entriesOf} />)
  const user = userEvent.setup()
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(false))
  await user.click(screen.getByRole("button", { name: "Add host" }))
  return { user, dialog: await screen.findByRole("dialog", { name: "Add a host" }), ...view }
}

async function toGrant(answer: unknown) {
  const opened = await openForm([], answer)
  await opened.user.type(within(opened.dialog).getByLabelText("Name"), "web-2")
  await opened.user.click(within(opened.dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await opened.user.click(await within(review).findByRole("button", { name: "Apply" }))
  return { ...opened, done: await screen.findByRole("dialog", { name: "Host added" }) }
}

test("the Add host form passes axe, also with the tailnet input and its tag hint", async () => {
  const { user, dialog } = await openForm([tailnet])
  await expectNoAxeViolations(dialog)
  await user.click(await within(dialog).findByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(within(dialog).getByLabelText("Tags (comma-separated)"), "server")
  expect(within(dialog).getByText("Each tag looks like tag:server.")).toBeTruthy()
  await expectNoAxeViolations(dialog)
})

test("the plan, the enrollment token and the missing-token notice pass axe", async () => {
  const opened = await openForm()
  await opened.user.type(within(opened.dialog).getByLabelText("Name"), "web-2")
  await opened.user.click(within(opened.dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await within(review).findByLabelText("Planned changes")
  await expectNoAxeViolations(review)
  await opened.user.click(within(review).getByRole("button", { name: "Apply" }))
  await expectNoAxeViolations(await screen.findByRole("dialog", { name: "Host added" }))
  opened.unmount()

  const missing = await toGrant({ host: { id: "h2", name: "web-2" } })
  expect(within(missing.done).getByRole("alert")).toBeTruthy()
  await expectNoAxeViolations(missing.done)
})

test("the Add host button passes axe while the host list loads", async () => {
  stubFetch({ [HOSTS]: () => new Promise(() => undefined) })
  const { container } = renderWithProviders(<CreateHostDialog />)
  expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(true)
  await expectNoAxeViolations(container)
})

test("the keyboard moves through the form in reading order and the dialog holds the focus", async () => {
  const { user, dialog } = await openForm([extra], grant, entries)
  await within(dialog).findByLabelText("Extra input")
  expect(focusedName()).toBe("Name")
  expect(await tabSequence(user, 5)).toEqual(["Controller address (optional)", "Extra input", "Continue", "Close", "Name"])
  expect(await tabSequence(user, 2, { backwards: true })).toEqual(["Close", "Continue"])
  expect(dialog.contains(document.activeElement)).toBe(true)
})

test("Escape closes the form and the focus returns to the Add host button", async () => {
  const { user } = await openForm()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Escape closes the plan dialog and the focus returns to the Add host button", async () => {
  const { user, dialog } = await openForm()
  await user.type(within(dialog).getByLabelText("Name"), "web-2")
  await user.click(within(dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await within(review).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Done closes the enrollment token dialog and the focus returns to the Add host button", async () => {
  const { user, done } = await toGrant(grant)
  expect(within(done).getByDisplayValue("dpe_example_enrollment")).toBeTruthy()
  await user.click(within(done).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Escape closes the enrollment token dialog and the focus returns to the Add host button", async () => {
  const { user } = await toGrant(grant)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})
```

`web/src/pages/audit.a11y.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { AuditEntry } from "@/api/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { AuditPage } from "./audit"

const AUDIT = "POST /api/v1/actions/core.audit.list/v1/read"

function entry(id: string, action: string, extra: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id, at: "2026-10-19T08:00:00Z", actor: "user:u1", hostId: null, action, version: 1, phase: "apply",
    changeId: null, input: {}, result: "ok", errorCode: null, source: "127.0.0.1", ...extra,
  }
}

function pages(call: FetchCall): StubResponse {
  const input = (call.body as { input: { before?: string } }).input
  return input.before === undefined
    ? { body: { output: { entries: [entry("a2", "core.token.create", { hostId: "h1", changeId: "c2" })], nextBefore: "a2" } } }
    : { body: { output: { entries: [entry("a1", "core.auth.login", { result: "error", errorCode: "auth.invalid_credentials" })], nextBefore: null } } }
}

const failure: StubResponse = { status: 500, body: { type: "about:blank", title: "Internal", status: 500, detail: "older page failed", code: "internal" } }

test("the audit log passes axe with entries, a linked change and a failed result", async () => {
  stubFetch({ [AUDIT]: pages })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  await expectNoAxeViolations(container)
  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  await screen.findByText("core.auth.login/v1")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe while it loads", async () => {
  stubFetch({ [AUDIT]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByLabelText("Loading the audit log")
  await expectNoAxeViolations(container)
})

test("the empty audit log passes axe", async () => {
  stubFetch({ [AUDIT]: { body: { output: { entries: [], nextBefore: null } } } })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("No audit entries yet")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe when the first page fails", async () => {
  stubFetch({ [AUDIT]: failure })
  const { container } = renderWithProviders(<AuditPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("older page failed")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe when an older page fails beside the loaded entries", async () => {
  stubFetch({ [AUDIT]: (call) => ((call.body as { input: { before?: string } }).input.before === undefined ? pages(call) : failure) })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  expect((await screen.findByRole("alert")).textContent).toBe("older page failed")
  await expectNoAxeViolations(container)
})

test("the keyboard reaches the change link and then the load button", async () => {
  stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  const user = userEvent.setup()
  await screen.findByText("core.token.create/v1")
  expect(await tabSequence(user, 2)).toEqual(["core.token.create/v1", "Load older entries"])
})

test("the audit log has a level-one heading, named columns and the page title Audit log", async () => {
  stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["Audit log"])
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Time", "Actor", "Host", "Action", "Result"])
  expect(document.title).toBe("Audit log · Darkpanel")
})
```

`web/src/pages/tokens.a11y.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Plan, TokenInfo } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { TokensPage } from "./tokens"

const LIST = "POST /api/v1/actions/core.token.list/v1/read"
const REVOKE_PLAN = "POST /api/v1/actions/core.token.revoke/v1/plan"

function token(id: string, name: string, extra: Partial<TokenInfo> = {}): TokenInfo {
  return {
    id, name, scope: "read", createdAt: "2026-10-01T08:00:00Z", expiresAt: "2999-01-01T00:00:00Z",
    lastUsedAt: null, revokedAt: null, ...extra,
  }
}

const tokens = [
  token("tok1", "ci"),
  token("tok2", "old", { revokedAt: "2026-10-10T08:00:00Z" }),
  token("tok3", "stale", { expiresAt: "2000-01-01T00:00:00Z" }),
]

const revokePlan: Plan = {
  planId: "p2", hostId: null, action: "core.token.revoke", version: 1, inputHash: "h",
  diff: [{ op: "-", text: "token ci" }], guards: [], expiresAt: "2026-10-19T09:00:00Z", confirmation: "tok1",
}

test("the token list passes axe with active, revoked and expired tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByText("ci")
  await expectNoAxeViolations(container)
})

test("the token list passes axe while it loads", async () => {
  stubFetch({ [LIST]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByLabelText("Loading the tokens")
  await expectNoAxeViolations(container)
})

test("the empty token list passes axe", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens: [] } } } })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByText("No API tokens yet")
  await expectNoAxeViolations(container)
})

test("the token list error passes axe", async () => {
  stubFetch({ [LIST]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } } })
  const { container } = renderWithProviders(<TokensPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the revoke dialog passes axe and gives the focus back to its button", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } }, [REVOKE_PLAN]: { body: revokePlan } })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(screen.getByRole("button", { name: "Revoke token ci" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Type tok1 to confirm")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Revoke token ci")
})

test("the keyboard reaches Create token, then each Revoke button, skipping revoked tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  expect(await tabSequence(user, 3)).toEqual(["Create token", "Revoke token ci", "Revoke token stale"])
})

test("the token list has a level-one heading and the page title API tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  renderWithProviders(<TokensPage />)
  await screen.findByText("ci")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["API tokens"])
  expect(document.title).toBe("API tokens · Darkpanel")
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w9-tmp/13 && export TMPDIR=/root/.cache/w9-tmp/13 GOTOOLCHAIN=local
timeout 600 make web-deps
timeout 300 pnpm -C web exec vitest run src/pages/login.a11y src/pages/hosts.a11y src/pages/host.a11y src/pages/audit.a11y src/pages/tokens.a11y src/pages/create-host-dialog.a11y
```

Expected: `Tests  17 failed | 31 passed (48)`, the 17 being these:

```text
login.a11y:              the page title is Sign in
hosts.a11y:              the empty host list passes axe (heading-order)
                         the host list has one level-one heading, the page title and a table with named columns
host.a11y:               the host page passes axe for a host without modules (heading-order)
                         the host page passes axe for a module that is not enabled (heading-order)
                         the host page passes axe for an unavailable module (heading-order)
                         the page title of a module page / an unavailable module / a module that is not enabled /
                           a host without modules / an unknown host
                         the page title is Host while the host list loads
audit.a11y:              the empty audit log passes axe (heading-order)
                         the audit log has a level-one heading, named columns and the page title Audit log
tokens.a11y:             the empty token list passes axe (heading-order)
                         the token list has a level-one heading and the page title API tokens
create-host-dialog.a11y: the plan, the enrollment token and the missing-token notice pass axe (label)
```

- [ ] **Step 3: Implement**

All six files import `usePageTitle` from `@/lib/page-title`. A hook runs before any early return of its component. The comment that explains the visually hidden headings is written once per file, in full sentences, and says why: darkraise-ui's `EmptyState` titles are `h3`, so the heading under the page's `h1` needs an `h2` between them.

1. `login.tsx`, `LoginPage`: call `usePageTitle("Sign in")` after the `next` constant and before the first `if (status === "loading")` return, so the title is set while the session is read, on the login form and during the redirect.
2. `hosts.tsx`: `HostsPage` calls `usePageTitle("Hosts")`. In `HostsBody`, the empty-list branch returns a fragment of a visually hidden `h2` (class `sr-only`) reading `Host list`, then the existing `EmptyState` with the title `No hosts yet`. The other states keep their markup.
3. `host.tsx`: add a module-level `hostTitle(loaded: boolean, name: string | undefined, moduleId: string | undefined): string` and call `usePageTitle` with its result in `HostPage`, directly after the `useQuery` call (`loaded` is `hosts.isSuccess`, `name` the name of the host of `hostId` when the list has it, `moduleId` the route parameter). It returns `Host` while `loaded` is false (the list is loading or failed), `Host not found` when the list has no such host, the host's name when there is no module in the route (the host index with no module enabled), and otherwise `<name> · <label>`, where the label is the `moduleRegistry` entry's `label` for `moduleId` and `moduleId` itself when no entry has that id. Fix the heading order of three states: `ModuleBody`'s `not-enabled` branch returns a fragment of a visually hidden `h2` reading `Module` and the existing `EmptyState`; the `No modules` branch of `HostPage` puts a visually hidden `h2` reading `Modules` between the header and its `EmptyState`; the `unavailable` branch gives its `AlertTitle` the prop `as="h2"` (`AlertTitle` accepts `h2` to `h6`, default `h5`).
4. `create-host-dialog.tsx`, the `Host added` dialog: associate the label with the input that shows the token or bundle, which has no accessible name today: give `ClipboardInput` the `id` `host-grant-secret` and `ClipboardLabel` the matching `htmlFor`. Nothing else in the dialog changes; the focus returns to "Add host" already (the tests pin it).
5. `audit.tsx`: `AuditPage` calls `usePageTitle("Audit log")`; the empty branch of `AuditBody` returns a visually hidden `h2` reading `Audit entries` before its `EmptyState`.
6. `tokens.tsx`: `TokensPage` calls `usePageTitle("API tokens")`; the empty branch of `TokensBody` returns a visually hidden `h2` reading `Token list` before its `EmptyState`.

Two defects next to these files are outside this task's Files, so do not edit them, and leave them to the wave-close findings (Task 24): the same unlabelled `ClipboardInput` in `web/src/pages/create-token-dialog.tsx` and `web/src/modules/tailnet/keys-page.tsx`, and the audit log's "Load older entries" button, which unmounts when the last page loads and drops the focus to the document body.

- [ ] **Step 4: Run test to verify it passes**

```bash
export TMPDIR=/root/.cache/w9-tmp/13 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/pages
timeout 1200 make lint-web test-web
```

Expected: `src/pages` passes in full (the six new files, 48 tests, and every existing page test); `make lint-web test-web` ends with `tsc --noEmit` and eslint (`--max-warnings 0`) silent and every Vitest file passing. Then `rm -rf /root/.cache/w9-tmp/13`. The task changes no Go file, `Makefile`, `package.json` or lock file, so no Go command is needed beyond what `make web-deps` and `make test-web` run.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/login.tsx web/src/pages/hosts.tsx web/src/pages/host.tsx web/src/pages/create-host-dialog.tsx web/src/pages/audit.tsx web/src/pages/tokens.tsx web/src/pages/login.a11y.test.tsx web/src/pages/hosts.a11y.test.tsx web/src/pages/host.a11y.test.tsx web/src/pages/create-host-dialog.a11y.test.tsx web/src/pages/audit.a11y.test.tsx web/src/pages/tokens.a11y.test.tsx
git commit -m "fix(web): page titles and axe fixes for core pages"
```


### Task 14: web a11y: change dialog and change pages

**Files:**
- Create: `web/src/components/change-dialog.a11y.test.tsx`
- Create: `web/src/pages/change.a11y.test.tsx`
- Create: `web/src/pages/change-actions.a11y.test.tsx`
- Modify: `web/src/components/change-dialog.tsx` (the `Countdown` and `Applied` components, `ChangeDialogBody`)
- Modify: `web/src/pages/change.tsx` (`ChangePage`)
- Modify: `web/src/pages/change-actions.tsx` (`ChangeActions`, `UndoDialog`)
- Read for fixtures: `web/src/components/change-dialog.test.tsx`, `web/src/pages/change.test.tsx`, `web/src/pages/change-actions.test.tsx`, `web/src/test/utils.tsx`

**Interfaces:**
- Consumes: Contracts "Accessibility helpers" (`expectNoAxeViolations`, `tabSequence`, `focusedName` from `@/test/a11y`) and "Page title" (`usePageTitle` from `@/lib/page-title`), both produced by Task 2.
- Produces: `ChangeDialog` keeps its props and `ChangeTarget`; every dialog that uses it now holds one always-mounted `role="status"` region named "Change progress" (polite, visually hidden) that other tests may find with `getByRole("status", { name: "Change progress" })`. `ChangePage` sets the title `Change <short id>`, where the short id is the last eight characters of the change id (a ULID's first ten characters are its timestamp, so the tail tells nearby changes apart) or the whole id when it has eight or fewer. `ChangeActions` moves focus to its notice when one appears.

**Items:** 41

**Depends on:** Task 2
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

What this audit found, by reading the three files and running the dialog in jsdom: axe reports nothing on the planned, typed-confirmation, overridden-guard, applying, pending (countdown with Confirm and Revert) and failed states; the darkraise-ui focus trap already keeps Tab inside the dialog, Escape closes it and focus returns to the opener. Five things are wrong, and the tests below fail on exactly these: (1) the countdown has `role="timer"` with `aria-live="off"`, so a screen-reader user is never told that the change will revert, and the outcome of an apply ("completed", "reverted") is shown only as a badge nobody announces; (2) the change page sets no document title; (3) after Undo or Release succeeds the button that opened the dialog is gone from the page, the focus trap can restore focus only to an element still in the document, and focus falls to `body`; (4) the same happens when a repair applied through `ChangeDialog` closes; (5) the "Unavailable" and "Availability unknown" alerts render darkraise-ui's `AlertTitle`, an `h5`, straight after the dialog's `h2`, which axe reports as `heading-order` (the Docker, NPM and Cloudflare change wrappers of Task 20 inherit it).

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/change-dialog.a11y.test.tsx`:

````tsx
import { useState } from "react"
import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import type { Change, Guard, Plan, Step } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeDialog, type ChangeDialogProps } from "./change-dialog"

const PLAN = "POST /api/v1/hosts/h1/actions/cda.rule.set/v1/plan"
const APPLY = "POST /api/v1/hosts/h1/actions/cda.rule.set/v1/apply"
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/cda/availability"

const cdaAvailable = { module: "cda", available: true, reason: null, actions: [{ name: "cda.rule.set", available: true, reason: null }] }
const cdaTarget = { name: "cda.rule.set", version: 1, hostId: "h1", module: "cda", revertible: true }

function cdaPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action: "cda.rule.set", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "allow 443/tcp" }, { op: " ", text: "default deny" }], guards: [],
    expiresAt: "2026-10-23T09:00:00Z", confirmation: null, ...over,
  }
}

function cdaGuard(over: Partial<Guard> = {}): Guard {
  return { id: "ssh-session", ok: false, message: "this change blocks your SSH session", overridable: true, overridden: false, ...over }
}

function cdaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cda.rule.set", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

// CdaHarness opens the dialog from a button, as every caller does, so the
// focus the dialog must give back has an element to return to.
function CdaHarness(props: Partial<ChangeDialogProps>) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open the change
      </button>
      <ChangeDialog open={open} onOpenChange={setOpen} title="Allow HTTPS" target={cdaTarget} input={{ port: 443 }} {...props} />
    </>
  )
}

async function cdaOpen(routes: Record<string, StubHandler>, props: Partial<ChangeDialogProps> = {}, setup: Parameters<typeof userEvent.setup>[0] = {}) {
  const calls = stubFetch({ [AVAILABILITY]: { body: cdaAvailable }, ...routes })
  const user = userEvent.setup(setup)
  renderWithProviders(<CdaHarness {...props} />)
  await user.click(screen.getByRole("button", { name: "Open the change" }))
  const dialog = await screen.findByRole("dialog", { name: "Allow HTTPS" })
  return { calls, user, dialog }
}

test("the unavailable and availability-unknown alerts keep the heading order", async () => {
  const off = await cdaOpen({
    [AVAILABILITY]: { body: { ...cdaAvailable, available: false, reason: "UFW is not installed." } },
    [PLAN]: { body: cdaPlan() },
  })
  await within(off.dialog).findByText("UFW is not installed.")
  await expectNoAxeViolations(off.dialog)
})

afterEach(() => {
  vi.useRealTimers()
})

test("a planned change with a failed guard and a typed confirmation has no violations", async () => {
  const { dialog } = await cdaOpen({ [PLAN]: { body: cdaPlan({ guards: [cdaGuard()], confirmation: "web-1" }) } })
  await within(dialog).findByRole("checkbox", { name: "Override ssh-session" })
  await expectNoAxeViolations(dialog)
})

test("a guard that was overridden has no violations", async () => {
  let plans = 0
  const { dialog, user } = await cdaOpen({
    [PLAN]: () => {
      plans++
      return { body: cdaPlan({ guards: [cdaGuard({ ok: plans > 1, overridden: plans > 1 })] }) }
    },
  })
  await user.click(await within(dialog).findByRole("checkbox", { name: "Override ssh-session" }))
  await within(dialog).findByText("overridden")
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await expectNoAxeViolations(dialog)
})

test("an applying composite with its steps has no violations", async () => {
  const steps: Step[] = [
    { id: "network", ordinal: 1, host: "h1", action: "cda.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "ports", ordinal: 2, host: "h1", action: "cda.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const applying = cdaChange({ class: "composite", state: "applying" })
  const { dialog, user } = await cdaOpen({
    [PLAN]: { body: cdaPlan() },
    [APPLY]: { status: 202, body: applying },
    "GET /api/v1/changes/c1": { body: applying },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
  })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("list", { name: "Steps" })
  await expectNoAxeViolations(dialog)
})

test("a pending change with its countdown, Confirm and Revert has no violations", async () => {
  const pending = cdaChange({ state: "pending", revertDeadline: "2099-01-01T00:00:00Z" })
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: pending }, "GET /api/v1/changes/c1": { body: pending } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("timer")
  expect(within(dialog).getByRole("button", { name: "Confirm" })).toBeTruthy()
  expect(within(dialog).getByRole("button", { name: "Revert" })).toBeTruthy()
  await expectNoAxeViolations(dialog)
})

test("a failed change has no violations", async () => {
  const failed = cdaChange({ state: "failed", error: { code: "internal", message: "ufw exited with status 1" } })
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: failed }, "GET /api/v1/changes/c1": { body: failed } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("ufw exited with status 1")
  await expectNoAxeViolations(dialog)
})

test("Tab and Shift+Tab stay inside the dialog and wrap", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan({ guards: [cdaGuard()], confirmation: "web-1" }) } })
  await within(dialog).findByLabelText("Type web-1 to confirm")
  const forward: string[] = []
  for (let i = 0; i < 8; i++) {
    forward.push(...(await tabSequence(user, 1)))
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  expect(forward).toContain("Override ssh-session")
  expect(forward).toContain("Type web-1 to confirm")
  expect(new Set(forward).size).toBeLessThan(forward.length)
  const backward: string[] = []
  for (let i = 0; i < 8; i++) {
    backward.push(...(await tabSequence(user, 1, { backwards: true })))
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  expect(new Set(backward).size).toBeLessThan(backward.length)
})

test("Escape closes the dialog and returns focus to the button that opened it", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() } })
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("the dialog keeps focus after Apply and Close returns it to the opener", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: cdaChange() }, "GET /api/v1/changes/c1": { body: cdaChange() } })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  apply.focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  expect(dialog.contains(document.activeElement)).toBe(true)
  // The footer's Close comes before the corner button the dialog adds.
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  await user.click(close)
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("the typed-confirmation and revert-timeout fields have labels and the timeout its help", async () => {
  const { dialog } = await cdaOpen({ [PLAN]: { body: cdaPlan({ confirmation: "web-1" }) } })
  const phrase = await within(dialog).findByLabelText("Type web-1 to confirm")
  expect(phrase.tagName).toBe("INPUT")
  expect(phrase.getAttribute("autocomplete")).toBe("off")
  const timeout = within(dialog).getByRole("spinbutton", { name: "Revert after (seconds)" })
  const help = document.getElementById(timeout.getAttribute("aria-describedby") ?? "")
  expect(help?.textContent).toBe("Unless you confirm the change within this time, it is reverted on the host.")
  expect(timeout.getAttribute("aria-invalid")).toBe("false")
})

test("the countdown announces itself politely at fixed points, not every second", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date("2026-10-23T08:00:00Z"))
  const pending = cdaChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })
  const { dialog, user } = await cdaOpen(
    { [PLAN]: { body: cdaPlan() }, [APPLY]: { body: pending }, "GET /api/v1/changes/c1": { body: pending } },
    {},
    { advanceTimers: vi.advanceTimersByTime },
  )
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  const timer = await within(dialog).findByRole("timer")
  expect(timer.closest('[aria-live="polite"], [aria-live="assertive"], [role="alert"], [role="status"]')).toBeNull()
  const progress = within(dialog).getByRole("status", { name: "Change progress" })
  expect(progress.getAttribute("aria-live")).toBe("polite")
  expect(progress.textContent).toBe("Reverts automatically in 60 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(timer.textContent).toBe("Reverts automatically in 55 s unless confirmed.")
  expect(progress.textContent).toBe("Reverts automatically in 60 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(25000)
  })
  expect(progress.textContent).toBe("Reverts automatically in 30 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000)
  })
  expect(progress.textContent).toBe("Reverts automatically in 10 seconds unless confirmed.")
})

test("the outcome of an apply is announced politely", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: cdaChange() }, "GET /api/v1/changes/c1": { body: cdaChange() } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  const progress = await within(dialog).findByRole("status", { name: "Change progress" })
  expect(progress.getAttribute("aria-live")).toBe("polite")
  expect(progress.textContent).toBe("Change completed.")
})
````

Create `web/src/pages/change.a11y.test.tsx`:

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Effect, ObserveResult, Step } from "@/api/types"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangePage } from "./change"

function cpaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "needs_repair", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null,
    error: { code: "core.needs_repair", message: "the provider's answer was lost", cause: "cloud.uncertain_outcome" },
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cpaEffect(over: Partial<Effect> = {}): Effect {
  return {
    changeId: "c1", seq: 1, kind: "create", resource: "cdx.key:?darkpanel-c1", local: false, inverseLocal: false,
    commitBy: "2026-10-23T08:10:00Z", state: "unresolved", reason: "uncertain", before: { state: null }, after: { state: { tags: ["a"] } },
    post: null, undoTo: null, undoResource: "", current: null, resolution: "", resolvedBy: "", resolvedAt: null, ...over,
  }
}

const cpaTwoMatches: ObserveResult = {
  effect: cpaEffect(),
  observation: {
    current: { state: { tags: ["a"] } }, resource: "", matches: 2, exclusive: false,
    candidates: [{ resource: "cdx.key:k1", state: { state: {} } }, { resource: "cdx.key:k2", state: { state: {} } }],
  },
  allowed: null,
}

const cpaSteps: Step[] = [
  { id: "ports", ordinal: 2, host: "h1", action: "cdx.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
  { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
]

function cpaRender(routes: Record<string, StubHandler>, path = "/changes/c1") {
  stubFetch(routes)
  return renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path },
  )
}

test("a composite that needs repair, with its steps, effects and actions, has no violations", async () => {
  const { container } = cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange({ class: "composite" }) },
    "GET /api/v1/changes/c1/steps": { body: { steps: cpaSteps } },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpaEffect(), cpaEffect({ seq: 2, state: "accepted", resolution: "accept", resolvedBy: "user:u1" })] } },
  })
  await screen.findByRole("region", { name: "Steps" })
  await screen.findByRole("region", { name: "Effects" })
  await screen.findByRole("button", { name: "Preview effect 1" })
  await screen.findByRole("group", { name: "Decide a step" })
  await expectNoAxeViolations(container)
})

test("a completed change with no effects has no violations", async () => {
  const { container } = cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange({ class: "revertible", state: "confirmed", error: null }) },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  await screen.findByText("This change recorded no effects.")
  await expectNoAxeViolations(container)
})

test("the resolve dialog has no violations, and Escape returns focus to the effect's button", async () => {
  cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpaEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpaTwoMatches },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  await within(dialog).findByRole("radio", { name: "cdx.key:k2" })
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Preview effect 1")
})

test("the page title names the change by the last eight characters of its id and is restored after", async () => {
  const id = "01JZ8Q4M6N3V7B2D5F9H1K0XYZ"
  document.title = "before"
  const { unmount } = cpaRender(
    {
      [`GET /api/v1/changes/${id}`]: { body: cpaChange({ id, error: null, state: "completed" }) },
      [`GET /api/v1/changes/${id}/effects`]: { body: { effects: [] } },
    },
    `/changes/${id}`,
  )
  expect(document.title).toBe("Change 9H1K0XYZ · Darkpanel")
  await screen.findByText("This change recorded no effects.")
  expect(document.title).toBe("Change 9H1K0XYZ · Darkpanel")
  unmount()
  expect(document.title).toBe("before")
})

test("an id of eight characters or fewer is the whole title, also while the change is unknown", async () => {
  cpaRender(
    { "GET /api/v1/changes/zz": { status: 404, body: { type: "about:blank", title: "Change not found", status: 404, detail: "no change zz", code: "change.not_found" } } },
    "/changes/zz",
  )
  expect(document.title).toBe("Change zz · Darkpanel")
  await screen.findByRole("alert")
  expect(document.title).toBe("Change zz · Darkpanel")
})
````

Create `web/src/pages/change-actions.a11y.test.tsx`:

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Plan } from "@/api/types"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeActions } from "./change-actions"
import { ChangePage } from "./change"

function caaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

const caaPlan: Plan = {
  planId: "rp1", hostId: "h1", action: "cdx.rule.fix", version: 1, inputHash: "h", diff: [{ op: "+", text: "restore rule 443" }],
  guards: [], expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function caaRenderActions(change: Change, routes: Record<string, StubHandler> = {}) {
  stubFetch(routes)
  return renderWithProviders(<ChangeActions change={change} />)
}

// caaRenderPage renders the change page, whose answer follows the change
// the test's handlers set, as the page does when an action moves it on.
function caaRenderPage(read: () => Change, routes: Record<string, StubHandler>) {
  stubFetch({ "GET /api/v1/changes/c1": () => ({ body: read() }), "GET /api/v1/changes/c1/effects": { body: { effects: [] } }, ...routes })
  return renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path: "/changes/c1" },
  )
}

test("the undo dialog has no violations and Escape returns focus to Undo", async () => {
  caaRenderActions(caaChange())
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  const dialog = await screen.findByRole("dialog", { name: "Undo this change?" })
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Undo")
})

test("the release dialog has no violations, with its reason field named and its refusal announced", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }))
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = await screen.findByRole("dialog", { name: "Release this change?" })
  expect(within(dialog).getByRole("textbox", { name: "Reason" }).hasAttribute("required")).toBe(true)
  await user.type(within(dialog).getByRole("textbox", { name: "Reason" }), "   ")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("Enter why the change is given up.")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Release")
})

test("the repair dialog has no violations", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/repair": { body: { plan: caaPlan } } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
})

test("the actions of a change that needs repair, with the step decision, have no violations", async () => {
  const steps = [
    { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const { container } = caaRenderActions(caaChange({ class: "composite", state: "needs_repair" }), { "GET /api/v1/changes/c1/steps": { body: { steps } } })
  await screen.findByRole("group", { name: "Decide a step" })
  await expectNoAxeViolations(container)
})

test("an undo that moves the change on leaves focus on its notice, not on a button that is gone", async () => {
  let current = caaChange()
  caaRenderPage(() => current, {
    "POST /api/v1/changes/c1/undo": () => {
      current = caaChange({ state: "undoing", owner: "undo" })
      return { status: 202, body: current }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Undo" }))
  const dialog = await screen.findByRole("dialog", { name: "Undo this change?" })
  await user.click(within(dialog).getByRole("button", { name: "Undo" }))
  const notice = await screen.findByText("Undo started. The change is now undoing.")
  expect(notice.getAttribute("role")).toBe("status")
  await waitFor(() => expect(screen.queryByRole("button", { name: "Undo" })).toBeNull())
  expect(document.activeElement).toBe(notice)
})

test("a release by keyboard leaves focus on its notice", async () => {
  let current = caaChange({ state: "needs_repair" })
  caaRenderPage(() => current, {
    "POST /api/v1/changes/c1/release": () => {
      current = caaChange({ state: "abandoned", updatedAt: "2026-10-23T08:10:00Z" })
      return { body: { change: current, abandoned: [{ seq: 1 }] } }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Release" }))
  const dialog = await screen.findByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByRole("textbox", { name: "Reason" }), "stuck")
  await user.tab()
  expect(focusedName()).toBe("Release")
  await user.keyboard("{Enter}")
  const notice = await screen.findByText("Released. 1 effect abandoned.")
  expect(notice.getAttribute("role")).toBe("status")
  await waitFor(() => expect(screen.queryByRole("button", { name: "Release" })).toBeNull())
  expect(document.activeElement).toBe(notice)
})

test("closing a repair that was applied leaves focus on a notice of it", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/repair": { body: { plan: caaPlan } },
    "POST /api/v1/hosts/h1/actions/cdx.rule.fix/v1/apply": { body: caaChange({ id: "r1", action: "cdx.rule.fix" }) },
    "GET /api/v1/changes/r1": { body: caaChange({ id: "r1", action: "cdx.rule.fix" }) },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  const notice = await screen.findByText("The repair was applied.")
  expect(notice.getAttribute("role")).toBe("status")
  expect(document.activeElement).toBe(notice)
})
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w9-tmp/14 && export TMPDIR=/root/.cache/w9-tmp/14 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/components/change-dialog.a11y.test.tsx src/pages/change.a11y.test.tsx src/pages/change-actions.a11y.test.tsx`

Expected: exactly eight tests FAIL and the other sixteen pass (the axe, Tab, Escape, focus-return and label tests are guards that pass already; a guard that fails means Task 2's helper is wrong, not this task's code). Quote the output. The eight, with the first line of each failure:
- `the unavailable and availability-unknown alerts keep the heading order`: `axe found 1 violation(s): heading-order`.
- `the countdown announces itself politely at fixed points, not every second` and `the outcome of an apply is announced politely`: `Unable to find an accessible element with the role "status" and name "Change progress"`.
- `the page title names the change by the last eight characters of its id and is restored after` and `an id of eight characters or fewer is the whole title, also while the change is unknown`: `expected 'before' to be 'Change 9H1K0XYZ · Darkpanel'` (and the same for `Change zz · Darkpanel`).
- `an undo that moves the change on leaves focus on its notice, not on a button that is gone` and `closing a repair that was applied leaves focus on a notice of it`: `Unable to find an element with the text: Undo started. The change is now undoing.` (and `The repair was applied.`).
- `a release by keyboard leaves focus on its notice`: `expected <body …> to be <p role="status" …>`.

- [ ] **Step 3: Implement**

In `web/src/components/change-dialog.tsx`:
1. Replace `Countdown`'s own interval with a hook `useSecondsLeft(deadline: string | null): number | null` that keeps a `now` state, starts a one-second `setInterval` only while `deadline` is not null (clearing it on cleanup or when the deadline goes), and returns `secondsLeft(deadline, now)` or null. `Countdown` becomes a presentational component taking `left: number` and still renders `<p role="timer" aria-live="off">` with the text `Reverts automatically in {left} s unless confirmed.`: `change-dialog.test.tsx` asserts that text and role, and the timer must never become a live region itself, because a region that changed every second would be read out every second.
2. In `Applied`, call `useSecondsLeft` with the change's `revertDeadline` only while the change is `pending` (`counting`), and render `Countdown` only when `counting` and the hook returned a number. Add the announcement text `progress`: while `counting` it is `Reverts automatically in <n> seconds unless confirmed.` (`1 second` when n is 1), otherwise `Change <state>.` (for example `Change completed.`, `Change reverted.`). `<n>` is held in a `spoken` state initialised from the seconds left; during render, when a pure helper `shouldAnnounce(spoken, left)` is true, call `setSpoken(left)` (the derived-state pattern, no ref written during render). `shouldAnnounce` is true when `left` has dropped to 30 or below while `spoken` was above 30, or to 10 or below while `spoken` was above 10 (a module constant lists 30 and 10). So the text changes at the start, at 30 s and at 10 s, and when the state changes, never once a second. `Applied` takes a new prop `onProgress(text: string)` and calls it from an effect whenever `progress` changes.
3. In `ChangeDialogBody`, keep a `progress` state (initially the empty string), pass `setProgress` as `onProgress`, and always render, before the error alert and the footer, `<p role="status" aria-label="Change progress" aria-live="polite" className="sr-only">` holding `progress`. It must be mounted before the first announcement (a live region inserted together with its text is not reliably announced), which is why it lives in the body and not in `Applied`.
4. In `ChangeDialogBody`, the two availability alerts (`Unavailable`, `Availability unknown`) must not open a heading below the dialog's `h2` at a skipped level: render each title as `<AlertTitle as="h3">` (darkraise-ui 6.9.6 types the prop as `as?: "h2" | "h3" | "h4" | "h5" | "h6"`, `web/node_modules/darkraise-ui/dist/components/alert/Alert.d.ts`), keeping the visible text and the alert's `role`. No other `AlertTitle` in this file sits inside the dialog.

In `web/src/pages/change.tsx`: import `usePageTitle` from `@/lib/page-title` and call ``usePageTitle(`Change ${changeId.slice(-8)}`)`` in `ChangePage` straight after `useParams()`, before the loading and error returns, so the title is right while the change loads and when it is unknown. `slice(-8)` returns the whole id when it is shorter.

In `web/src/pages/change-actions.tsx`:
1. `ChangeActions` holds `noticeRef` (an `HTMLParagraphElement` ref) and an effect on `notice` that calls `noticeRef.current?.focus()` when `notice !== null`. The notice paragraph becomes `<p ref={noticeRef} role="status" tabIndex={-1} className="text-sm">` and keeps no outline override. A short comment states why: the button that opened the dialog may be gone by then and the trap cannot restore focus to it. The effect runs after the closing dialog's own focus restore (cleanups run before effects in one commit), so the notice keeps focus.
2. `UndoDialog` takes `onDone(message: string)` like `ReleaseDialog` and, after a successful undo and before `onClose()`, calls ``onDone(`Undo started. The change is now ${next.state}.`)``; `ChangeActions` passes `setNotice`.
3. For the repair dialog, add a `repaired` boolean state: set it false at the start of `repair()`, set it true in the `ChangeDialog`'s `onApplied` (which still invalidates the change query), and in its `onOpenChange(false)` clear `repairPlan` and, when `repaired` is true, `setNotice("The repair was applied.")`.
Release already sets a notice; it now takes focus through item 1.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w9-tmp/14 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/components src/pages` then `timeout 1200 make lint-web test-web`
Expected: PASS, with `tsc --noEmit` and `eslint --max-warnings 0` clean. Run the countdown test (`the countdown announces itself politely…`) five times in a row to see it is stable (it uses fake timers with `shouldAdvanceTime`, like the existing countdown test). Quote the output. Then `rm -rf /root/.cache/w9-tmp/14`.

- [ ] **Step 5: Commit**

````bash
git add web/src/components/change-dialog.a11y.test.tsx web/src/pages/change.a11y.test.tsx web/src/pages/change-actions.a11y.test.tsx web/src/components/change-dialog.tsx web/src/pages/change.tsx web/src/pages/change-actions.tsx
git commit -m "fix(web): announce and focus the change flow"
````

### Task 15: web a11y: firewall change flow

**Files:**
- Create: `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `rule-form.a11y.test.tsx`, `rules-table.a11y.test.tsx`, `firewall-change.a11y.test.tsx`, `state-controls.a11y.test.tsx`, `snapshots-panel.a11y.test.tsx`, `status-panel.a11y.test.tsx`, `exposure-page.a11y.test.tsx` (all in `web/src/modules/firewall/`)
- Modify: `web/src/modules/firewall/firewall-change.tsx`, `rules-table.tsx`, `status-panel.tsx`, `firewall-page.tsx`, `state-controls.tsx`
- Read, and change only if one of the new tests is red for it: `rule-form.tsx`, `snapshots-panel.tsx`, `exposure-page.tsx`, `routes.tsx`
- Not edited: `container-actions.tsx` (Task 20), `docker-panel.tsx`, `docker-sources.tsx` and `snapshot-compare.tsx` (they also use `FirewallChange`, which keeps working without the new optional prop)
- Read for fixtures: `web/src/modules/firewall/fixtures.ts`, the existing `*.test.tsx` beside each component, `web/src/test/utils.tsx`

**Interfaces:**
- Consumes: Contracts "Accessibility helpers" (`expectNoAxeViolations`, `tabSequence`, `focusedName`), Task 2.
- Produces: `FirewallChangeProps.fallbackFocus?: RefObject<HTMLElement | null>` and `StatusBanners`' optional prop of the same name; two focusable landmarks, the region "Rules" (in `RulesTable`) and the region "Firewall state" (in `FirewallPage`), which take focus when a change dialog closes and the control that opened it is gone. The firewall page no longer remounts `StateControls` when the status changes.

**Items:** 41

**Depends on:** Task 2
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

What this audit found: the chained dialogs already hand focus back correctly where the opener survives (darkraise-ui keeps the trap disabled while a dialog is closed and restores focus to the element that was focused when it opened, if that is still in the document): "Add rule" to the form to the change dialog and back to "Add rule", the manual-rule questions in `RulesTable`, `FirewallChange` and `SnapshotsPanel`, Escape in every dialog, and axe on every component. Five things are wrong, and the tests below fail on exactly these: (1) `FirewallPage` gives `StateControls` a `key` made of the configured state and `active`, and `FirewallChange` refreshes the host's resources as soon as an apply succeeds, so the status read that follows a state change remounts the card and destroys the open change dialog with its countdown, Confirm and Revert: the operator who just enabled or disabled UFW loses the controls that keep a lockout from becoming permanent, and focus falls to `body`; (2) the same remount replaces the Enable/Disable button, so even a closed dialog would have no opener to return to; (3) after a rule is deleted its Delete button leaves the table, so closing the change dialog drops focus to `body`; (4) after a repair succeeds the "Out of sync" banner and its Repair button are gone, with the same result; (5) `StateControls` has to start its drafts again from the host's settings by itself once the page stops keying it.

- [ ] **Step 1: Write the failing tests**

Create `web/src/modules/firewall/firewall-page.a11y.test.tsx` (the page-level keyboard flows, which are the ones that fail):

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwDocker, fwRule, fwSnapshot, fwStatus } from "./fixtures"
import FirewallRoutes from "./routes"
import type { FirewallStatus } from "./types"

const FPA_STATUS = "GET /api/v1/hosts/h1/resources/firewall/status"

function fpaChange(action: string, over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action, version: 1, class: "revertible", state: "pending", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: "2099-01-01T00:00:00Z", output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

// fpaRender serves a firewall whose status follows the status the test holds,
// so a change applied through a stub moves what the next read answers.
function fpaRender(read: () => FirewallStatus, routes: Record<string, StubHandler> = {}) {
  stubFetch({
    [FPA_STATUS]: () => ({ body: read() }),
    "GET /api/v1/hosts/h1/resources/firewall/snapshots": { body: { snapshots: [fwSnapshot()] } },
    "GET /api/v1/hosts/h1/resources/firewall/docker": { body: fwDocker({ available: false, reason: "the Docker socket is not present", enabled: false, health: "off" }) },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    ...routes,
  })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/*" element={<FirewallRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/firewall" },
  )
}

function fpaPlan(action: string): StubHandler {
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
}

// fpaTabTo presses Tab until the focused element has the given name.
async function fpaTabTo(user: UserEvent, name: string, limit = 30): Promise<void> {
  for (let i = 0; i < limit; i++) {
    const [focused] = await tabSequence(user, 1)
    if (focused === name) return
  }
  throw new Error(`Tab never reached ${name}; focus is on ${focusedName()}`)
}

test("the firewall page has no violations with its banners, rules and Docker panel", async () => {
  const { container } = fpaRender(() =>
    fwStatus({ inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] as FirewallStatus["outOfSync"] }),
  )
  await screen.findByRole("table", { name: "Firewall rules" })
  await screen.findByRole("button", { name: "Repair" })
  await expectNoAxeViolations(container)
})

test("a state change applied from the page keeps its countdown, and Confirm and Close work by keyboard", async () => {
  let active = false
  const status = () => fwStatus({ active, configured: { enabled: active, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } } })
  const pending = fpaChange("firewall.state.enable")
  fpaRender(status, {
    "POST /api/v1/hosts/h1/actions/firewall.state.enable/v1/plan": fpaPlan("firewall.state.enable"),
    "POST /api/v1/hosts/h1/actions/firewall.state.enable/v1/apply": () => {
      active = true
      return { body: pending }
    },
    "GET /api/v1/changes/c1": () => ({ body: active ? pending : fpaChange("firewall.state.enable", { state: "confirmed", revertDeadline: null }) }),
    "POST /api/v1/changes/c1/confirm": () => ({ body: fpaChange("firewall.state.enable", { state: "confirmed", revertDeadline: null }) }),
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Enable")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Enable the firewall" })
  await within(dialog).findByLabelText("Planned changes")
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByRole("timer")
  // The status read that follows the apply says the firewall is active.
  await screen.findByText("UFW is active.")
  expect(screen.getByRole("dialog", { name: "Enable the firewall" })).toBe(dialog)
  expect(within(dialog).getByRole("timer")).toBeTruthy()
  await fpaTabTo(user, "Confirm")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("confirmed")
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Disable")
})

test("a state change can be reverted by keyboard from the dialog that applied it", async () => {
  let active = true
  const status = () => fwStatus({ active, configured: { enabled: active, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } } })
  const pending = fpaChange("firewall.state.disable")
  const reverted = fpaChange("firewall.state.disable", { state: "reverted", revertDeadline: null })
  fpaRender(status, {
    "POST /api/v1/hosts/h1/actions/firewall.state.disable/v1/plan": fpaPlan("firewall.state.disable"),
    "POST /api/v1/hosts/h1/actions/firewall.state.disable/v1/apply": () => {
      active = false
      return { body: pending }
    },
    "GET /api/v1/changes/c1": () => ({ body: active ? reverted : pending }),
    "POST /api/v1/changes/c1/revert": () => {
      active = true
      return { body: reverted }
    },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Disable")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Disable the firewall" })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByRole("timer")
  await screen.findByText("UFW is inactive.")
  await fpaTabTo(user, "Revert")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("reverted")
  expect(within(dialog).queryByRole("timer")).toBeNull()
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await screen.findByText("UFW is active.")
  expect(focusedName()).toBe("Disable")
})

test("closing the change dialog of a deleted rule leaves focus on the rules, not on the page", async () => {
  let rules = [fwRule({ id: "01JD0000000000000000000001" }), fwRule({ id: "01JD0000000000000000000002", toPort: "8080", comment: "alt" })]
  const applied = fpaChange("firewall.rule.delete", { state: "completed", class: "revertible", revertDeadline: null })
  fpaRender(() => fwStatus({ rules }), {
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": fpaPlan("firewall.rule.delete"),
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/apply": () => {
      rules = rules.slice(1)
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Delete rule 1")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: /^Delete rule:/ })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(() => expect(within(screen.getByRole("table", { name: "Firewall rules" })).getAllByRole("row")).toHaveLength(2))
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Rules" }))
})

test("closing the change dialog of a repair leaves focus on the status, not on the page", async () => {
  let inSync = false
  const applied = fpaChange("firewall.state.repair", { state: "completed", revertDeadline: null })
  fpaRender(() => fwStatus({ inSync, outOfSync: inSync ? [] : ([{ family: "v4", table: "filter", chain: "ufw-user-input" }] as FirewallStatus["outOfSync"]) }), {
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/plan": fpaPlan("firewall.state.repair"),
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/apply": () => {
      inSync = true
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Repair")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Repair the firewall" })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(() => expect(screen.queryByText("Out of sync")).toBeNull())
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Firewall state" }))
})
````

Create `web/src/modules/firewall/state-controls.a11y.test.tsx`:

````tsx
import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StateControls } from "./state-controls"

function scaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.defaults.set/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.defaults.set", version: 1, inputHash: "h", diff: [{ op: "~", text: "incoming reject" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<StateControls hostId="h1" status={fwStatus()} />)
}

test("the default policy is set by keyboard, and Escape returns focus to the button that planned it", async () => {
  const { container } = scaRender()
  await expectNoAxeViolations(container)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Default policy" })
  within(form).getByRole("radio", { name: "deny", checked: true }).focus()
  await user.keyboard("{ArrowDown}")
  expect(within(form).getByRole("radio", { name: "reject", checked: true })).toBeTruthy()
  const submit = within(form).getByRole("button", { name: "Set default policy" })
  expect(submit.hasAttribute("disabled")).toBe(false)
  submit.focus()
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Set the default incoming policy to reject" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Set default policy")
})

function ScaHarness() {
  const [level, setLevel] = useState("low")
  return (
    <>
      <button type="button" onClick={() => setLevel("medium")}>
        The host changed
      </button>
      <StateControls hostId="h1" status={fwStatus({ configured: { ...fwStatus().configured, logLevel: level } })} />
    </>
  )
}

test("the drafts start again from the host's settings when those change, in the same card", async () => {
  stubFetch({})
  renderWithProviders(<ScaHarness />)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Logging" })
  await user.click(within(form).getByLabelText("high"))
  expect(within(form).getByRole("radio", { name: "high", checked: true })).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "The host changed" }))
  expect(within(screen.getByRole("form", { name: "Logging" })).getByRole("radio", { name: "medium", checked: true })).toBeTruthy()
  expect(within(screen.getByRole("form", { name: "Logging" })).getByRole("button", { name: "Set logging" }).hasAttribute("disabled")).toBe(true)
})
````

Create `web/src/modules/firewall/rule-form.a11y.test.tsx` (the chained "Add rule" case: the form closes, the change dialog opens, and after it closes focus is on "Add rule"):

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { AddRule } from "./rule-form"

const RFA_PLAN = "POST /api/v1/hosts/h1/actions/firewall.rule.add/v1/plan"
const RFA_APPLY = "POST /api/v1/hosts/h1/actions/firewall.rule.add/v1/apply"

const rfaPending = {
  id: "c1", hostId: "h1", action: "firewall.rule.add", version: 1, class: "revertible", state: "pending", actor: "user:u1",
  createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: "2099-01-01T00:00:00Z", output: null, error: null,
  compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function rfaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.add", available: true, reason: null }] },
    },
    [RFA_PLAN]: {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.add", version: 1, inputHash: "h", diff: [{ op: "+", text: "allow in tcp to any port 443" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    [RFA_APPLY]: { body: rfaPending },
    "GET /api/v1/changes/c1": { body: rfaPending },
    "POST /api/v1/changes/c1/confirm": { body: { ...rfaPending, state: "confirmed", revertDeadline: null } },
  })
  return renderWithProviders(<AddRule hostId="h1" />)
}

// rfaPlanned opens the form with the keyboard, fills the port and submits it
// with Enter, which closes the form and opens the change dialog.
async function rfaPlanned() {
  const user = userEvent.setup()
  const [first] = await tabSequence(user, 1)
  expect(first).toBe("Add rule")
  await user.keyboard("{Enter}")
  const form = await screen.findByRole("form", { name: "Rule" })
  await user.type(within(form).getByLabelText("To port"), "443{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Add a rule" })
  await within(dialog).findByLabelText("Planned changes")
  expect(screen.queryByRole("form", { name: "Rule" })).toBeNull()
  return { user, dialog }
}

test("the rule form has no violations and keeps Tab inside it", async () => {
  rfaRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Add rule" }))
  const dialog = await screen.findByRole("dialog", { name: "Add a rule" })
  await expectNoAxeViolations(dialog)
  for (let i = 0; i < 40; i++) {
    await tabSequence(user, 1)
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
})

test("Escape in the rule form returns focus to Add rule", async () => {
  rfaRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Add rule" }))
  await screen.findByRole("form", { name: "Rule" })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})

test("the change dialog that replaces the form has no violations", async () => {
  rfaRender()
  const { dialog } = await rfaPlanned()
  await expectNoAxeViolations(dialog)
})

test("closing the change dialog of an added rule with Escape returns focus to Add rule", async () => {
  rfaRender()
  const { user } = await rfaPlanned()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})

test("closing the change dialog after Apply and Confirm returns focus to Add rule", async () => {
  rfaRender()
  const { user, dialog } = await rfaPlanned()
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  apply.focus()
  await user.keyboard("{Enter}")
  const confirm = await within(dialog).findByRole("button", { name: "Confirm" })
  confirm.focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("confirmed")
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})
````

Create `web/src/modules/firewall/rules-table.a11y.test.tsx`:

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { RulesTable } from "./rules-table"

async function rtaTabTo(user: UserEvent, name: string, limit = 30): Promise<void> {
  for (let i = 0; i < limit; i++) {
    const [focused] = await tabSequence(user, 1)
    if (focused === name) return
  }
  throw new Error(`Tab never reached ${name}; focus is on ${focusedName()}`)
}

function rtaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<RulesTable hostId="h1" rules={fwStatus().rules} />)
}

test("the rules table with an origin on every row and a read-only row has no violations", async () => {
  const { container } = rtaRender()
  await screen.findByRole("table", { name: "Firewall rules" })
  await expectNoAxeViolations(container)
})

test("every Delete button has a name that says which rule it deletes", async () => {
  rtaRender()
  const names = (await screen.findAllByRole("button", { name: /^Delete rule/ })).map((b) => b.getAttribute("aria-label"))
  expect(names).toEqual(["Delete rule 1", "Delete rule 2"])
})

test("the manual-rule question has no violations and Escape returns focus to the rule's Delete button", async () => {
  rtaRender()
  const user = userEvent.setup()
  await rtaTabTo(user, "Delete rule 2")
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Delete a manual rule?" })
  await expectNoAxeViolations(ask)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Delete rule 2")
})

test("the change dialog that follows the manual-rule question returns focus to the rule's Delete button", async () => {
  rtaRender()
  const user = userEvent.setup()
  await rtaTabTo(user, "Delete rule 2")
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Delete a manual rule?" })
  await rtaTabTo(user, "Delete it anyway")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: /^Delete rule:/ })
  await within(dialog).findByLabelText("Planned changes")
  expect(ask.isConnected).toBe(false)
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Delete rule 2")
})
````

Create `web/src/modules/firewall/firewall-change.a11y.test.tsx`:

````tsx
import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { FirewallChange } from "./firewall-change"

const FCA_DETAIL = 'The rule "allow in tcp from any to any port 22" was added by hand; pass manual to delete it.'

function fcaPlans(call: FetchCall): StubResponse {
  const input = (call.body as { input: { manual?: boolean } }).input
  if (input.manual === true) {
    return {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-28T09:00:00Z", confirmation: null },
    }
  }
  return { status: 409, body: { type: "about:blank", title: "Manual action required", status: 409, detail: FCA_DETAIL, code: "firewall.manual_required" } }
}

function FcaHost() {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  return (
    <>
      <button type="button" onClick={() => setInput({ id: "manual-0123456789abcdef" })}>
        Open the change
      </button>
      <FirewallChange hostId="h1" action="firewall.rule.delete" title="Delete rule 2" input={input} onClose={() => setInput(null)} />
    </>
  )
}

async function fcaOpen(): Promise<{ user: UserEvent; ask: HTMLElement }> {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.delete", available: true, reason: null }] },
    },
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": fcaPlans,
  })
  renderWithProviders(<FcaHost />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open the change" }))
  return { user, ask: await screen.findByRole("dialog", { name: "Change a manual rule?" }) }
}

test("Tab stays inside the manual-rule question", async () => {
  const { user, ask } = await fcaOpen()
  await expectNoAxeViolations(ask)
  for (let i = 0; i < 6; i++) {
    await tabSequence(user, 1)
    expect(ask.contains(document.activeElement)).toBe(true)
  }
})

test("Cancel on the manual-rule question returns focus to the button that opened the change", async () => {
  const { user, ask } = await fcaOpen()
  await user.click(within(ask).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("Escape on the change dialog that follows the question returns focus to the button that opened the change", async () => {
  const { user, ask } = await fcaOpen()
  await user.click(within(ask).getByRole("button", { name: "Change it anyway" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  await within(dialog).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})
````

Create `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`:

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { formatTime } from "@/lib/format"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwSnapshot } from "./fixtures"
import { SnapshotsPanel } from "./snapshots-panel"

const SPA_NEW = `Restore the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}`

function spaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/snapshots": {
      body: {
        snapshots: [
          fwSnapshot({ id: "s-base", createdAt: "2026-10-20T08:00:00Z", changeId: "c0", baseline: true, pinned: true }),
          fwSnapshot({ id: "s-new", createdAt: "2026-10-24T08:00:00Z", changeId: "c2" }),
        ],
      },
    },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.snapshot.restore/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.snapshot.restore", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 8080 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<SnapshotsPanel hostId="h1" />)
}

test("the snapshots list, with its select boxes and Restore buttons named, has no violations", async () => {
  const { container } = spaRender()
  await screen.findByRole("table", { name: "Firewall snapshots" })
  expect(screen.getAllByRole("checkbox").map((c) => c.getAttribute("aria-label"))).toEqual([
    `Select the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}`,
    `Select the snapshot taken ${formatTime("2026-10-20T08:00:00Z")}`,
  ])
  await expectNoAxeViolations(container)
})

test("the restore question has no violations, and Escape returns focus to the snapshot's Restore button", async () => {
  spaRender()
  const user = userEvent.setup()
  const restore = await screen.findByRole("button", { name: SPA_NEW })
  restore.focus()
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  expect(within(ask).getByRole("checkbox", { name: "Allow the restore to add or remove manual rules" })).toBeTruthy()
  await expectNoAxeViolations(ask)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe(SPA_NEW)
})

test("the change dialog that follows the restore question returns focus to the snapshot's Restore button", async () => {
  spaRender()
  const user = userEvent.setup()
  const restore = await screen.findByRole("button", { name: SPA_NEW })
  restore.focus()
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  await user.click(within(ask).getByRole("button", { name: "Plan the restore" }))
  const dialog = await screen.findByRole("dialog", { name: "Restore a snapshot" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe(SPA_NEW)
})
````

Create `web/src/modules/firewall/status-panel.a11y.test.tsx`:

````tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StatusBanners, StatusSummary } from "./status-panel"
import type { FirewallStatus } from "./types"

function spaRender(status: FirewallStatus) {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.state.repair", version: 1, inputHash: "h", diff: [{ op: "~", text: "reload ufw" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(
    <>
      <StatusBanners hostId="h1" status={status} />
      <StatusSummary status={status} />
    </>,
  )
}

test.each([
  ["an unsupported configuration", { supported: false, unsupported: ["/etc/ufw/before.init is executable"] }],
  ["chains that differ", { inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] }],
  ["a sync check that failed", { inSync: false, outOfSync: [], syncError: "iptables-save failed" }],
  ["an inactive firewall", { active: false }],
] as Array<[string, Partial<FirewallStatus>]>)("the banners and summary for %s have no violations", async (_name, over) => {
  const { container } = spaRender(fwStatus(over))
  await screen.findByLabelText("Configured state")
  await expectNoAxeViolations(container)
})

test("the repair dialog is opened and closed by keyboard and Escape returns focus to Repair", async () => {
  spaRender(fwStatus({ inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] }))
  const user = userEvent.setup()
  const repair = await screen.findByRole("button", { name: "Repair" })
  repair.focus()
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Repair the firewall" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Repair")
})
````

Create `web/src/modules/firewall/exposure-page.a11y.test.tsx`:

````tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwExposure } from "./fixtures"
import { ExposurePage } from "./exposure-page"
import type { ExposureView } from "./types"

function epaRender(view: ExposureView) {
  stubFetch({ "GET /api/v1/hosts/h1/resources/firewall/exposure": { body: view } })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/exposure" element={<ExposurePage />} />
    </Routes>,
    { path: "/hosts/h1/firewall/exposure" },
  )
}

const epaClosed = {
  proto: "udp" as const,
  addr: "127.0.0.1",
  port: 5353,
  family: "v4" as const,
  process: "avahi-daemon",
  classes: [{ class: "internet" as const, verdict: "closed" as const, decidedBy: "filter/ufw-user-input#4" }],
}

test("the exposure table, its filters and its notes have no violations", async () => {
  const { container } = epaRender(fwExposure({ notes: ["IPv6 rules could not be read."] }))
  await screen.findByRole("table", { name: "Exposure" })
  await expectNoAxeViolations(container)
})

test("every verdict is a word as well as a colour", async () => {
  epaRender(fwExposure())
  const table = await screen.findByRole("table", { name: "Exposure" })
  const lists = screen.getAllByRole("list", { name: /^Reachability of/ })
  expect(lists).toHaveLength(2)
  expect(table.textContent).toContain("open")
  expect(table.textContent).toContain("partial")
})

test("the filters are used by keyboard: a radio group by arrow keys and the checkbox by Space", async () => {
  epaRender(fwExposure({ rows: [...fwExposure().rows, epaClosed] }))
  const user = userEvent.setup()
  const table = await screen.findByRole("table", { name: "Exposure" })
  expect(table.querySelectorAll("tbody tr")).toHaveLength(3)
  screen.getByRole("radio", { name: "All protocols" }).focus()
  await user.keyboard("{ArrowDown}")
  expect(screen.getByRole("radio", { name: "tcp" }).getAttribute("aria-checked")).toBe("true")
  expect(table.querySelectorAll("tbody tr")).toHaveLength(2)
  screen.getByRole("checkbox", { name: "Only ports the internet can reach" }).focus()
  expect(focusedName()).toBe("Only ports the internet can reach")
  await user.keyboard(" ")
  expect(screen.getByRole("checkbox", { name: "Only ports the internet can reach" }).getAttribute("aria-checked")).toBe("true")
})

test("a read that failed is announced as an alert", async () => {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/exposure": {
      status: 409,
      body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" },
    },
  })
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/exposure" element={<ExposurePage />} />
    </Routes>,
    { path: "/hosts/h1/firewall/exposure" },
  )
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w9-tmp/15 && export TMPDIR=/root/.cache/w9-tmp/15 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/modules/firewall`

Expected: exactly five tests FAIL; every other test of the folder passes (the new axe, Tab, Escape and chained-dialog tests are guards for behaviour that already holds, and a guard that fails means a new defect to fix in the file it names). Quote the output. The five:
- `a state change applied from the page keeps its countdown, and Confirm and Close work by keyboard`: `Unable to find an accessible element with the role "dialog" and name "Enable the firewall"`, at the `expect(screen.getByRole("dialog", …)).toBe(dialog)` line after `UFW is active.` appears.
- `a state change can be reverted by keyboard from the dialog that applied it`: `Tab never reached Revert`.
- `closing the change dialog of a deleted rule leaves focus on the rules, not on the page` and `closing the change dialog of a repair leaves focus on the status, not on the page`: `Unable to find an accessible element with the role "region" and name "Rules"` and `… "Firewall state"`.
- `the drafts start again from the host's settings when those change, in the same card` (in `state-controls.a11y.test.tsx`): the `medium` radio is not checked.

- [ ] **Step 3: Implement**

1. `firewall-change.tsx`: add the optional prop `fallbackFocus?: RefObject<HTMLElement | null>` to `FirewallChangeProps` (doc comment: takes focus when the dialog closes with focus still inside it, because the control that opened it is gone, such as the row of a rule just deleted). Keep a `wasOpen` ref and `const isOpen = input !== null`. In an effect on `isOpen` and `fallbackFocus`: when `isOpen`, set `wasOpen.current = true` and return; when it is false and `wasOpen.current` is true, clear it, read `document.activeElement` and, when that is null, `document.body` or an element with a `[role="dialog"]` ancestor (`closest`), call `fallbackFocus?.current?.focus()`. In one commit every effect cleanup runs before any effect, so by the time this runs the dialog's trap has already restored focus to the opener when the opener is still in the document, and the condition is then false: the fallback never overrides a surviving opener.
2. `rules-table.tsx`: replace the fragment that wraps the table, the manual-rule question and the delete `FirewallChange` by `<section aria-label="Rules" tabIndex={-1} ref={region}>` (a `useRef<HTMLElement>`; add no outline override, so keyboard focus stays visible) and pass `fallbackFocus={region}` to the delete `FirewallChange`.
3. `status-panel.tsx`: `StatusBanners` takes the same optional `fallbackFocus` and passes it to its repair `FirewallChange`.
4. `firewall-page.tsx`: in `FirewallPage` hold `const stateRegion = useRef<HTMLDivElement>(null)`, wrap `StatusBanners` and `StatusSummary` in `<div ref={stateRegion} role="region" aria-label="Firewall state" tabIndex={-1} className="flex flex-col gap-4">`, pass `fallbackFocus={stateRegion}` to `StatusBanners`, and delete the `key` prop of `StateControls`.
5. `state-controls.tsx`: since the page no longer remounts the card, reset the drafts inside it. Keep `const configured = JSON.stringify(status.configured)` and a `seen` state initialised to it; during render, when `seen !== configured`, call `setSeen(configured)`, `setDirection("incoming")`, `setPolicy(currentPolicy(status, "incoming"))` and `setLevel(status.configured.logLevel)` (the derived-state pattern). A comment says why: remounting the card would take the open change dialog with it. The Enable/Disable `Button` stays one element in the same position, so React keeps its DOM node when `active` flips and the dialog's focus return reaches it.
6. Leave `rule-form.tsx`, `snapshots-panel.tsx`, `exposure-page.tsx` and `routes.tsx` alone unless a guard test is red. The radio groups of darkraise-ui move with Up and Down (their default orientation is vertical); the tests use `{ArrowDown}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w9-tmp/15 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/modules/firewall` then `timeout 1200 make lint-web test-web`
Expected: PASS (all firewall tests, then the whole web suite), `tsc --noEmit` and `eslint --max-warnings 0` clean. Quote the output. Then `rm -rf /root/.cache/w9-tmp/15`.

- [ ] **Step 5: Commit**

````bash
git add web/src/modules/firewall
git commit -m "fix(web): keep focus in the firewall change flow"
````

### Task 16: docs: install, first admin, lockout recovery, uninstall and administration

**Files:**
- Create: `docs/user/README.md`, `docs/user/install.md`, `docs/user/first-admin.md`, `docs/user/lockout-recovery.md`, `docs/user/uninstall.md`, `docs/user/administration.md`
- Test: `internal/docs/user_test.go` (package `docs_test`, identifiers `du…` and `TestDu…`)
- Read: `README.md` (sections Install, Add a host, Updates, Break-glass, Uninstall, The first admin, Trust the web root, Use the CLI with a token), `install.sh.in`, `cmd/darkpanel/uninstall.go`, `cmd/darkpanel/agent.go`, `cmd/darkpanel/system.go`, `cmd/darkpanel/update.go`, `internal/cli/enroll.go`, `internal/cli/firewall.go`, `internal/cli/controller_backup.go`, `internal/cli/controller_quarantine.go`, `internal/controller/coreactions/`, `internal/modules/firewall/uninstall.go`, `internal/modules/tailscale/uninstall.go`, `docs/versioning.md`, `schemas/core.*/v1.json` (class, scope and break-glass flag of each action)

**Interfaces:**
- Consumes: Contracts "Docs checks" (`dcRead`, `dcMentions`, `dcModuleNames`, `dcLinks` in `internal/docs/helpers_test.go`, Task 12) and "Documentation paths". The registry's `core` module names come from `dcModuleNames(t, "core")`; command paths come from `cli.CommandPath` (`internal/cli/names.go`).
- Produces: `docs/user/README.md`, the index every other user page is reached from, linking the pages of Tasks 17 and 18, `docs/dev/module-guide.md` (Task 19), `docs/api/README.md` (Task 3) and `docs/versioning.md`; the five pages above. Test identifiers `duPages`, `duIndexed`, `duFlags`, `duDashed`, `duHeadingRE`, `duFenceRE`, `duCodeRE`, `duHTMLRE`, `duLinkRE`, `duPlanRE`, `TestDuIndexLinksEveryPage`, `TestDuPagesLink`, `TestDuInstall`, `TestDuFirstAdmin`, `TestDuLockoutRecovery`, `TestDuUninstall`, `TestDuAdministration`, `TestDuStyle`.

**Items:** 42

**Depends on:** Task 3, Task 12
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 0 = 5

The tests make a page go stale visibly: the flags of `install.sh` and of `darkpanel uninstall` are read from their sources and must equal a fixed list (a new flag fails the test until the page and the list are updated together), the module uninstall options come from the modules' own `UninstallOptions()`, the break-glass command paths are generated from the action names, and every core action and resource name must appear on the administration page. `TestDuStyle` holds all six pages to the documentation style of Global Constraints (one `#` title on the first line, no skipped heading level, tagged fences, no HTML, link text that says where the link goes, no mention of the plan) and is the automated half of the heading-order and link-text check that Task 23 cites for the first-admin and lockout-recovery pages.

- [ ] **Step 1: Write the failing test**

Create `internal/docs/user_test.go`:

````go
package docs_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

// duPages are the pages of docs/user this file's tests read; the README
// indexes them and the module pages.
var duPages = []string{
	"docs/user/install.md",
	"docs/user/first-admin.md",
	"docs/user/lockout-recovery.md",
	"docs/user/uninstall.md",
	"docs/user/administration.md",
}

// duIndexed are the link targets docs/user/README.md must carry, relative to
// docs/user.
var duIndexed = []string{
	"install.md", "first-admin.md", "lockout-recovery.md", "uninstall.md", "administration.md",
	"modules/hostinfo.md", "modules/firewall.md", "modules/docker.md", "modules/tailscale.md", "modules/tailnet.md",
	"modules/derp.md", "modules/npm.md", "modules/cloudflare.md", "modules/panel.md",
	"../dev/module-guide.md", "../api/README.md", "../versioning.md",
}

// duFlags returns the first submatch of every match of re in the file at rel,
// sorted and without duplicates, failing when none matches.
func duFlags(t *testing.T, rel string, re *regexp.Regexp) []string {
	t.Helper()
	var out []string
	for _, m := range re.FindAllStringSubmatch(dcRead(t, rel), -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		t.Fatalf("docs: no flag found in %s with %s", rel, re)
	}
	return out
}

func duDashed(flags []string) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = "--" + f
	}
	return out
}

func TestDuIndexLinksEveryPage(t *testing.T) {
	var links []string
	for _, target := range duIndexed {
		links = append(links, "]("+target+")")
	}
	dcMentions(t, "docs/user/README.md", links...)
}

func TestDuPagesLink(t *testing.T) {
	for _, rel := range duPages {
		dcLinks(t, rel)
	}
}

func TestDuInstall(t *testing.T) {
	// The flags install.sh accepts are its case labels in main.
	flags := duFlags(t, "install.sh.in", regexp.MustCompile(`(?m)^\t\t--([a-z][a-z-]*)\)`))
	want := []string{"agent-advertise", "agent-listen", "base-url", "role", "token-file", "version"}
	if !slices.Equal(flags, want) {
		t.Fatalf("docs: install.sh.in accepts %v, want %v; update this test and docs/user/install.md together", flags, want)
	}
	dcMentions(t, "docs/user/install.md", duDashed(flags)...)
	dcMentions(t, "docs/user/install.md",
		"install.sh", "sudo sh install.sh --role controller", "sudo sh install.sh --role agent",
		"sudo darkpanel agent enroll", "darkpanel host create", "darkpanel system install")
}

func TestDuFirstAdmin(t *testing.T) {
	dcMentions(t, "docs/user/first-admin.md",
		"sudo darkpanel user create --username", "darkpanel user activate", "sudo darkpanel user reset-totp", "TOTP", "authenticator")
}

func TestDuLockoutRecovery(t *testing.T) {
	// The commands are generated from the core action names, so the page
	// follows a rename of an action.
	var userCommands []string
	for _, verb := range []string{"core.user.list", "core.user.reset-password", "core.user.reset-totp", "core.user.delete"} {
		if !slices.Contains(dcModuleNames(t, "core"), verb) {
			t.Fatalf("docs: the registry has no action %s", verb)
		}
		userCommands = append(userCommands, "sudo darkpanel "+strings.Join(cli.CommandPath(verb), " "))
	}
	dcMentions(t, "docs/user/lockout-recovery.md", userCommands...)
	dcMentions(t, "docs/user/lockout-recovery.md",
		"sudo darkpanel update rollback --local", "sudo darkpanel update recover",
		"sudo darkpanel firewall rollback --local",
		"sudo darkpanel "+strings.Join(cli.CommandPath("firewall.snapshot.restore"), " ")+" --local")
}

func TestDuUninstall(t *testing.T) {
	// cmd/darkpanel is package main and cannot be imported, so the flags are
	// read from the source of the command that registers them.
	flags := duFlags(t, "cmd/darkpanel/uninstall.go", regexp.MustCompile(`cmd\.Flags\(\)\.[A-Za-z]+Var\(&\w+, "([a-z-]+)"`))
	want := []string{"abort", "keep-data", "module-option", "stop-tasks", "yes"}
	if !slices.Equal(flags, want) {
		t.Fatalf("docs: darkpanel uninstall registers %v, want %v; update this test and docs/user/uninstall.md together", flags, want)
	}
	dcMentions(t, "docs/user/uninstall.md", duDashed(flags)...)
	dcMentions(t, "docs/user/uninstall.md", "sudo darkpanel uninstall")

	var options []string
	for _, m := range modules.All() {
		un, ok := m.(core.Uninstaller)
		if !ok {
			continue
		}
		for _, o := range un.UninstallOptions() {
			for _, v := range o.Values {
				options = append(options, m.Manifest().ID+"."+o.Key+"="+v)
			}
		}
	}
	if len(options) == 0 {
		t.Fatal("docs: no module declares an uninstall option")
	}
	dcMentions(t, "docs/user/uninstall.md", options...)
}

func TestDuAdministration(t *testing.T) {
	dcMentions(t, "docs/user/administration.md", dcModuleNames(t, "core")...)

	// Backup, restore and the restore quarantine are hand-written commands
	// of package cli, so their flags are read from the files that add them.
	flag := regexp.MustCompile(`cmd\.Flags\(\)\.[A-Za-z]+\("([a-z0-9-]+)"`)
	backup := duFlags(t, "internal/cli/controller_backup.go", flag)
	if !slices.Contains(backup, "passphrase-file") {
		t.Fatalf("docs: controller_backup.go flags %v lack passphrase-file", backup)
	}
	dcMentions(t, "docs/user/administration.md", duDashed(backup)...)
	dcMentions(t, "docs/user/administration.md", duDashed(duFlags(t, "internal/cli/controller_quarantine.go", flag))...)
	dcMentions(t, "docs/user/administration.md",
		"darkpanel controller backup", "darkpanel controller restore", "darkpanel controller quarantine")
}

var (
	duHeadingRE = regexp.MustCompile(`^(#{1,6}) +\S`)
	duFenceRE   = regexp.MustCompile("^```([A-Za-z]*)\\s*$")
	duCodeRE    = regexp.MustCompile("`[^`]*`")
	duHTMLRE    = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	duLinkRE    = regexp.MustCompile(`\[([^\]]*)\]\(`)
	duPlanRE    = regexp.MustCompile(`(?i)\b(task [0-9]+|W9-R[0-9]+|register row|wave [0-9]+|coming soon|will be added|TODO)\b`)
)

// TestDuStyle holds the pages to the documentation style: one title first,
// headings that never skip a level, fenced blocks that are tagged, no HTML,
// links whose text says where they go, and no mention of the plan.
func TestDuStyle(t *testing.T) {
	for _, rel := range append([]string{"docs/user/README.md"}, duPages...) {
		lines := strings.Split(dcRead(t, rel), "\n")
		level, titles, inFence := 0, 0, false
		for i, line := range lines {
			at := func(msg string) { t.Errorf("docs: %s:%d: %s: %q", rel, i+1, msg, line) }
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
				at("HTML in a page")
			}
			for _, m := range duLinkRE.FindAllStringSubmatch(prose, -1) {
				if text := strings.ToLower(strings.TrimSpace(m[1])); slices.Contains([]string{"", "here", "click here", "link", "this link", "this page", "more"}, text) {
					at("link text does not say where the link goes")
				}
			}
			if duPlanRE.MatchString(prose) {
				at("a page names the plan or promises future work")
			}
		}
		if inFence {
			t.Errorf("docs: %s ends inside a fenced block", rel)
		}
		if titles != 1 {
			t.Errorf("docs: %s has %d titles, want 1", rel, titles)
		}
	}
}
````

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w9-tmp/16 && export TMPDIR=/root/.cache/w9-tmp/16 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestDu' ./internal/docs/`
Expected: FAIL. Every test fails with a `docs:` line naming the first missing page (`docs/user/README.md` or `docs/user/install.md` and so on, from `dcRead`); none fails with a derivation error (`install.sh.in accepts …`, `darkpanel uninstall registers …`), because those checks read files that exist. Quote the output.

- [ ] **Step 3: Write the pages**

The Markdown pages are the implementation. Follow Global Constraints "Documentation style": English, plain Markdown, ATX headings with one `#` title on the first line, sentence-case headings that never skip a level, second person, present tense, fenced blocks tagged `sh`, `json` or `text`, commands exactly as the CLI prints them (`sudo darkpanel …`), placeholders such as `<token-file>`, `example.com` and `203.0.113.10`, no HTML, no secret. Describe what exists on `main` only; never name a plan, task, wave or register row. Link text names its target ("the lockout recovery page", never "here"). **Link rule:** the five pages below link only to each other, to files that exist on `main` today (`README.md`, `docs/versioning.md`, `docs/api/README.md` from Task 3) and to external sites; they do not link to `docs/user/modules/*.md` or `docs/dev/module-guide.md`, which other tasks write in the same wave, and name those modules by id in prose instead. Only `docs/user/README.md` links them, and `TestDuPagesLink` does not run on it. Start from the root `README.md` sections and correct them against the sources named here; where they disagree, the source wins.

1. `docs/user/README.md`, title "Darkpanel user guide". One paragraph of what Darkpanel is (the root `README.md` opening paragraph). Sections "Get started" (install, first admin, uninstall), "Day to day" (administration; then one bullet per module page, `modules/hostinfo.md`, `modules/firewall.md`, `modules/docker.md`, `modules/tailscale.md`, `modules/tailnet.md`, `modules/derp.md`, `modules/npm.md`, `modules/cloudflare.md`, `modules/panel.md`, each with one line on what the module does), "When something goes wrong" (lockout recovery) and "Reference" (`../api/README.md` for the API reference, `../versioning.md` for versions and compatibility, `../dev/module-guide.md` for writing a module). Every target of `duIndexed` appears as `](target)`.
2. `docs/user/install.md`, title "Install Darkpanel". Sections: "Before you start" (Ubuntu 22.04 or 24.04, Debian 12 or 13, root, and the tools `install.sh` requires: `openssl`, `sha256sum`, `tar`, `base64`, `od` and `curl` or `wget`; `install.sh.in` `check_os` and `main`); "Install the controller" (`sudo sh install.sh --role controller --agent-advertise <address agents reach>:7443`, with `--agent-listen <ip>:<port>` repeatable and what each of `--role`, `--version`, `--base-url`, `--agent-listen` and `--agent-advertise` does, from the usage comment and `main` of `install.sh.in`: without `--version` the newest stable release in the index is installed, `--base-url` may name any mirror because trust comes from the signatures, the agent flags are for the controller role only); "What the installer does" (verifies the minisign signature of `checksums.txt` with OpenSSL against embedded keys, checks the tarball checksum, installs under `/usr/local/lib/darkpanel/` with `/usr/local/bin/darkpanel` pointing at it, runs `darkpanel system install`; the users, directories, keys and units that creates; the controller runs as `darkpanel`, the agent as root; rerunning finishes or repairs an install; the listeners and the data and key directories exactly as the root `README.md` Install section states them); "Check install.sh before you run it" (the minisign and checksum steps and the `gh attestation verify` check of the root `README.md`); "Add a host" (Hosts page "Add host" or `darkpanel host create --name <name>`; the token lasts one hour, works once and is shown once; on the new host `sudo sh install.sh --role agent`, the token read from the prompt or from `--token-file`, a root-owned file with mode 0600; the agent connects out and opens no port; `darkpanel host create --tailnet-join --tags <tag>` for a host that must join the tailnet first, whose answer is a bootstrap bundle that `--token-file` also accepts; `darkpanel host revoke`, `darkpanel host token` and `sudo darkpanel agent enroll` for a revoked host, with `agent enroll` reading `--token-file`, a hidden prompt or standard input); "Reach the web interface" (the loopback listeners and the `ssh -L 8080:127.0.0.1:8080 <host>` forward; the HTTPS web root and `sudo darkpanel controller web-ca`, from "Trust the web root"); and a closing pointer to the first admin page. Sources: `README.md`, `install.sh.in`, `cmd/darkpanel/system.go`, `cmd/darkpanel/agent.go`.
3. `docs/user/first-admin.md`, title "Create the first admin". There is no default account and no setup page. Sections: "Create the admin" (`sudo darkpanel user create --username admin` on the controller's host, through the controller socket; it asks for the password twice, prints a QR code, the `otpauth` URI and the TOTP secret for an authenticator app, asks for a code and activates the user; the secret is shown once; the final line `User <name> is active and can log in.`; source `internal/cli/enroll.go` `runEnrollment`); "If the activation did not finish" (`darkpanel user activate --username <name>`, the hint `activateHint` prints, with `--socket` or `--dev` when the first command used them); "Sign in" (username, password and the six-digit code on the web interface; reach it as the install page says); "If you lose the authenticator" (`sudo darkpanel user reset-totp --username admin` issues a new secret and runs the same flow; for a lost password or a locked account see the lockout recovery page); "Next: an API token for the CLI" (the Tokens page; scopes `read`, `operate` and `admin`; expiry of 90 days unless `expiresInDays` says otherwise; `DARKPANEL_TOKEN` and `DARKPANEL_URL`, from "Use the CLI with a token"). Keep the heading order strict: this page is one of the flows whose heading order and link text the accessibility record checks by hand.
4. `docs/user/lockout-recovery.md`, title "Recover from a lockout". Sections: "Before you start" (these commands run as root on the host itself and use the controller socket or the agent socket, so they work when the web interface and the API do not; `--local` sends an agent-side command to the local agent's socket, for example `sudo darkpanel hostinfo summary get --local`); "You cannot sign in" (`sudo darkpanel user list`, `sudo darkpanel user reset-password --username <name>`, `sudo darkpanel user reset-totp --username <name>` and `sudo darkpanel user delete --username <name>`: what each does, which of them prints a new secret or asks for a password, from `internal/controller/coreactions/users.go` and `schemas/core.user.*/v1.json`); "A firewall change cut you off" (the revert timer undoes an unconfirmed change by itself, 60 seconds unless `--confirm-timeout` says otherwise, 10 to 900; if you are already out, on the host `sudo darkpanel firewall rollback --local` reverts a pending change or restores the snapshot before a confirmed one and confirms it in the same command, with `--change <id>` to name one, `--yes`, `--manual` and `--override firewall.lockout`; and `sudo darkpanel firewall snapshot restore --local --id <id> --confirm` restores a named snapshot; source `internal/cli/firewall.go`); "An update left a host broken" (`sudo darkpanel update rollback --local --yes` puts the previous binary back and nothing stops it once it runs; if a crash or power loss cut it short the services can stay stopped until the next boot, and `sudo darkpanel update recover` finishes a rollback that had started replacing the binary and restarts the services, or ends one that had not, so it can be run again; automatic rollback within two minutes from `docs/versioning.md`); "Restore the controller from a backup" (one paragraph pointing to the administration page). Name the Tailscale and panel-exposure lockouts only by module id.
5. `docs/user/uninstall.md`, title "Uninstall Darkpanel". Sections: "What uninstall does" (`sudo darkpanel uninstall` runs as root, prints the plan, asks `Proceed? [y/N]`, then stops and removes the services, units, binary, users, data and keys the host's ownership manifest lists; a declined prompt exits 3; without a terminal only `--yes` proceeds; source `cmd/darkpanel/uninstall.go`); "Flags" (one entry for each of `--keep-data`, `--yes`, `--module-option`, `--stop-tasks` and `--abort`, from the help strings in `newUninstallCmd`; `--abort` takes none of `--stop-tasks`, `--keep-data` and `--module-option`); "Module options" (`--module-option <module>.<key>=<value>`, repeatable; the plan lists every option with its default; for each option of each module that declares one, every value as `<module>.<key>=<value>` with the module's help text and default, read from `internal/modules/firewall/uninstall.go` (`firewall.rules=remove`, `firewall.rules=keep`, `firewall.rules=baseline`) and `internal/modules/tailscale/uninstall.go` (`tailscale.package=keep`, `tailscale.package=remove`); the page prints each as written in a `--module-option` example); "Keep your data" (what `--keep-data` keeps: databases, backups, update records, keys, agent credentials and users, so a later install finds them); "Unfinished changes" (`--stop-tasks`: without it uninstall refuses while a change's task unit is active); "Abort an uninstall" (`--abort` clears the uninstall barrier of an uninstall that stopped before it stopped the services, so the agent accepts changes again, audited as this host's local root user). Read `internal/install/uninstall.go` for the plan's order and refusals and state only what it does.
6. `docs/user/administration.md`, title "Administer Darkpanel". One section per area, and in each section every action or resource of the `core` module by its name, the command the CLI generates for it (`darkpanel <path>`, from `cli.CommandPath`: `core.audit.list` is `darkpanel audit list`), its minimum scope and one sentence of what it does and whether it can be undone (class from `schemas/core.*/v1.json`): "Users" (`core.user.activate`, `core.user.create`, `core.user.delete`, `core.user.list`, `core.user.reset-password`, `core.user.reset-totp`), "API tokens" (`core.token.create`, `core.token.list`, `core.token.revoke`; scopes `read`, `operate`, `admin`; 90 days), "Hosts" (`core.host.create`, `core.host.delete`, `core.host.enroll-local`, `core.host.list`, `core.host.revoke`, `core.host.token`, `core.host.update-policy` with the `stable` and `beta` channels and the weekly window), "Modules on a host" (`core.module.disable`, `core.module.enable`, `core.module.list`: a module is enabled per host), "Audit log" (`core.audit.list`), "Updates and rollouts" (`core.update.apply`, `core.update.rollback`, `core.update.rollout`, `core.update.rollout-cancel`, `core.update.rollouts`, `core.update.status`: one host at a time, the controller's own host first, stopping at the first failure; the one-minor-at-a-time and agent-window rules of `docs/versioning.md`, linked as the versioning page), and "Backup and restore" (the hand-written commands `darkpanel controller backup --out <file>` with `--passphrase-file`, `--argon2-t`, `--argon2-m`, `--argon2-p`; `darkpanel controller restore --in <file>` with `--passphrase-file` and `--recover`; `darkpanel controller quarantine list`, `resolve <record> <done|never|repair>`, `enroll-token <host>` and `release` with `--keep-unresolved` and `--json`; read the flags and help text in `internal/cli/controller_backup.go` and `internal/cli/controller_quarantine.go`, and the encrypted-archive, restore-phase and quarantine behaviour in spec section 10 "Backup format" and "Restore", stating the operator-visible parts only: the passphrase prompt or file, that both services must be stopped for a restore, that a restore quarantines unfinished work until you settle or release it). When a name appears in the test failure as missing, add it to its section.

- [ ] **Step 4: Run the test to verify it passes**

Run: `export TMPDIR=/root/.cache/w9-tmp/16 GOTOOLCHAIN=local && timeout 300 go test -count=1 -v -run 'TestDu' ./internal/docs/`
Expected: PASS for all eight tests. Then the Go constraints of Global Constraints, each with its real output: `timeout 600 go build ./...`; `timeout 300 go vet ./...` (no output); `timeout 1500 go test -count=1 ./...` (every line `ok` or `?`); `timeout 300 go test -race -count=1 ./internal/docs/`; `timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `timeout 300 go mod tidy -diff` (no output). Then read each page once for the rules the tests cannot check (sentence-case headings, second person, accurate commands against the sources above). Finally `go clean -testcache && rm -rf /root/.cache/w9-tmp/16`.

- [ ] **Step 5: Commit**

````bash
git add docs/user/README.md docs/user/install.md docs/user/first-admin.md docs/user/lockout-recovery.md docs/user/uninstall.md docs/user/administration.md internal/docs/user_test.go
git commit -m "docs(user): add install, admin and recovery pages"
````

### Task 17: docs: module pages for hostinfo, firewall, docker, tailscale and tailnet

**Files:**
- Create: `docs/user/modules/hostinfo.md`, `docs/user/modules/firewall.md`, `docs/user/modules/docker.md`, `docs/user/modules/tailscale.md`, `docs/user/modules/tailnet.md`
- Test: `internal/docs/modules_a_test.go` (package `docs_test`, identifiers `dm…` and `TestDm…`)
- Read: each module's manifest and wire types (`internal/modules/hostinfo/hostinfo.go`; `internal/modules/firewall/` including `wire.go`, `uninstall.go` and `guard/guard.go`; `internal/modules/docker/` including `wire.go`, `compose_check.go` and `owned.go`; `internal/modules/tailscale/` including `wire.go` and `link/link.go`; `internal/modules/tailnet/` including `wire.go`, `account.go` and `policy/policy.go`), `internal/core/errcode/errcode.go`, `internal/cli/names.go` and `internal/cli/firewall.go`, `schemas/<action>/v1.json` for the class, scope and side of each action, the root `README.md`, and spec sections 2, 3, 4.1 and 4.2 (`docs/dforge/specs/2026-10-21-v1-completion-design.md`) for behaviour: the lockout guard, auto-revert, snapshots, the Docker integration, the exposure view, Compose refusals, node preferences and the tailnet policy

**Interfaces:**
- Consumes: Contracts "Docs checks" (`dcRead`, `dcMentions`, `dcModuleNames`, `dcLinks`, Task 12) and "Documentation paths". `dcModuleNames(t, id)` is the sorted list of every non-internal action and resource name of the module: `hostinfo` 1, `firewall` 22, `docker` 26, `tailscale` 10, `tailnet` 14 at the time of writing.
- Produces: the five pages, linked from `docs/user/README.md` (Task 16). Test identifiers `dmPages`, `dmCodeRE`, `dmHTMLRE`, `dmFields`, `dmInputFields`, `dmUninstallOptions`, `dmCodes`, `TestDmEveryActionAndResourceIsDocumented`, `TestDmHostinfo`, `TestDmFirewall`, `TestDmDocker`, `TestDmTailscale`, `TestDmTailnet`, `TestDmStyle`.

**Items:** 42

**Depends on:** Task 3, Task 12
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 0 = 5

The test holds each page to the registry and to the code: every action and resource name, and the command the CLI generates for it (`darkpanel firewall rule add`; a resource's command ends in `get`, as in `darkpanel firewall status get`), every field of the inputs and outputs named in the test (read by reflection from the registry's `InputType` and from `hostinfo.Summary`), every error code and guard id of the module (the `errcode` constants and the guard packages' ids), the Docker API floor (`dockerapi.MinAPIVersion`) and each uninstall option the module declares, so a rename or an added action fails the build until the page follows.

- [ ] **Step 1: Write the failing test**

Create `internal/docs/modules_a_test.go`:

````go
package docs_test

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/hostinfo"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/registry"
)

// dmPages maps each module this file covers to its page.
var dmPages = map[string]string{
	"hostinfo":  "docs/user/modules/hostinfo.md",
	"firewall":  "docs/user/modules/firewall.md",
	"docker":    "docs/user/modules/docker.md",
	"tailscale": "docs/user/modules/tailscale.md",
	"tailnet":   "docs/user/modules/tailnet.md",
}

var (
	dmCodeRE = regexp.MustCompile("`[^`]*`")
	dmHTMLRE = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
)

// dmFields returns the JSON names of the fields of typ, with the fields of
// embedded structs flattened in and the "guard" of a guarded
// action left out.
func dmFields(typ reflect.Type) []string {
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct && name == "":
			out = append(out, dmFields(f.Type)...)
		case name == "" || name == "-" || name == "guard" || name == "overrides" || !f.IsExported():
		default:
			out = append(out, name)
		}
	}
	return out
}

// dmInputFields returns the JSON names of the input of the action name.
func dmInputFields(t *testing.T, name string) []string {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range reg.Actions() {
		if a.Spec.Name == name {
			fields := dmFields(a.InputType)
			if len(fields) == 0 {
				t.Fatalf("docs: %s has no input fields", name)
			}
			return fields
		}
	}
	t.Fatalf("docs: the registry has no action %s", name)
	return nil
}

// dmUninstallOptions returns module id's uninstall options as
// <module>.<key>=<value>, one per allowed value.
func dmUninstallOptions(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	for _, m := range modules.All() {
		if un, ok := m.(core.Uninstaller); ok && m.Manifest().ID == id {
			for _, o := range un.UninstallOptions() {
				for _, v := range o.Values {
					out = append(out, id+"."+o.Key+"="+v)
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("docs: module %s declares no uninstall option", id)
	}
	return out
}

func dmCodes(codes ...errcode.Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = string(c)
	}
	return out
}

// TestDmEveryActionAndResourceIsDocumented holds each page to naming every
// action and resource of its module in the registry, with the command the
// CLI generates for it, and to linking only files that exist.
func TestDmEveryActionAndResourceIsDocumented(t *testing.T) {
	for id, page := range dmPages {
		names := dcModuleNames(t, id)
		if len(names) == 0 {
			t.Fatalf("docs: the registry has no action or resource of module %s", id)
		}
		dcMentions(t, page, names...)
		var commands []string
		for _, n := range names {
			commands = append(commands, "darkpanel "+strings.Join(cli.CommandPath(n), " "))
		}
		dcMentions(t, page, commands...)
		dcLinks(t, page)
	}
}

func TestDmHostinfo(t *testing.T) {
	dcMentions(t, dmPages["hostinfo"], dmFields(reflect.TypeFor[hostinfo.Summary]())...)
}

func TestDmFirewall(t *testing.T) {
	page := dmPages["firewall"]
	dcMentions(t, page, guard.GuardID, string(errcode.PlanGuardFailed))
	dcMentions(t, page, dmCodes(
		errcode.FirewallManualRequired, errcode.FirewallUnsupportedConfiguration, errcode.FirewallUnsupportedIntegration,
		errcode.FirewallDockerUserInUse, errcode.FirewallPredictionMismatch,
	)...)
	dcMentions(t, page, dmInputFields(t, "firewall.docker.set-sources")...)
	dcMentions(t, page, "--confirm-timeout", "--confirm", "900", "baseline", "firewall.reconcile", "darkpanel.module")
	dcMentions(t, page, "sudo darkpanel firewall rollback --local",
		"sudo darkpanel "+strings.Join(cli.CommandPath("firewall.snapshot.restore"), " ")+" --local")
	for _, o := range dmUninstallOptions(t, "firewall") {
		dcMentions(t, page, "--module-option "+o)
	}
}

func TestDmDocker(t *testing.T) {
	page := dmPages["docker"]
	dcMentions(t, page, dmCodes(errcode.DockerEngineUnavailable, errcode.DockerInUse, errcode.DockerOwned, errcode.DockerComposeUnavailable)...)
	dcMentions(t, page, dockerapi.MinAPIVersion, "[redacted]", "darkpanel.module", "provider", "--no-build", "include", "profile",
		"com.docker.compose.project")
}

func TestDmTailscale(t *testing.T) {
	page := dmPages["tailscale"]
	dcMentions(t, page, link.GuardID)
	dcMentions(t, page, dmCodes(
		errcode.TailscaleUnavailable, errcode.TailscaleRepoConflict, errcode.TailscalePackagePresent,
		errcode.TailscalePackageHeld, errcode.TailscalePinFailed,
	)...)
	dcMentions(t, page, dmInputFields(t, "tailscale.prefs.set")...)
	dcMentions(t, page, "STUN", "DERP")
	for _, o := range dmUninstallOptions(t, "tailscale") {
		dcMentions(t, page, "--module-option "+o)
	}
}

func TestDmTailnet(t *testing.T) {
	page := dmPages["tailnet"]
	dcMentions(t, page, dmCodes(errcode.TailnetPolicyConflict, errcode.TailnetPolicyInvalid)...)
	dcMentions(t, page, dmInputFields(t, "tailnet.account.connect")...)
	dcMentions(t, page, dmInputFields(t, "tailnet.authkey.create")...)
	dcMentions(t, page, "devices:core", "devices:routes", "auth_keys", "policy_file")
	dcMentions(t, page, "`set`", "`delete`", "`addItem`", "`removeItem`", "--tailnet-join", "ETag")
}

// TestDmStyle holds the module pages to the documentation style that
// matters to a reader of the source: one title on the first line, fenced
// blocks that are tagged, and no HTML.
func TestDmStyle(t *testing.T) {
	ids := make([]string, 0, len(dmPages))
	for id := range dmPages {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		rel := dmPages[id]
		titles, inFence := 0, false
		for i, line := range strings.Split(dcRead(t, rel), "\n") {
			if strings.HasPrefix(line, "```") {
				if !inFence && !slices.Contains([]string{"```sh", "```json", "```text"}, strings.TrimSpace(line)) {
					t.Errorf("docs: %s:%d: a fenced block must be tagged sh, json or text: %q", rel, i+1, line)
				}
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			if strings.HasPrefix(line, "# ") {
				titles++
				if i != 0 {
					t.Errorf("docs: %s:%d: the title must be the first line", rel, i+1)
				}
			}
			if prose := dmCodeRE.ReplaceAllString(line, ""); dmHTMLRE.MatchString(prose) {
				t.Errorf("docs: %s:%d: HTML in a page: %q", rel, i+1, line)
			}
		}
		if titles != 1 {
			t.Errorf("docs: %s has %d titles, want 1", rel, titles)
		}
	}
}
````

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w9-tmp/17 && export TMPDIR=/root/.cache/w9-tmp/17 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestDm' ./internal/docs/`
Expected: FAIL. Every test fails with a `docs:` line naming the first missing page under `docs/user/modules/` (from `dcRead`); the registry reads, the reflection and the uninstall-option lookups in the helpers must not fail first (a `registry has no action` or `declares no uninstall option` failure means a name in the test is wrong). Quote the output.

- [ ] **Step 3: Write the pages**

The Markdown pages are the implementation. Follow Global Constraints "Documentation style" (English, plain Markdown, one `#` title on the first line, sentence-case headings, second person, present tense, tagged fences, no HTML, `sudo darkpanel …` exactly as the CLI prints it, placeholders only, no plan, task, wave or register vocabulary, only what exists on `main`). **Link rule:** these pages link only to each other, to `docs/versioning.md` and `docs/api/README.md` (both exist) and to external sites; they do not link to `install.md`, `lockout-recovery.md`, `administration.md`, to the `derp`, `npm`, `cloudflare` and `panel` pages or to `docs/dev/module-guide.md`, which other tasks write in the same wave, and name those by title or module id in prose. Every page has the same skeleton: a one-paragraph summary (what the module does, where it runs: the agent on each host or the controller, and its page in the web interface at `/hosts/<id>/<module>` or `/tailnet`); "Requirements" (what the host needs, from the module's preflight and the availability reasons it reports); "Actions and resources" (a table with one row per name in `dcModuleNames`: the name, the command `darkpanel <path>`, whether it only reads or changes something and, for a change, whether it is undone by the revert timer, undone by an inverse or irreversible and needs a typed confirmation, the minimum scope, and a one-line description; take the class and scope from `schemas/<name>/v1.json`); then the module's own sections below; and "Troubleshooting" (each error code the module returns, with its cause and the fix). Every changing action also notes that `--yes` skips the `Apply? [y/N]` question and `--confirm`/`--confirm-timeout` apply to a revertible one; do not restate this elsewhere.

1. `docs/user/modules/hostinfo.md`, title "Host information". One resource, `hostinfo.summary`, `darkpanel hostinfo summary get`, read-only, agent side. A table of every top-level field of the answer under `--json`, by its JSON name, with its unit: `hostname`, `os` (`id`, `versionId`, `prettyName` from `/etc/os-release`), `kernel`, `arch`, `uptimeSeconds`, `bootTime`, `cpu` (`model`, `logicalCount`), `memory` and `swap` and `rootFs` (bytes), `load` (1, 5 and 15 minute), `darkpanelVersion`. Say where each value is read (`/proc/cpuinfo`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `statfs` on `/`), the host page's summary card, and that with the controller unreachable `sudo darkpanel hostinfo summary get --local` answers from the local agent.
2. `docs/user/modules/firewall.md`, title "Firewall". Sections beyond the skeleton: "How it works" (UFW's configured state from its files and the loaded state from `iptables-save`, and the in-sync check between them; a host out of sync or with an unsupported configuration refuses every change except a repair, `firewall.unsupported_configuration`); "Rules" (the rule model: action `allow`, `deny`, `limit`, `reject`, direction, protocol, from and to with ports, interfaces, family, logging, comment; Darkpanel's rules carry a `darkpanel:<id>` tag, manual rules are shown as such and a change to one needs consent, `firewall.manual_required`; a rule the model cannot express is read-only; `firewall.rule.add`, `firewall.rule.delete`, `firewall.rules.apply`); "Settings" (`firewall.state.enable`, `firewall.state.disable`, `firewall.state.repair`, `firewall.defaults.set`, `firewall.logging.set`); "The lockout guard" (the plan predicts the loaded state after the change and evaluates your protected flows, SSH, the agent's link to the controller, and fails the guard `firewall.lockout` with code `plan.guard_failed` when a flow would be dropped; it is overridable with `--override firewall.lockout` or the checkbox in the dialog, except on an unsupported or out-of-sync host; `firewall.prediction_mismatch` when the prediction and UFW's answer differ); "Auto-revert" (every change is revertible: unless you confirm within the timeout, 60 seconds by default, `--confirm-timeout` from 10 to 900, the agent reverts it, even if the agent restarts; the dialog shows a countdown with Confirm and Revert; the CLI's `--confirm` confirms at once); "Snapshots" (`firewall.snapshots`, `firewall.snapshot.compare`, `firewall.snapshot.restore`: one before every change, the baseline pinned before the first change and never pruned, the retention counts of spec 2.5, a restore lists every manual rule it would add or remove and needs `--manual`); "Docker integration" (`firewall.docker`, `firewall.docker.enable`, `firewall.docker.disable`, `firewall.docker.repair`, `firewall.docker.set-sources` with `privateSources` and `forwardExemptions`, `firewall.container.allow`, `firewall.container.deny`, `firewall.container.remove`, `firewall.containers.apply`: why UFW alone does not protect published container ports, the marked block Darkpanel writes, per-binding policy, the background task `firewall.reconcile` that keeps it in step with the containers, foreign `DOCKER-USER` rules refused with `firewall.docker_user_in_use`, `firewall.unsupported_integration`); "Exposure" (`firewall.exposure`: one row per listening port, its reachability by source class, `open`, `partial`, `closed`, `filtered` or `unknown`, and which rule decides it; the filters of the table); "Break-glass" (`sudo darkpanel firewall rollback --local` and `sudo darkpanel firewall snapshot restore --local --id <id> --confirm`, from `internal/cli/firewall.go`); "Uninstall" (`--module-option firewall.rules=remove`, `firewall.rules=keep`, `firewall.rules=baseline` with the one-line meaning of each from `internal/modules/firewall/uninstall.go`, and that Docker's owned chains are cleaned up in every case). The container page's Allow and Deny buttons are the `firewall.container.allow` and `firewall.container.deny` actions; mention that they are disabled with a reason when the integration is not supported. The label `darkpanel.module` marks objects the firewall never touches.
3. `docs/user/modules/docker.md`, title "Docker". Sections beyond the skeleton: "Requirements" (the socket is present and the Engine API is `1.44` or later, Docker Engine 25; otherwise `docker.engine_unavailable`); "Containers, images, networks and volumes" (the resources `docker.containers`, `docker.images`, `docker.networks`, `docker.volumes`, `docker.engine`, `docker.projects`; start, stop, restart, remove, inspect, logs with follow and `since`/`tail`; the undo of each action as the table of spec section 3 gives it, an irreversible one typing the object's name); "Secrets" (`docker.container.inspect` shows environment values as `[redacted]`; `docker.container.reveal-env` returns them through secret delivery, admin scope, and refuses Darkpanel-owned containers; logs of Darkpanel-owned containers are filtered); "Guards" (`docker.in_use` when a network or volume is used by a container, `docker.owned` for objects labelled `darkpanel.module`; prune lists exactly the images and bytes it will remove); "Compose" (`docker.compose.up`, `docker.compose.down`, `docker.compose.pull`, `docker.compose.restart`: admin scope, long-running, the plan says it runs the project's Compose file as root; discovery of projects from container labels such as `com.docker.compose.project` so a project stays listed after `down`; the per-project `profile` list; the checks that refuse a project with `docker.compose_unavailable`: relative paths, missing or wrongly owned files, symlinks, a service with a `provider` key, unknown top-level or service keys, a service with a build section and no local image (`up` always runs with `--no-build`), remote `include` and `extends`; `docker.owned` for a project named `darkpanel*` or touching Darkpanel-owned objects; pulls from private registries through Compose are not supported); "Exposure in container views" (the container page shows the firewall's exposure rows for its published ports and the buttons modules add, firewall's Allow and Deny among them). Every `docker.*` action named in `dcModuleNames` is in the table.
4. `docs/user/modules/tailscale.md`, title "Tailscale". The node on one host (agent side). Sections beyond the skeleton: "Requirements" (supported OS only; `tailscale.unavailable` while tailscaled's socket does not answer, except for `tailscale.package.install`); "Status and latency" (`tailscale.status`: backend state, the node's addresses, DNS name and tags, peers with their connection `direct`, `derp <region>` or `peer-relay`, the preferred DERP region, whether Tailscale SSH is on; `tailscale.latency`: STUN latency and DERP HTTPS probe latency per region, each labelled as such and shown as unavailable when it cannot be measured); "Preferences" (`tailscale.prefs.set` with each field of its input by JSON name, only the fields you give change and a revert puts exactly those back; `tailscale.prefs.repair` with `outcome` `restore` or `keep` for a change whose revert failed); "The link guard" (the overridable guard `tailscale.link`: it fails when the change would cut a flow that depends on Tailscale or Tailscale SSH, and which changes it checks; override it only when you have another way in); "Login and logout" (`tailscale.node.login` with an auth key, which is a secret, `hostname`, `advertiseTags` and `relogin`; `tailscale.node.logout` irreversible and typed); "Package" (`tailscale.package.install`, `tailscale.package.update`, `tailscale.package.pin`, `tailscale.package.unpin`; `tailscale.package_present`, `tailscale.package_held`, `tailscale.repo_conflict`, `tailscale.pin_failed`; a pin is a transaction that rolls itself back); "Uninstall" (`--module-option tailscale.package=keep` and `tailscale.package=remove`, from `internal/modules/tailscale/uninstall.go`: remove takes out the package only if Darkpanel installed it).
5. `docs/user/modules/tailnet.md`, title "Tailnet". The tailnet account and policy, controller side, one tailnet per controller, web page `/tailnet`. Sections beyond the skeleton: "Connect your tailnet" (`tailnet.account.connect` with the input fields `tailnet`, `clientId`, `clientSecret` (a secret) and `tagsAllowed`; the OAuth client needs the scopes `devices:core`, `devices:routes`, `auth_keys` and `policy_file`; the connect probes what the client can do and shows each capability's state; `tailnet.account.get`; `tailnet.account.disconnect` irreversible and typed); "Devices" (`tailnet.device.list`, `tailnet.device.authorize`, `tailnet.device.set-routes`, `tailnet.device.expire`, `tailnet.device.delete`, the last two irreversible and typed); "Auth keys" (`tailnet.authkey.create` with its input fields `tags` (each within `tagsAllowed`), `reusable`, `ephemeral`, `preauthorized` and `expirySeconds`, `tailnet.authkey.list`; the key is shown once); "The policy file" (`tailnet.policy.get`; `tailnet.policy.set` takes the full HuJSON text, validates it with Tailscale, shows a line diff and applies it with the ETag it read, so an edit made elsewhere meanwhile fails with `tailnet.policy_conflict` and an invalid file with `tailnet.policy_invalid`; undo restores the earlier text only while the policy has not changed since, otherwise the change needs repair; `tailnet.policy.patch` takes typed edits `set`, `delete`, `addItem` and `removeItem` addressed by a JSON Pointer, keeps comments and unrelated content, and planning the same edits again gives an empty diff; other modules use it); "Join a host to the tailnet" (`darkpanel host create --tailnet-join --tags <tag>`, the one-use pre-authorized key with an hour's expiry inside the bootstrap bundle that `install.sh --role agent` takes, the host joins the tailnet and then enrolls over it; `tailnet.node.join` as the retry for an enrolled host); "Troubleshooting" (`tailnet.policy_conflict`, `tailnet.policy_invalid`, and what a missing capability means).

- [ ] **Step 4: Run the test to verify it passes**

Run: `export TMPDIR=/root/.cache/w9-tmp/17 GOTOOLCHAIN=local && timeout 300 go test -count=1 -v -run 'TestDm' ./internal/docs/`
Expected: PASS for all seven tests. The failure messages name every missing string; add each to its page rather than loosening the test. Then the Go constraints of Global Constraints, each with its real output: `timeout 600 go build ./...`; `timeout 300 go vet ./...` (no output); `timeout 1500 go test -count=1 ./...` (every line `ok` or `?`); `timeout 300 go test -race -count=1 ./internal/docs/`; `timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `timeout 300 go mod tidy -diff` (no output). Read each page once against its sources (the test checks that names exist, not that the prose is true). Finally `go clean -testcache && rm -rf /root/.cache/w9-tmp/17`.

- [ ] **Step 5: Commit**

````bash
git add docs/user/modules/hostinfo.md docs/user/modules/firewall.md docs/user/modules/docker.md docs/user/modules/tailscale.md docs/user/modules/tailnet.md internal/docs/modules_a_test.go
git commit -m "docs(user): add hostinfo to tailnet module pages"
````

### Task 18: docs: module pages for derp, npm, cloudflare and panel

**Files:**
- Create: `docs/user/modules/derp.md`
- Create: `docs/user/modules/npm.md`
- Create: `docs/user/modules/cloudflare.md`
- Create: `docs/user/modules/panel.md`
- Test: `internal/docs/modules_b_test.go`

**Interfaces:**
- Consumes: Contracts "Docs checks" (`dcRead`, `dcMentions`, `dcModuleNames`, `dcLinks` from `internal/docs/helpers_test.go`, Task 12), "Documentation paths" and Global Constraints "Documentation style" and "Secrets". The test also reads the registry (`registry.Build()`) and the generated CLI (`cli.NewRoot`), and links to `docs/api/<module>.md`, which Task 3 generates.
- Produces: the four pages at the paths of Contracts "Documentation paths". Task 16's `docs/user/README.md` links to them and Task 24's `TestWcDocsLinks` checks their links. Test identifiers are prefixed `Dn`/`dn`.

**Items:** 42

**Depends on:** Task 3, Task 12
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/docs/modules_b_test.go`. `dnPages` holds, per page, the verbatim facts the page must state; each fact is an error code, a port, a path the module creates on the host or a name the reader types or reads in the UI, and the page-content list in Step 3 gives its source. The other tests check that every non-internal action of the module is named (`dcModuleNames`), that every relative link resolves, that the six sections exist in order, that code fences carry `sh`, `json` or `text` only, that no HTML or plan wording appears, and that every `darkpanel ...` line of an `sh` block names a command of the real CLI with flags that command has.

```go
package docs_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/registry"
)

// dnPage is one module page and the facts it must state, each verbatim: an error code a refusal answers with, a
// port, a path a module creates on the host, a name the page's reader types or finds.
type dnPage struct {
	module string
	path   string
	facts  []string
}

var dnPages = []dnPage{
	{"derp", "docs/user/modules/derp.md", []string{
		"derp.port_in_use", "derp.not_ready", "derp.region_taken", "derp.no_fallback", "derp.verify_failed",
		"80/tcp", "443/tcp", "3478/udp",
		"/usr/local/lib/darkpanel/derper", "/etc/systemd/system/darkpanel-derper.service", "/var/lib/darkpanel/derp", "darkpanel-derp",
		"-verify-clients", "letsencrypt", "sha256-raw:", "30 days", "omitDefaultRegions", "tailscaled", "900", "999",
	}},
	{"npm", "docs/user/modules/npm.md", []string{
		"npm.not_public", "npm.unsupported_target", "npm.reserved", "npm.setup_failed", "npm.unhealthy",
		"darkpanel-npm", "darkpanel-npm-data", "darkpanel-npm-letsencrypt", "127.0.0.1:81",
		"/var/lib/darkpanel/agent/npm-backups", "Docker 28.0", "user-defined", "--acme-email", "derp.port_in_use",
	}},
	{"cloudflare", "docs/user/modules/cloudflare.md", []string{
		"cloud.not_connected", "cloud.auth_failed", "cloud.permission_missing", "cloud.rate_limited", "cloud.conflict",
		"cloud.uncertain_outcome", "cloud.cleanup_pending", "cloudflare.reserved", "cloudflare.unsupported_target",
		"cloudflare.connector_missing", "Cloudflare Tunnel: Edit", "Zone Read", "DNS: Edit",
		"darkpanel-cloudflared-", "/var/lib/darkpanel/agent/cloudflare-tokens", "user-defined", "overwritten",
	}},
	{"panel", "docs/user/modules/panel.md", []string{
		"panel.wrong_host", "panel.totp_incomplete", "panel.already_exposed", "panel.not_exposed",
		"npm.not_public", "cloudflare.connector_missing", "darkpanel-panel", "dp-panel", "172.31.255.0/24", "9443",
		"sudo darkpanel panel listener revoke", "Panel exposed via", "Let's Encrypt",
	}},
}

// dnSections are the level-two headings every page has, in this order.
var dnSections = []string{
	"## Overview", "## Before you start", "## Use it in the web UI", "## Use it from the CLI",
	"## What it changes on the host", "## When something goes wrong",
}

var (
	dnFenceTags = []string{"sh", "json", "text"}
	// dnInternalRE finds words that name the plan, the wave or the register a page must never mention.
	dnInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9])`)
	// dnHTMLRE finds an HTML tag outside inline code, which dnInlineRE removes first.
	dnHTMLRE   = regexp.MustCompile(`</?[a-z][a-z0-9]*[ />]`)
	dnInlineRE = regexp.MustCompile("`[^`]*`")
)

func TestDnPagesNameEveryAction(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			names := dcModuleNames(t, p.module)
			if len(names) == 0 {
				t.Fatalf("module %s has no action or resource to document", p.module)
			}
			dcMentions(t, p.path, names...)
		})
	}
}

func TestDnPagesStateTheirFacts(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) { dcMentions(t, p.path, p.facts...) })
	}
}

func TestDnPagesLinkOnlyToFiles(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) { dcLinks(t, p.path) })
	}
}

func TestDnPagesHaveTheirSections(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			lines := strings.Split(dcRead(t, p.path), "\n")
			titles := 0
			next := 0
			for _, line := range lines {
				switch {
				case strings.HasPrefix(line, "# "):
					titles++
				case next < len(dnSections) && line == dnSections[next]:
					next++
				}
			}
			if titles != 1 || !strings.HasPrefix(lines[0], "# ") {
				t.Errorf("%s has %d level-one headings and starts with %q, want one, on the first line", p.path, titles, lines[0])
			}
			if next < len(dnSections) {
				t.Errorf("%s lacks the heading %q, or has it out of order", p.path, dnSections[next])
			}
		})
	}
}

func TestDnPagesFollowTheStyle(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			text := dcRead(t, p.path)
			open := false
			for i, line := range strings.Split(text, "\n") {
				if rest, ok := strings.CutPrefix(line, "```"); ok {
					switch {
					case open && rest != "":
						t.Errorf("%s:%d: a closing fence carries %q", p.path, i+1, rest)
					case !open && !slices.Contains(dnFenceTags, rest):
						t.Errorf("%s:%d: a code block tagged %q, want one of %v", p.path, i+1, rest, dnFenceTags)
					}
					open = !open
				}
				if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
					t.Errorf("%s:%d: HTML in %q", p.path, i+1, line)
				}
			}
			if open {
				t.Errorf("%s ends inside a code block", p.path)
			}
			if m := dnInternalRE.FindString(text); m != "" {
				t.Errorf("%s names %q; a page never names a plan, wave, task or register row", p.path, m)
			}
		})
	}
}

// dnCommand is one command line of a code block tagged sh: the words that name the command and the flags it passes.
type dnCommand struct {
	line  string
	words []string
	flags []string
}

// dnCommands returns the darkpanel command lines of the sh blocks of md. A trailing backslash continues a line; the
// words are those before the first flag or placeholder.
func dnCommands(md string) []dnCommand {
	var cmds []dnCommand
	inSh, in := false, false
	var cur string
	for _, raw := range strings.Split(md, "\n") {
		if rest, ok := strings.CutPrefix(raw, "```"); ok {
			in = !in
			inSh = in && rest == "sh"
			cur = ""
			continue
		}
		if !inSh {
			continue
		}
		line := strings.TrimSpace(raw)
		if cur != "" {
			line = cur + " " + line
		}
		if strings.HasSuffix(line, "\\") {
			cur = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
			continue
		}
		cur = ""
		line = strings.TrimPrefix(strings.TrimPrefix(line, "$ "), "sudo ")
		if !strings.HasPrefix(line, "darkpanel ") {
			continue
		}
		c := dnCommand{line: line}
		for _, f := range strings.Fields(line)[1:] {
			switch {
			case strings.HasPrefix(f, "--"):
				name, _, _ := strings.Cut(strings.TrimPrefix(f, "--"), "=")
				c.flags = append(c.flags, name)
			case len(c.flags) == 0 && !strings.ContainsAny(f[:1], "-<[$'\"|>"):
				c.words = append(c.words, f)
			}
		}
		cmds = append(cmds, c)
	}
	return cmds
}

func TestDnCommandsParse(t *testing.T) {
	md := "text\n```sh\n$ sudo darkpanel panel listener revoke --yes\ndarkpanel derp server deploy \\\n  --host-id <id> --mode=ip\necho hi\nsudo darkpanel npm instance status --local\n```\n```text\ndarkpanel nothing here\n```\n"
	got := dnCommands(md)
	want := []dnCommand{
		{"darkpanel panel listener revoke --yes", []string{"panel", "listener", "revoke"}, []string{"yes"}},
		{"darkpanel derp server deploy --host-id <id> --mode=ip", []string{"derp", "server", "deploy"}, []string{"host-id", "mode"}},
		{"darkpanel npm instance status --local", []string{"npm", "instance", "status"}, []string{"local"}},
	}
	if len(got) != len(want) {
		t.Fatalf("dnCommands found %d commands, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].line != want[i].line || !slices.Equal(got[i].words, want[i].words) || !slices.Equal(got[i].flags, want[i].flags) {
			t.Errorf("command %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDnCommandsExist(t *testing.T) {
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	root, err := cli.NewRoot(cli.Options{Registry: reg, Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			for _, c := range dnCommands(dcRead(t, p.path)) {
				cmd, rest, err := root.Find(c.words)
				if err != nil || len(rest) != 0 || cmd == root {
					t.Errorf("%s: %q names no command of the CLI (found %v, leftover %v, error %v)", p.path, c.line, cmd.CommandPath(), rest, err)
					continue
				}
				for _, f := range c.flags {
					if f != "help" && cmd.Flag(f) == nil {
						t.Errorf("%s: %q passes --%s, which %s does not have", p.path, c.line, f, cmd.CommandPath())
					}
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w9-tmp/18 && TMPDIR=/root/.cache/w9-tmp/18 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestDn' ./internal/docs/`
Expected: FAIL. `TestDnPagesNameEveryAction`, `TestDnPagesStateTheirFacts`, `TestDnPagesLinkOnlyToFiles`, `TestDnPagesHaveTheirSections`, `TestDnPagesFollowTheStyle` and `TestDnCommandsExist` each fail for the four modules with `docs/user/modules/<module>.md: open .../docs/user/modules/<module>.md: no such file or directory`; `TestDnCommandsParse` passes. A build failure here means Task 12's helpers are not in the base: stop and report.

- [ ] **Step 3: Implement**

Write the four Markdown pages. Each page is one `#` title (`DERP servers`, `Nginx Proxy Manager`, `Cloudflare Tunnel`, `Panel exposure`) and then exactly these `##` sections in this order, with these headings: `Overview`, `Before you start`, `Use it in the web UI`, `Use it from the CLI`, `What it changes on the host`, `When something goes wrong`. Write in the second person and the present tense, describe what exists on `main`, and use `example.com`, `203.0.113.10` and `<token-file>` style placeholders. A page links only to files that exist in this task's base: the other three pages of this task (for example `npm.md` from `panel.md`), `../../../README.md`, `../../versioning.md` and `../../api/<module>.md` (the generated reference of that module). It never links to the pages of the firewall, docker, tailscale, tailnet or hostinfo modules or to `docs/user/README.md`, which other tasks write in the same wave; name those modules in plain words.

Every page's `Use it from the CLI` section holds one table with a row for every non-internal action of the module (the names `dcModuleNames` returns: `rg -n 'Name:' internal/modules/<module>` finds most, and the exact list is every action of `registry.Build()` of that module whose `Spec.Internal` is false). Columns: the action name in backticks, its command, one sentence taken from the action's `Summary`, and whether it asks a confirmation (an irreversible action asks the operator to type a phrase; a compensable one can be undone with `darkpanel change undo <id>`). The command is `darkpanel <module> <noun> <verb>` (`cli.CommandPath` in `internal/cli/names.go`). Under the table, explain in plain words, with one `sh` example per page: an action that runs on a host takes `--host <id or name>` and is optional when there is only one host (agent-side actions), an action whose input names `hostId` takes `--host-id`, `--yes` skips the `Apply? [y/N]` question, `--local` sends an agent-side command to the host's own agent socket when the controller cannot be reached, and every command needs the API token set up as the repository `README.md` shows under "Use the CLI with a token". Take the flags of every command you show from `GOTOOLCHAIN=local go run ./cmd/darkpanel <module> <noun> <verb> --help`, which prints help and touches nothing; `TestDnCommandsExist` rejects a command or flag the CLI does not have. The `Use it in the web UI` section names the pages, tabs and buttons as `web/src/modules/<module>/` renders them. `What it changes on the host` lists what the module creates, so the reader knows what `sudo darkpanel uninstall` removes and what a leftover is. `When something goes wrong` is a table of error codes (`internal/core/errcode/errcode.go` has each code's title and status), what each means here and what the reader does.

Page contents, with the source of each fact:

1. `derp.md` (spec section 4.3; `internal/modules/derp/wire.go`, `derp.go`, `servers.go`, `server/server.go`; `web/src/modules/derp/`).
   - Overview: a DERP server is a custom Tailscale relay region that derper serves from one of your hosts; the controller publishes it as a region in the tailnet policy through the tailnet module; derper starts with `-verify-clients`, so it admits only nodes of your tailnet and refuses a client when its own tailscaled does not answer; `verifyClients: false` turns that off and the plan shows a warning. The deploy pins and holds the host's `tailscaled` at the derper version, so the host takes a Tailscale update only when a Darkpanel release changes the derper version.
   - Before you start: the host is on the tailnet (`derp.server.install` is available only then); the tailnet account is connected; the firewall module opens the ports; certificate mode `letsencrypt` (the name's DNS points at the host and port 80 serves the ACME challenge) or `ip` (self-signed for the public address, valid one year, clients pin it as `sha256-raw:` plus the SHA-256 of the certificate); region ids run from 900 to 999.
   - Ports: `80/tcp`, `443/tcp` and `3478/udp`; install refuses with `derp.port_in_use` when any is bound, for example by Nginx Proxy Manager (link `npm.md`).
   - Web UI: `/derp` shows the servers table with its health (`ok`, `degraded`, `broken`, with a reason per row), the deploy and region dialogs and the probe panel (the probes from the controller and from chosen agents, and per node its preferred region against use observed now).
   - Host changes: system user `darkpanel-derp`; `/usr/local/lib/darkpanel/derper` (with `derper.new` and `derper.prev` while the binary is replaced); `/etc/systemd/system/darkpanel-derper.service`; `/var/lib/darkpanel/derp` with its `certs` directory and the key file `derper.key` (mode 0600, kept by every upgrade); three firewall rules labelled `derp`.
   - Errors: `derp.port_in_use`; `derp.not_ready` (publishing a region needs the server's deploy confirmed and `/derp/probe` and STUN reachable from the controller and from at least one agent that is not the DERP host); `derp.region_taken`; `derp.no_fallback` (an upgrade or certificate rotation is refused unless the host has a management path that does not use this region); `derp.verify_failed` (a derper step failed its check, for example the binary update's health check, which rolls derper back to the previous binary by itself within 60 seconds).
   - Certificates: the page and the health warn `degraded` 30 days before an `ip` certificate expires and report `broken` after; `darkpanel derp cert rotate` replaces it, and between its two steps clients holding the old pin cannot use the region.
2. `npm.md` (spec section 6; `internal/modules/npm/wire.go`, `deps.go`, `instance_deploy.go`; `web/src/modules/npm/`).
   - Overview: the instance runs in the container `darkpanel-npm` from the image this release pins, on the network `darkpanel-npm`, with the volumes `darkpanel-npm-data` and `darkpanel-npm-letsencrypt`; ports 80 and 443 are published on all addresses and the admin API on `127.0.0.1:81` only. Deploy creates the NPM administrator with the `acmeEmail` you give, lower-cased, and a random password that is stored sealed and never appears in the container's environment, the agent's logs or the log stream; `darkpanel npm admin reveal-password` shows it once, to an admin.
   - Before you start: Docker 28.0 or later with its raw-table protection loaded (the module reports itself unavailable otherwise); ports 80 and 443 free and no DERP server on the host; `acmeEmail` a bare address on a public domain (an IP literal, a name without a dot and a reserved name are refused as `request.invalid_input`); with the firewall module's Docker integration on, `npm instance deploy-public` opens 80 and 443 as a step you confirm after checking the ports from outside, and without it the ports are already reachable and `npm instance confirm-public` records that you checked.
   - Exposing a container (`npm expose container`, `npm expose remove`): needs a confirmed public instance (`npm.not_public` otherwise); the container must be on a user-defined bridge network, because NPM reaches it by name (`npm.unsupported_target` otherwise: connect the container to a user-defined network); the steps attach NPM to that network, request a certificate and add the proxy host with forced SSL and HSTS; attachments are claim based, so removing one exposure never detaches a network another exposure uses. The container page has an Expose action.
   - Managing NPM: proxy hosts, access lists and certificates through their add, update, remove, request and list actions; `npm certificate remove` and `npm instance remove` are irreversible and ask for a typed phrase. Objects an exposure created are reserved: `npm.reserved` refuses to change them, or to point a proxy host at `127.0.0.1`, `localhost`, the NPM container's own port 81 or the panel network, except through the owning exposure.
   - Admin interface: `npm admin expose` (off by default) publishes NPM's admin UI on a hostname and every page of the web UI then shows the warning "Admin interface exposed"; `npm admin unexpose` removes the proxy host and leaves the certificate, which only `npm certificate remove` deletes.
   - Upgrade: `npm instance upgrade` archives both volumes under `/var/lib/darkpanel/agent/npm-backups` (the last three sets are kept), waits up to 120 seconds for the new container's API and, when it is interrupted, restores the volumes from the verified archive.
   - Web UI: `/hosts/<id>/npm` with the tabs Instance, Proxy hosts, Access lists and Certificates.
   - Errors: `npm.unhealthy`, `npm.not_public`, `npm.unsupported_target`, `npm.reserved`, `npm.setup_failed` (the administrator could not be created: run `darkpanel change undo <id>`, which removes the container, both volumes and the network, then deploy again) and `derp.port_in_use`. An interrupted creation becomes unresolved and nothing settles it by itself: wait for its commit window (2 minutes for proxy hosts and access lists, 10 for certificates) and run `darkpanel change resolve`; a certificate match is marked as not exclusive because NPM may reuse a certificate.
   - Host changes: the container, network and volumes above, the archive directory, and the firewall rules of ports 80 and 443 when the integration is on.
3. `cloudflare.md` (spec section 7; `internal/modules/cloudflare/wire.go`, `connector_deps.go`, `account_connect.go`; `web/src/modules/cloudflare/account-panel.tsx` for the permission wording).
   - Overview: the module manages remotely managed tunnels, a connector container for each tunnel on a host, and public hostnames; no inbound port opens on any host. Tunnels are named `darkpanel-<change id>`. The connector runs as the container `darkpanel-cloudflared-<tunnel id>` from a pinned image digest, reads its token from a file, never from its environment or command line, and the token is write-only through the API.
   - Before you start: the account id and an API token with "Cloudflare Tunnel: Edit" on the account, "Zone Read" and "DNS: Edit" on the zones you use; connecting probes what the token can read and reports write permissions as unverified. Docker on the host that runs the connector.
   - Exclusive management: Cloudflare offers no conflict protection, so you must not edit a tunnel Darkpanel created in the dashboard or with another client. Darkpanel keeps a hash of the configuration it last wrote; a different configuration found before a write is reported as drift (`cloud.conflict`) and nothing is written until you accept it with `cloudflare tunnel adopt`. A write another client makes between Darkpanel's read and its own write is overwritten without trace, and the DNS record delete and the configuration write are read-then-write checks, not compare-and-swap; the window is one API round trip.
   - Public hostnames: `cloudflare hostname add` inserts the rule before the required catch-all `http_status:404`, then creates a proxied CNAME to `<tunnel id>.cfargotunnel.com` with the comment `darkpanel:<change id>`; remove deletes only the record and rule it recorded.
   - Exposing a container (`cloudflare expose container`, `cloudflare expose remove`): the connector joins the container's network; the same user-defined bridge network rule as for Nginx Proxy Manager applies (`cloudflare.unsupported_target`), and `cloudflare.connector_missing` means no connector of that tunnel runs on the host. `cloudflare.reserved` refuses a hostname whose service is the panel gateway, except through the panel exposure (link `panel.md`). The container page has an Expose via tunnel action.
   - Disconnect: `cloudflare account disconnect` is irreversible and deletes the stored token; `cloud.cleanup_pending` means Darkpanel still has cleanup pending on the account, which the refusal lists, and `--abandon-cleanup` gives that cleanup up.
   - Web UI: `/cloudflare` with the pages Account, Tunnels, Connectors and Hostnames.
   - Host changes: the connector container and the token files in `/var/lib/darkpanel/agent/cloudflare-tokens` (one `<tunnel id>.token` each, mode 0400).
   - Errors: `cloud.not_connected`, `cloud.auth_failed`, `cloud.permission_missing`, `cloud.rate_limited`, `cloud.conflict`, `cloud.uncertain_outcome` (a write whose result is unknown: resolve it with `darkpanel change resolve` after its commit window), `cloud.cleanup_pending`, `cloudflare.reserved`, `cloudflare.unsupported_target`, `cloudflare.connector_missing`.
4. `panel.md` (spec section 8; `internal/modules/panel/wire.go`, `expose_add.go`; `web/src/modules/panel/panel-page.tsx`, `banner.tsx`).
   - Overview: `panel exposure add` publishes the Darkpanel web UI itself on a public hostname through Nginx Proxy Manager (`--via npm`, link `npm.md`) or a Cloudflare tunnel (`--via tunnel`, link `cloudflare.md`); login over the exposure needs TOTP like everywhere; every page shows the banner "Panel exposed via <npm|tunnel> at <host>" with a link to remove it; the audit log marks proxied requests and keeps both addresses.
   - Before you start: only the controller's own host is allowed (`panel.wrong_host`); every admin has finished TOTP enrolment (`panel.totp_incomplete`); no exposure exists (`panel.already_exposed`); for `npm` a confirmed public instance runs on that host (`npm.not_public`); for `tunnel` the tunnel id is a lowercase UUID and its connector runs on that host (`cloudflare.connector_missing`); the hostname is lower-case with at least two labels; `--port` is optional, 1024 to 65535, default 9443.
   - What the add does, in order: the network `darkpanel-panel` (bridge `dp-panel`, the highest free /29 of `172.31.255.0/24` that overlaps no Docker network, the gateway at .1 and the proxy at .2); the proxy joins it; the controller opens a listener on the gateway address and the port that admits only the proxy; a certificate (NPM only); the proxy host or tunnel hostname; and last a firewall rule that allows the proxy to the gateway port, the one step you confirm.
   - Removing: `panel exposure remove` first revokes the listener, which is permanent: nothing reopens it, and only a new add exposes the panel again; then it removes the proxy host or hostname, the attachment, the network and the firewall rule. The certificate stays in NPM, deleted only by `npm certificate remove`; repeated add and remove cycles for one hostname count against Let's Encrypt's duplicate-certificate limit. `panel.not_exposed` refuses a removal when nothing is exposed.
   - Break-glass: on the controller's host, `sudo darkpanel panel listener revoke` revokes the listener over the controller socket without the web UI; then run `panel exposure remove` to finish the removal.
   - Web UI: `/hosts/<id>/panel` shows the form (Route, Public hostname, Tunnel id, Port, Review) on the controller's host only, and the exposure with a Remove exposure button once one exists.
   - Host changes: the network `darkpanel-panel`, the interface `dp-panel`, one firewall rule, and the proxy host or tunnel hostname in the proxy module.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w9-tmp/18 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestDn' ./internal/docs/ && GOTOOLCHAIN=local timeout 300 go build ./... && GOTOOLCHAIN=local timeout 300 go vet ./... && GOTOOLCHAIN=local timeout 600 go test -count=1 ./... && GOTOOLCHAIN=local timeout 300 go test -race -count=1 ./internal/docs/ && GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./... && GOTOOLCHAIN=local timeout 300 go mod tidy -diff`
Expected: every `go test` line `ok`, `go vet` and `go mod tidy -diff` print nothing, golangci-lint prints `0 issues.`. Quote the output, then `rm -rf /root/.cache/w9-tmp/18 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/user/modules/derp.md docs/user/modules/npm.md docs/user/modules/cloudflare.md docs/user/modules/panel.md internal/docs/modules_b_test.go
git commit -m "docs(user): add derp, npm, cloudflare, panel pages"
```

### Task 19: docs: module developer guide

**Files:**
- Create: `docs/dev/module-guide.md`
- Test: `internal/docs/guide_test.go`

**Interfaces:**
- Consumes: Contracts "Docs checks" (`dcRead`, `dcMentions`, `dcLinks`, Task 12) and "Documentation paths"; the `make api-docs` target and `TestAdAPIDocsCurrent` of Contracts "API reference" (Task 3); Global Constraints "Documentation style". The test compiles against `internal/core`, `internal/modules`, `internal/registry` and `internal/cli`, so a rename of any identifier the guide teaches breaks the build of `internal/docs`.
- Produces: `docs/dev/module-guide.md`, which Task 24's `TestWcDocsLinks` checks. Test identifiers are prefixed `Dg`/`dg`.

**Items:** 42

**Depends on:** Task 3, Task 12
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 0 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/docs/guide_test.go`. `dgNames` writes each teaching identifier as the Go expression a reader would use (a generic function is instantiated, an interface is a `reflect.TypeFor`), next to the name the guide must mention. The other tests require the files and make targets the guide teaches to exist, every backticked repository path in the guide to exist, every `core.X` the guide uses to be exported by package `core` (read from the source with `go/parser`), the tests the guide names to be declared in the files it names, the web interfaces of `web/src/modules/types.ts` to exist, the eleven sections to appear in order, code fences to be tagged `sh`, `json` or `text`, and the links to resolve.

```go
package docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

const dgGuide = "docs/dev/module-guide.md"

// dgIO is the input and output type the generic names below are instantiated with.
type dgIO struct{}

// dgNames are the identifiers the guide teaches. Each is written as the Go expression the guide's reader would use,
// so renaming or removing one breaks this file's compilation; the guide must also mention it by the name here.
var dgNames = []struct {
	name string
	ref  any
}{
	{"core.DefineRead", core.DefineRead[dgIO, dgIO]},
	{"core.DefineMutating", core.DefineMutating[dgIO, dgIO]},
	{"core.DefineRevertible", core.DefineRevertible[dgIO, dgIO]},
	{"core.DefineComposite", core.DefineComposite[dgIO, dgIO]},
	{"core.DefineTask", core.DefineTask[dgIO, dgIO]},
	{"core.DefineReveal", core.DefineReveal[dgIO, dgIO]},
	{"core.DefineInternalRead", core.DefineInternalRead[dgIO, dgIO]},
	{"core.DefineResource", core.DefineResource[dgIO]},
	{"core.Reader", reflect.TypeFor[core.Reader[dgIO, dgIO]]()},
	{"core.Mutator", reflect.TypeFor[core.Mutator[dgIO, dgIO]]()},
	{"core.Reverter", reflect.TypeFor[core.Reverter[dgIO, dgIO]]()},
	{"core.Composer", reflect.TypeFor[core.Composer[dgIO, dgIO]]()},
	{"core.TaskMutator", reflect.TypeFor[core.TaskMutator[dgIO, dgIO]]()},
	{"core.Observer", reflect.TypeFor[core.Observer]()},
	{"core.Undoer", reflect.TypeFor[core.Undoer]()},
	{"core.Recoverer", reflect.TypeFor[core.Recoverer]()},
	{"core.Uninstaller", reflect.TypeFor[core.Uninstaller]()},
	{"core.Module", reflect.TypeFor[core.Module]()},
	{"core.Manifest", core.Manifest{}},
	{"core.ActionSpec", core.ActionSpec{}},
	{"core.PreflightCheck", core.PreflightCheck{}},
	{"core.OwnedPath", core.OwnedPath{}},
	{"core.OwnedFile", core.OwnedFile},
	{"core.LabelModule", core.LabelModule},
	{"core.ReservedNames", core.ReservedNames},
	{"core.NewRegistry", core.NewRegistry},
	{"modules.All", modules.All},
	{"registry.Build", registry.Build},
	{"cli.CommandPath", cli.CommandPath},
}

// dgPaths are the files the guide teaches, each a path that exists in the repository. A path under
// internal/modules/<id>/ stands for that directory of any module; derp has migrations.
var dgPaths = []string{
	"internal/modules/modules.go",
	"internal/cli/modules.go",
	"web/src/modules/registry.ts",
	"web/src/modules/types.ts",
	"internal/modules/<id>/migrations/",
	"internal/modules/hostinfo/hostinfo.go",
	"docs/adr/0002-module-contract.md",
	"docs/adr/0011-actors-cli-names-and-layout.md",
	"docs/adr/0014-v1-core-extensions.md",
	"CONTRIBUTING.md",
}

var dgTargets = []string{"make schemas", "make api-docs"}

// dgTests are the tests the guide names, with the file each is declared in.
var dgTests = []struct{ name, file string }{
	{"TestEveryActionHasACLICommand", "internal/modules/registry_test.go"},
	{"TestSnapshotsCurrent", "internal/modules/modules_test.go"},
	{"TestAdAPIDocsCurrent", "internal/apidocs/apidocs_test.go"},
}

// dgWebTypes are the interfaces of web/src/modules/types.ts the guide teaches.
var dgWebTypes = []string{"ModuleEntry", "ModuleRouteProps", "ContainerAction", "ContainerRef", "HostCreateExtension", "GlobalPage"}

// dgSections are the level-two headings of the guide, in order.
var dgSections = []string{
	"## What a module is",
	"## Create the module package",
	"## Declare actions",
	"## Declare resources",
	"## Preflight, owned paths and migrations",
	"## Register the module",
	"## Get the CLI for free",
	"## Add the web pages",
	"## Schemas and the API reference",
	"## Test the module",
	"## Rules the registry enforces",
}

var (
	dgCoreRE   = regexp.MustCompile(`\bcore\.([A-Z][A-Za-z0-9]*)`)
	dgPathRE   = regexp.MustCompile("`((?:internal|web|docs|cmd|schemas|scripts|release|test)/[^`\\s]*)`")
	dgFenceTag = []string{"sh", "json", "text"}
)

func TestDgGuideMentionsEveryName(t *testing.T) {
	var names []string
	for _, n := range dgNames {
		if n.ref == nil {
			t.Errorf("%s has no value", n.name)
		}
		names = append(names, n.name)
	}
	dcMentions(t, dgGuide, names...)
}

func TestDgGuideMentionsEveryPathAndTarget(t *testing.T) {
	dcMentions(t, dgGuide, dgPaths...)
	dcMentions(t, dgGuide, dgTargets...)
	root := dcRoot(t)
	for _, p := range dgPaths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(p, "<id>", "derp")))); err != nil {
			t.Errorf("the guide teaches %s, which does not exist: %v", p, err)
		}
	}
	makefile := dcRead(t, "Makefile")
	for _, target := range dgTargets {
		name := strings.TrimPrefix(target, "make ")
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:`).MatchString(makefile) {
			t.Errorf("the guide teaches %q, but the Makefile has no target %s", target, name)
		}
	}
}

func TestDgEveryPathTheGuideNamesExists(t *testing.T) {
	root := dcRoot(t)
	found := dgPathRE.FindAllStringSubmatch(dcRead(t, dgGuide), -1)
	if len(found) == 0 {
		t.Fatalf("%s names no path in backticks", dgGuide)
	}
	for _, m := range found {
		p := m[1]
		if strings.HasPrefix(p, "internal/modules/<id>/") {
			p = strings.ReplaceAll(p, "<id>", "derp")
		}
		if strings.ContainsAny(p, "<>*{}") || strings.HasSuffix(p, "...") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimRight(p, ".,;:")))); err != nil {
			t.Errorf("the guide names %s, which does not exist", m[1])
		}
	}
}

// dgExported returns the exported top-level names of the non-test Go files of the package in dir.
func dgExported(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = s.Name.IsExported()
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = n.IsExported()
						}
					}
				}
			}
		}
	}
	return names
}

func TestDgEveryCoreNameTheGuideUsesExists(t *testing.T) {
	exported := dgExported(t, filepath.Join(dcRoot(t), "internal", "core"))
	uses := dgCoreRE.FindAllStringSubmatch(dcRead(t, dgGuide), -1)
	if len(uses) == 0 {
		t.Fatalf("%s uses no core name", dgGuide)
	}
	for _, m := range uses {
		if !exported[m[1]] {
			t.Errorf("the guide uses core.%s, which package core does not export", m[1])
		}
	}
}

func TestDgGuideNamesItsTestsAndTheyExist(t *testing.T) {
	for _, tc := range dgTests {
		dcMentions(t, dgGuide, tc.name)
		if !strings.Contains(dcRead(t, tc.file), "func "+tc.name+"(") {
			t.Errorf("%s does not declare %s, which the guide names", tc.file, tc.name)
		}
	}
}

func TestDgGuideTeachesTheWebTypes(t *testing.T) {
	dcMentions(t, dgGuide, dgWebTypes...)
	dcMentions(t, dgGuide, "moduleRegistry")
	types := dcRead(t, "web/src/modules/types.ts")
	for _, name := range dgWebTypes {
		if !strings.Contains(types, "export interface "+name+" ") {
			t.Errorf("web/src/modules/types.ts does not export the interface %s, which the guide teaches", name)
		}
	}
	if !strings.Contains(dcRead(t, "web/src/modules/registry.ts"), "export const moduleRegistry") {
		t.Error("web/src/modules/registry.ts does not export moduleRegistry, which the guide teaches")
	}
}

func TestDgGuideHasItsSections(t *testing.T) {
	lines := strings.Split(dcRead(t, dgGuide), "\n")
	titles, next := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "# "):
			titles++
		case next < len(dgSections) && line == dgSections[next]:
			next++
		}
	}
	if titles != 1 || !strings.HasPrefix(lines[0], "# ") {
		t.Errorf("%s has %d level-one headings and starts with %q, want one, on the first line", dgGuide, titles, lines[0])
	}
	if next < len(dgSections) {
		t.Errorf("%s lacks the heading %q, or has it out of order", dgGuide, dgSections[next])
	}
}

func TestDgGuideFollowsTheStyle(t *testing.T) {
	text := dcRead(t, dgGuide)
	open := false
	for i, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, "```")
		if !ok {
			continue
		}
		switch {
		case open && rest != "":
			t.Errorf("%s:%d: a closing fence carries %q", dgGuide, i+1, rest)
		case !open && !slices.Contains(dgFenceTag, rest):
			t.Errorf("%s:%d: a code block tagged %q, want one of %v", dgGuide, i+1, rest, dgFenceTag)
		}
		open = !open
	}
	if open {
		t.Errorf("%s ends inside a code block", dgGuide)
	}
	if m := regexp.MustCompile(`(?i)\b(wave [0-9]|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9])`).FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row", dgGuide, m)
	}
}

func TestDgGuideLinksOnlyToFiles(t *testing.T) { dcLinks(t, dgGuide) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w9-tmp/19 && TMPDIR=/root/.cache/w9-tmp/19 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestDg' ./internal/docs/`
Expected: the package compiles and every `TestDg` test fails with `docs/dev/module-guide.md: open .../docs/dev/module-guide.md: no such file or directory`. A build failure naming an identifier of `dgNames` means that identifier no longer exists: stop and report. A build failure naming `dcRead` means Task 12's helpers are missing: stop and report.

- [ ] **Step 3: Implement**

Write `docs/dev/module-guide.md`: one `#` title (`Module developer guide`) and then exactly these `##` sections in this order, with these headings: `What a module is`, `Create the module package`, `Declare actions`, `Declare resources`, `Preflight, owned paths and migrations`, `Register the module`, `Get the CLI for free`, `Add the web pages`, `Schemas and the API reference`, `Test the module`, `Rules the registry enforces`. Write for a Go and React developer who has not seen the repository, in the second person and the present tense, in English. Code blocks are tagged `text` (Go and TypeScript snippets included), `sh` or `json`; the documentation style of Global Constraints applies, so the guide never names a plan, wave, task or register row. Name every identifier of `dgNames` exactly as the test writes it (`core.DefineRead`, not `DefineRead`), and every path of `dgPaths`, the two make targets (`make schemas`, `make api-docs`), the three tests of `dgTests` and the interfaces of `dgWebTypes` plus `moduleRegistry`. Every `core.X` the guide writes must exist in package `core`, and every repository path in backticks must exist (a path under `internal/modules/<id>/` is checked with `derp` as the id; do not write other placeholder paths in backticks). Link, with relative links, to `../adr/0002-module-contract.md`, `../adr/0008-test-harness.md`, `../adr/0009-host-execution-safety.md`, `../adr/0011-actors-cli-names-and-layout.md`, `../adr/0014-v1-core-extensions.md`, `../../CONTRIBUTING.md` and `../api/README.md` (the generated API reference); `TestDgGuideLinksOnlyToFiles` fails on a link to a file that does not exist.

Section facts, with the files to read for each:

1. What a module is (`docs/adr/0002-module-contract.md`, `internal/core/contract.go` `Module` and `Manifest`, `internal/core/registry.go` `ReservedNames`): a module is a Go package under `internal/modules/<id>` whose value implements `core.Module` (one method, `Manifest() core.Manifest`); modules are compiled in, there are no runtime plugins; the id matches `^[a-z][a-z0-9]*$` and is not one of `core.ReservedNames`; a module runs on the controller, the agent or both (`core.SideController`, `core.SideAgent`); a module never changes another module's resources directly, it calls the owning module's actions, and a composite is how one module sequences another's (ADR 0014).
2. Create the module package (`internal/modules/hostinfo/hostinfo.go` and `hostinfo_test.go`, the smallest module): show its shape with the file names, the `ID` constant, `Module()` and `Manifest()` (id, `Version: 1`, `DefaultEnabled: true`, one resource through `core.DefineResource`, one `core.PreflightCheck`); explain that the module reads the host through a small `Source` struct that tests fill with fixtures (`fstest.MapFS`), which is why its tests never touch the machine they run on.
3. Declare actions (`internal/core/define.go`, `internal/core/contract.go` `ActionSpec` and the handler interfaces, `internal/core/registry.go` `checkAction`, `checkLifecycle`, `checkGuardsAndScope`, `checkReveal`): the name `<module>.<noun>.<verb>`; `core.ActionSpec` fields; a table of the constructors with the handler each takes and the class it sets: `core.DefineRead` with `core.Reader`, `core.DefineMutating` with `core.Mutator` (class compensable or irreversible; Plan, Apply, Verify), `core.DefineRevertible` with `core.Reverter` (adds Snapshot and Revert; agent side; `RepairedBy`), `core.DefineComposite` with `core.Composer` (Steps and Output; controller side; `Unwind` reverse or forward; the lock `composite:<module>` is set for you), `core.DefineTask` with `core.TaskMutator` (the Task handler runs in a transient unit), `core.DefineReveal` (one stored secret returned once, admin scope) and `core.DefineInternalRead` (dispatched only by the controller; no CLI command). A plan never changes the host and Verify only reads it. A handler that also implements `core.Observer`, `core.Undoer` or `core.Recoverer` gets the matching lifecycle handler; a compensable action declares exactly one of an undo handler, `CompensatedBy` or `ForwardOnly`. Input and output are JSON structs with `jsonschema` tags; a secret field carries the `secret:"true"` tag; a pointer field of an output carries `jsonschema:"nullable"` (`TestPointerOutputsAreNullable` in `internal/modules/registry_test.go` fails otherwise). Use a small action of the repository as the worked example (for instance `npm.instance.status` in `internal/modules/npm/instance_status.go`) and show where its Go code, its input type and its schema snapshot live.
4. Declare resources: `core.DefineResource` makes `<module>.<resource>`, a read without input at schema version 1; its command is `darkpanel <module> <resource> get`.
5. Preflight, owned paths and migrations (`internal/core/contract.go` `PreflightCheck`, `OwnedPath` and the `Owned*` kinds, `LabelModule`, `Uninstaller`, `Manifest.Migrations`; link `../adr/0009-host-execution-safety.md` for the rule): a failed preflight marks the module unavailable with the error text as the reason; every file, block, directory, user, unit, sysctl key or service directory the module creates on a host is declared as a `core.OwnedPath` so uninstall removes it; Docker objects carry the label `darkpanel.module=<id>` (`core.LabelModule`); an optional `core.Uninstaller` adds uninstall options; migrations live in `internal/modules/<id>/migrations/` in one directory per side (`agent`, `controller`) of numbered `.sql` files, embedded with `//go:embed` and returned from `Manifest()` as an `fs.FS` per side (see `internal/modules/derp/store.go`).
6. Register the module (`internal/modules/modules.go`, `internal/registry/registry.go`): adding a module is one line in `modules.All`; `registry.Build` builds the one registry every component uses (the core module first, then `modules.All()`), and `core.NewRegistry` validates every rule of the last section when the process starts.
7. Get the CLI for free (`internal/cli/names.go`, `internal/cli/modules.go`, `internal/cli/input.go` `reservedFlags`, `internal/modules/registry_test.go`): `cli.CommandPath` turns `<module>.<noun>.<verb>` into `darkpanel <module> <noun> <verb>` and a resource into `darkpanel <module> <resource> get`; flags are generated from the input schema in kebab case; the names `url`, `socket`, `dev`, `help`, `json`, `host`, `local`, `input`, `yes`, `confirmation`, `confirm` and `override` are taken, so no input field may use them; a secret field has no flag and is prompted for; `TestEveryActionHasACLICommand` proves every non-internal action and resource has a command; hand-written commands are the exception and go in `internal/cli/<module>.go` with an entry in the map of `internal/cli/modules.go`.
8. Add the web pages (`web/src/modules/types.ts`, `web/src/modules/registry.ts`, `web/src/modules/hostinfo/` for the smallest module, `web/src/components/change-dialog.tsx`): one folder `web/src/modules/<id>/` and one entry in `moduleRegistry`; the fields of `ModuleEntry` (`id`, `label`, `icon`, `routes` for host pages, `global` for a page that belongs to no host, `containerActions`, `hostCreate`); what `ModuleRouteProps`, `ContainerAction`, `ContainerRef`, `HostCreateExtension` and `GlobalPage` carry; every mutating action goes through the change dialog; run `make lint-web test-web`.
9. Schemas and the API reference (`CONTRIBUTING.md` "Schemas", `docs/adr/0002-module-contract.md` compatibility rules, `Makefile`): `make schemas` regenerates the snapshots under `schemas/` and `TestSnapshotsCurrent` (`internal/modules/modules_test.go`) fails while one is stale; the compatibility rules (new optional input fields and new output fields are allowed in a published version; removing or renaming a field, changing a type or a constraint, or adding a required input field needs a new action version); `make api-docs` regenerates `docs/api/` from the registry and `TestAdAPIDocsCurrent` (`internal/apidocs/apidocs_test.go`) fails while it is stale; commit both results.
10. Test the module (`CONTRIBUTING.md` "Make targets", `internal/core/coretest`): unit tests run with fakes (a fake clock from `coretest`, a fake `core.Runner`, fixture file systems), never against the machine; behaviour that needs a real host runs in the VM harness (`test/harness`, `../adr/0008-test-harness.md`); name `make test-go`, `make lint-go`, `make lint-web`, `make test-web` and `make check`.
11. Rules the registry enforces (`internal/core/registry.go`, `internal/modules/registry_test.go` `TestRegistrationRules`): as a list, each rule one sentence: module id pattern and reserved names, action and resource name patterns and prefix, unique name and version, a compensating action must be registered, a break-glass action is controller-side, a revertible action is agent-side and has `RepairedBy`, `Internal` is for read actions, `Reveal` needs admin scope, an `ApplyTimeout` above 45 seconds needs `LongRunning`, owned-path kinds and their fields, and a migration directory per side that has tables. Verify each against `registry.go` before you write it, and drop a rule the code does not enforce.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w9-tmp/19 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestDg' ./internal/docs/ && GOTOOLCHAIN=local timeout 300 go build ./... && GOTOOLCHAIN=local timeout 300 go vet ./... && GOTOOLCHAIN=local timeout 600 go test -count=1 ./... && GOTOOLCHAIN=local timeout 300 go test -race -count=1 ./internal/docs/ && GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./... && GOTOOLCHAIN=local timeout 300 go mod tidy -diff`
Expected: every `go test` line `ok`, `go vet` and `go mod tidy -diff` print nothing, golangci-lint prints `0 issues.`. Quote the output, then `rm -rf /root/.cache/w9-tmp/19 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add docs/dev/module-guide.md internal/docs/guide_test.go
git commit -m "docs(dev): add the module developer guide"
```

### Task 20: web a11y: container actions and expose flows

**Files:**
- Create: `web/src/modules/docker/containers-tab.a11y.test.tsx`
- Create: `web/src/modules/docker/container-page.a11y.test.tsx`
- Create: `web/src/modules/docker/docker-change.a11y.test.tsx`
- Create: `web/src/modules/firewall/container-actions.a11y.test.tsx`
- Create: `web/src/modules/npm/container-actions.a11y.test.tsx`
- Create: `web/src/modules/npm/npm-change.a11y.test.tsx`
- Create: `web/src/modules/cloudflare/container-actions.a11y.test.tsx`
- Create: `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`
- Create: `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`
- Create: `web/src/modules/cloudflare/global.a11y.test.tsx`
- Create: `web/src/modules/derp/global.a11y.test.tsx`
- Create: `web/src/modules/tailnet/global.a11y.test.tsx`
- Modify: `web/src/modules/docker/containers-tab.tsx`
- Modify: `web/src/modules/firewall/container-actions.tsx`
- Modify: `web/src/modules/tailnet/global.tsx`
- Modify: `web/src/modules/derp/global.tsx`
- Modify: `web/src/modules/cloudflare/global.tsx`
- Modify only when its test finds a defect: `web/src/modules/docker/container-page.tsx`, `web/src/modules/docker/docker-change.tsx`, `web/src/modules/npm/container-actions.tsx`, `web/src/modules/npm/npm-change.tsx`, `web/src/modules/cloudflare/container-actions.tsx`, `web/src/modules/cloudflare/expose-dialog.tsx`, `web/src/modules/cloudflare/cloudflare-change.tsx`

**Interfaces:**
- Consumes: Contracts "Accessibility helpers" (`expectNoAxeViolations`, `tabSequence`, `focusedName` from `web/src/test/a11y.ts`) and "Page title" (`usePageTitle` from `web/src/lib/page-title.ts`), both Task 2; `renderWithProviders` and `stubFetch` from `web/src/test/utils.tsx`; the fixtures of each module (`dkContainer`, `dkDetail`, `dkNetwork`, `fwDocker`, `fwBinding`, `npInstance`, `cfTunnel`, `cfConnector`).
- Produces: the `usePageTitle` calls for the global pages of the tailnet, derp and cloudflare modules (each page's `label`, as Contracts "Page title" lists), and the rule that focus never falls to the page body after a container action changes the control that opened its dialog.

**Items:** 41

**Depends on:** Task 2, Task 14
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

Twelve test files, one beside each component. Every dialog test follows the same pattern: open the dialog from its trigger, check axe on the dialog element (the dialog is portalled out of the test's container, and axe's page-level `region` rule does not apply to an element), press Tab more often than the dialog has controls and require focus to stay inside it, press Escape, wait for the dialog and then for the list or form to be read again, and require focus to be on the trigger found again by role and name (never a held element: React may have replaced it). `focusedName()` reads the accessible name of the focused element.

A draft of these tests was run against a scratch copy of the tree at `3c5e837`, with stand-ins for Task 2's helpers; the red results in Step 2 are what that run gave.

`containers-tab.a11y.test.tsx` covers the table, the empty state and the read error with axe; the dialog's planning, plan, plan-error, typed-confirmation and applied states with axe; the Tab order through the row actions (`web`, `Stop web`, `Restart web`, `db`, `Start db`, `Remove db`: the disabled `Remove web` is skipped); the trap and the focus return for all four actions after the table is read again; and the two rows whose trigger no longer exists after the change applies.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler, type StubResponse } from "@/test/utils"
import { ContainersTab } from "./containers-tab"
import { dkContainer } from "./fixtures"
import type { DockerContainer } from "./types"

const CA_LIST = "GET /api/v1/hosts/h1/resources/docker/containers"
const CA_ACTIONS = ["docker.container.start", "docker.container.stop", "docker.container.restart", "docker.container.remove"]
const CA_DONE = { id: "ch1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }
const CA_BUSY = { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the container is busy", code: "docker.busy" } }

function caPlan(call: FetchCall): StubResponse {
  const action = call.path.split("/")[6] ?? ""
  const confirmation = action === "docker.container.remove" ? "db" : null
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

const caWeb = dkContainer({ id: "c1", name: "web", state: "running", status: "Up 2 hours", owner: undefined })
const caDb = dkContainer({ id: "c2", name: "db", image: "postgres:17", state: "exited", status: "Exited (0) 3 days ago", ports: [], owner: undefined, usage: undefined })

// caRender serves whatever list read() returns at the time of each request, so a test changes the
// host's containers between the apply and the refetch that follows the dialog's close.
function caRender(read: () => DockerContainer[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CA_LIST]: () => ({ body: { containers: read() } }),
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    "GET /api/v1/changes/ch1": { body: CA_DONE },
    ...Object.fromEntries(CA_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/plan`, caPlan])),
    ...Object.fromEntries(CA_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/apply`, { body: CA_DONE }])),
    ...routes,
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<ContainersTab hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker" },
  )
  return { listReads: () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/docker/containers").length, ...result }
}

async function caApply(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement) {
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
}

const caViews: { state: string; containers: DockerContainer[]; routes: Record<string, StubHandler>; ready: () => Promise<unknown> }[] = [
  { state: "the table", containers: [caWeb, caDb], routes: {}, ready: () => screen.findByRole("table", { name: "Docker containers" }) },
  { state: "the empty state", containers: [], routes: {}, ready: () => screen.findByText("No containers") },
  { state: "the read error", containers: [], routes: { [CA_LIST]: CA_BUSY }, ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ containers, routes, ready }) => {
  const { container } = caRender(() => containers, routes)
  await ready()
  await expectNoAxeViolations(container)
})

const caDialogs: { state: string; label: string; routes: Record<string, StubHandler>; ready: string | null }[] = [
  { state: "planning", label: "Restart web", routes: { "POST /api/v1/hosts/h1/actions/docker.container.restart/v1/plan": () => new Promise<StubResponse>(() => {}) }, ready: "Planning the change" },
  { state: "the plan", label: "Restart web", routes: {}, ready: "Planned changes" },
  { state: "a plan error", label: "Stop web", routes: { "POST /api/v1/hosts/h1/actions/docker.container.stop/v1/plan": CA_BUSY }, ready: null },
  { state: "the typed confirmation", label: "Remove db", routes: {}, ready: "Type db to confirm" },
]

test.each(caDialogs)("the dialog in $state has no axe violations", async ({ label, routes, ready }) => {
  caRender(() => [caWeb, caDb], routes)
  await userEvent.setup().click(await screen.findByRole("button", { name: label }))
  const dialog = await screen.findByRole("dialog", { name: label })
  if (ready === null) await within(dialog).findByText("the container is busy")
  else await within(dialog).findByLabelText(ready)
  await expectNoAxeViolations(dialog)
})

test("the dialog of an applied change has no axe violations", async () => {
  caRender(() => [caWeb, caDb])
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Restart web" }))
  const dialog = await screen.findByRole("dialog", { name: "Restart web" })
  await caApply(user, dialog)
  await expectNoAxeViolations(dialog)
})

test("Tab reaches every enabled row action, named for its container, in row order", async () => {
  caRender(() => [caWeb, caDb])
  await screen.findByRole("table", { name: "Docker containers" })
  expect(await tabSequence(userEvent.setup(), 6)).toEqual(["web", "Stop web", "Restart web", "db", "Start db", "Remove db"])
})

test.each(["Stop web", "Restart web", "Start db", "Remove db"])(
  "%s opens a dialog that keeps Tab inside it and Escape gives focus back to the action after the table re-renders",
  async (label) => {
    const { listReads } = caRender(() => [caWeb, caDb])
    const user = userEvent.setup()
    ;(await screen.findByRole("button", { name: label })).focus()
    await user.keyboard("{Enter}")
    const dialog = await screen.findByRole("dialog", { name: label })
    await within(dialog).findByLabelText("Planned changes")
    expect(dialog.contains(document.activeElement)).toBe(true)
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
    const before = listReads()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(listReads()).toBeGreaterThan(before))
    expect(document.activeElement).toBe(screen.getByRole("button", { name: label }))
  },
)

test("when an applied Stop leaves the row without that action, focus moves to the row's first enabled action", async () => {
  let running = true
  caRender(() => [running ? caWeb : { ...caWeb, state: "exited", status: "Exited (0) 1 second ago" }, caDb])
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Stop web" }))
  const dialog = await screen.findByRole("dialog", { name: "Stop web" })
  running = false
  await caApply(user, dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(focusedName()).toBe("Start web"))
})

test("when an applied Remove deletes the row, focus moves to the containers group", async () => {
  let rows = [caWeb, caDb]
  caRender(() => rows)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Remove db" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove db" })
  await user.type(await within(dialog).findByLabelText("Type db to confirm"), "db")
  rows = [caWeb]
  await caApply(user, dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(screen.queryByRole("button", { name: "Remove db" })).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("group", { name: "Containers" })))
})
```

`container-page.a11y.test.tsx` covers the container page's loading, ready, no-ports, firewall-missing, firewall-unavailable and three read-error states, and the Tab order into the module action slot, with the registry replaced by one action.

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import type { Host, ModuleState } from "@/api/types"
import type { ExposureView } from "@/modules/firewall/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { dkDetail } from "./fixtures"
import DockerRoutes from "./routes"

// The registry is replaced by one entry with one action, so this file tests the page's own markup and the
// slot's place in the keyboard order, not the real modules' forms (each has its own file).
vi.mock("@/modules/registry", async () => {
  const { lazy } = await import("react")
  const { Server } = await import("lucide-react")
  return {
    moduleRegistry: [
      {
        id: "alpha",
        label: "Alpha",
        icon: Server,
        containerActions: [{ id: "alpha.probe", label: "Alpha probe", render: lazy(async () => ({ default: () => <button type="button">Alpha action</button> })) }],
      },
    ],
  }
})
vi.mock("./logs-panel", () => ({ LogsPanel: () => <p>logs</p> }))
vi.mock("./stats-panel", () => ({ StatsPanel: () => <p>stats</p> }))

const CP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const CP_INSPECT = "POST /api/v1/hosts/h1/actions/docker.container.inspect/v1/read"
const CP_EXPOSURE = "GET /api/v1/hosts/h1/resources/firewall/exposure"
const CP_DOWN = { status: 500, body: { type: "about:blank", title: "Error", status: 500, detail: "the controller failed", code: "internal" } }

function cpState(id: string, enabled: boolean, available = true, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

function cpHost(modules: ModuleState[]): Host {
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules, ingestion: null }
}

const cpPorts = [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" as const }]
const cpOn = [cpState("docker", true), cpState("alpha", true), cpState("firewall", true)]
const cpExposure: ExposureView = {
  rows: [{ proto: "tcp", addr: "0.0.0.0", port: 8080, family: "v4", container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false }, classes: [{ class: "internet", verdict: "open" }] }],
}

function cpRender(routes: Record<string, StubHandler> = {}, modules: ModuleState[] = cpOn, ports: typeof cpPorts = cpPorts) {
  stubFetch({
    [CP_HOSTS]: { body: { output: { hosts: [cpHost(modules)] } } },
    [CP_INSPECT]: { body: { output: dkDetail({ id: "c1", name: "/web", running: true, ports }) } },
    [CP_EXPOSURE]: { body: cpExposure },
    ...routes,
  })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<DockerRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker/containers/web" },
  )
}

const cpViews: { state: string; routes: Record<string, StubHandler>; modules: ModuleState[]; ports: typeof cpPorts; ready: () => Promise<unknown> }[] = [
  { state: "the page with its exposure and its action", routes: {}, modules: cpOn, ports: cpPorts, ready: () => screen.findByRole("list", { name: "Published ports" }) },
  { state: "a container that publishes no ports", routes: {}, modules: cpOn, ports: [], ready: () => screen.findByText("This container publishes no ports.") },
  { state: "a host without the firewall module", routes: {}, modules: [cpState("docker", true), cpState("alpha", true)], ports: cpPorts, ready: () => screen.findByText("The firewall module is not enabled on this host.") },
  {
    state: "an unavailable firewall module",
    routes: {},
    modules: [cpState("docker", true), cpState("alpha", true), cpState("firewall", true, false, "ufw is not installed")],
    ports: cpPorts,
    ready: () => screen.findByText("The firewall module is unavailable: ufw is not installed."),
  },
  { state: "a failed exposure read", routes: { [CP_EXPOSURE]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findByText("the controller failed") },
  { state: "a failed container read", routes: { [CP_INSPECT]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findByRole("alert") },
  { state: "a failed host list", routes: { [CP_HOSTS]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findAllByRole("alert") },
]

test.each(cpViews)("$state has no axe violations", async ({ routes, modules, ports, ready }) => {
  const { container } = cpRender(routes, modules, ports)
  await ready()
  await expectNoAxeViolations(container)
})

test("the loading state has no axe violations", async () => {
  const { container } = cpRender({ [CP_INSPECT]: () => new Promise(() => {}) })
  await screen.findByLabelText("Loading the container")
  await expectNoAxeViolations(container)
})

test("Tab reaches the way back first and the module's action next, each by its name", async () => {
  cpRender()
  await screen.findByRole("button", { name: "Alpha action" })
  expect(await tabSequence(userEvent.setup(), 2)).toEqual(["Back to Docker", "Alpha action"])
})
```

`docker-change.a11y.test.tsx` covers `DockerChange` in six states, through a harness that owns the trigger.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState } from "react"
import { expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { DockerChange } from "./docker-change"

const DA_ACTION = "docker.compose.down"
const DA_BASE = "/api/v1/hosts/h1"
const DA_PLAN_PATH = `POST ${DA_BASE}/actions/${DA_ACTION}/v1/plan`
const DA_DONE = { id: "ch1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function daPlan(confirmation: string | null): StubResponse {
  return { body: { planId: "p1", hostId: "h1", action: DA_ACTION, version: 1, inputHash: "h", diff: [{ op: "-", text: "project blog" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

// DaHarness is the trigger a page would own: DockerChange stays mounted and opens while its input is set.
function DaHarness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open change
      </button>
      <DockerChange hostId="h1" action={DA_ACTION} title="Take blog down" input={open ? { project: "blog" } : null} onClose={() => setOpen(false)} />
    </>
  )
}

const daStates: { state: string; routes: Record<string, StubHandler>; ready: (dialog: HTMLElement) => Promise<unknown>; apply?: string }[] = [
  { state: "planning", routes: { [DA_PLAN_PATH]: () => new Promise<StubResponse>(() => {}) }, ready: (d) => within(d).findByLabelText("Planning the change") },
  { state: "the plan", routes: {}, ready: (d) => within(d).findByLabelText("Planned changes") },
  {
    state: "a plan error",
    routes: { [DA_PLAN_PATH]: { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the project is busy", code: "docker.busy" } } },
    ready: (d) => within(d).findByText("the project is busy"),
  },
  { state: "the typed confirmation", routes: { [DA_PLAN_PATH]: daPlan("blog") }, ready: (d) => within(d).findByLabelText("Type blog to confirm") },
  { state: "the applied change", routes: {}, ready: (d) => within(d).findByText("completed"), apply: "go" },
]

test.each(daStates)("$state has no axe violations, keeps Tab inside the dialog and gives focus back to its trigger", async ({ routes, ready, apply }) => {
  stubFetch({
    [`GET ${DA_BASE}/modules/docker/availability`]: { body: { module: "docker", available: true, reason: null, actions: [] } },
    [DA_PLAN_PATH]: daPlan(null),
    [`POST ${DA_BASE}/actions/${DA_ACTION}/v1/apply`]: { body: DA_DONE },
    "GET /api/v1/changes/ch1": { body: DA_DONE },
    ...routes,
  })
  renderWithProviders(<DaHarness />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: "Take blog down" })
  if (apply !== undefined) {
    const button = await within(dialog).findByRole("button", { name: "Apply" })
    await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false))
    await user.click(button)
  }
  await ready(dialog)
  await expectNoAxeViolations(dialog)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open change" }))
})

test("a module the host reports unavailable shows why and has no axe violations", async () => {
  stubFetch({
    [`GET ${DA_BASE}/modules/docker/availability`]: { body: { module: "docker", available: false, reason: "the Docker socket is missing", actions: [] } },
    [DA_PLAN_PATH]: daPlan(null),
  })
  renderWithProviders(<DaHarness />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: "Take blog down" })
  await within(dialog).findByText("the Docker socket is missing")
  await expectNoAxeViolations(dialog)
})
```

`firewall/container-actions.a11y.test.tsx` covers the Allow and Deny forms in five states and the failed read, the plan and pending dialogs, the Tab order of the form, the trap and focus return, and the Deny that leaves nothing to deny.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AllowAction, DenyAction } from "./container-actions"
import { fwBinding, fwDocker } from "./fixtures"
import type { DockerStatus } from "./types"

const CA_DOCKER = "GET /api/v1/hosts/h1/resources/firewall/docker"
const CA_PENDING = { id: "ch1", state: "pending", class: "revertible", error: null, output: null, revertDeadline: "2099-01-01T00:00:00Z" }

const caWebPort = { privatePort: 80, protocol: "tcp" as const, bindings: [{ hostIp: "0.0.0.0", hostPort: 8080 }] }
const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [caWebPort] }

// caDocker is a healthy, supported integration whatever fwDocker's defaults are.
function caDocker(over: Partial<DockerStatus> = {}): DockerStatus {
  return fwDocker({ available: true, supported: true, unsupported: [], enabled: true, bindings: [], ...over })
}

function caAllowedWeb(allowed: boolean): DockerStatus {
  return caDocker({ bindings: [fwBinding({ binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containers: ["web"], allowed: allowed ? ["any"] : [], denied: [] })] })
}

function caRender(kind: "allow" | "deny", read: () => DockerStatus, container: ContainerRef = caContainer, routes: Record<string, StubHandler> = {}) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
  }
  const calls = stubFetch({
    [CA_DOCKER]: () => ({ body: read() }),
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.container.allow/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.container.deny/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.container.allow/v1/apply": { body: CA_PENDING },
    "POST /api/v1/hosts/h1/actions/firewall.container.deny/v1/apply": { body: CA_PENDING },
    "GET /api/v1/changes/ch1": { body: CA_PENDING },
    "POST /api/v1/changes/ch1/confirm": { body: { ...CA_PENDING, state: "completed", revertDeadline: null } },
    ...routes,
  })
  const view = kind === "allow" ? <AllowAction container={container} onDone={vi.fn()} /> : <DenyAction container={container} onDone={vi.fn()} />
  return { dockerReads: () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/firewall/docker").length, ...renderWithProviders(view) }
}

async function caPlan(user: ReturnType<typeof userEvent.setup>, kind: "Allow" | "Deny") {
  const form = await screen.findByRole("form", { name: kind })
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  return dialog
}

test.each([
  { state: "the Allow form", kind: "allow" as const, docker: caAllowedWeb(false), container: caContainer, form: "Allow" },
  { state: "the Deny form", kind: "deny" as const, docker: caAllowedWeb(true), container: caContainer, form: "Deny" },
  { state: "an Allow form for a container without published ports", kind: "allow" as const, docker: caDocker(), container: { ...caContainer, ports: [] }, form: "Allow" },
  { state: "a Deny form with no allowed binding", kind: "deny" as const, docker: caDocker(), container: caContainer, form: "Deny" },
  { state: "an unsupported integration", kind: "allow" as const, docker: caDocker({ supported: false, unsupported: ["Docker 27 is older than 28.0"] }), container: caContainer, form: "Allow" },
])("$state has no axe violations", async ({ kind, docker, container, form }) => {
  const { container: root } = caRender(kind, () => docker, container)
  await screen.findByRole("form", { name: form })
  await expectNoAxeViolations(root)
})

test("a failed read of the integration has no axe violations", async () => {
  const { container } = caRender("allow", () => caDocker(), caContainer, {
    [CA_DOCKER]: { status: 500, body: { type: "about:blank", title: "Error", status: 500, detail: "the agent is unreachable", code: "internal" } },
  })
  await screen.findByRole("alert")
  await expectNoAxeViolations(container)
})

test("the plan dialog and the pending change dialog have no axe violations", async () => {
  caRender("allow", () => caAllowedWeb(false))
  const user = userEvent.setup()
  const dialog = await caPlan(user, "Allow")
  await expectNoAxeViolations(dialog)
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByRole("timer")
  await expectNoAxeViolations(dialog)
})

test("Tab goes from the binding to the source to Plan, each with a visible name", async () => {
  caRender("allow", () => caAllowedWeb(false))
  await screen.findByRole("form", { name: "Allow" })
  expect(await tabSequence(userEvent.setup(), 3)).toEqual(["all:8080/tcp", "From", "Plan"])
})

test.each(["allow", "deny"] as const)("the %s dialog keeps Tab inside it and Escape gives focus back to Plan after the form re-renders", async (kind) => {
  const { dockerReads } = caRender(kind, () => caAllowedWeb(true))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: kind === "allow" ? "Allow" : "Deny" })
  if (kind === "deny") await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  const before = dockerReads()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(dockerReads()).toBeGreaterThan(before))
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: kind === "allow" ? "Allow" : "Deny" })).getByRole("button", { name: "Plan" }))
})

test("when a confirmed Deny leaves nothing to deny, the Plan button is disabled and focus moves to the form", async () => {
  let allowed = true
  caRender("deny", () => caAllowedWeb(allowed))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deny" })
  await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  allowed = false
  await user.click(apply)
  await user.click(await within(dialog).findByRole("button", { name: "Confirm" }))
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect((await screen.findByText("web has no allowed binding. Allow one first.")).textContent).toBe("web has no allowed binding. Allow one first.")
  expect(within(screen.getByRole("form", { name: "Deny" })).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("form", { name: "Deny" })))
  expect(focusedName()).toBe("Deny")
})
```

`npm/container-actions.a11y.test.tsx` covers the Expose form and the states that gate it, the invalid port, the dialogs, the Tab order and the focus return.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import { npInstance } from "./fixtures"

const CA_HOST = "/api/v1/hosts/h1"
const CA_STATUS = `POST ${CA_HOST}/actions/npm.instance.status/v1/read`
const CA_INSPECT = `POST ${CA_HOST}/actions/docker.container.inspect/v1/read`
const CA_NETWORKS = `GET ${CA_HOST}/resources/docker/networks`
const CA_PLAN = "POST /api/v1/actions/npm.expose.container/v1/plan"
const CA_DONE = { id: "ch1", state: "completed", class: "composite", error: null, output: null, revertDeadline: null }
const CA_DOWN = { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } }

const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function caDeployed(over: Parameters<typeof npInstance>[0] = {}) {
  return npInstance({ deployed: true, containerId: "c0ffee", running: true, apiReady: true, public: "confirmed", adminEmail: "admin@example.com", ...over })
}

function caRender(routes: Record<string, StubHandler> = {}, networks: string[] = ["bridge", "backend"]) {
  const calls = stubFetch({
    [CA_STATUS]: { body: { output: caDeployed() } },
    [CA_INSPECT]: {
      body: { output: dkDetail({ id: "c1", ports: [{ privatePort: 80, proto: "tcp" }], networks: networks.map((network) => ({ network, networkId: "id-" + network, container: "web" })) }) },
    },
    [CA_NETWORKS]: { body: { networks: networks.map((n) => dkNetwork({ id: "id-" + n, name: n, driver: "bridge" })) } },
    [CA_PLAN]: (call: FetchCall) => ({
      body: { planId: "p1", hostId: null, action: "npm.expose.container", version: 1, inputHash: "h", diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null },
    }),
    "POST /api/v1/actions/npm.expose.container/v1/apply": { body: CA_DONE },
    "GET /api/v1/changes/ch1": { body: CA_DONE },
    "GET /api/v1/changes/ch1/steps": { body: { steps: [] } },
    ...routes,
  })
  const result = renderWithProviders(<ExposeAction container={caContainer} onDone={() => {}} />)
  return { statusReads: () => calls.filter((c) => c.path === `${CA_HOST}/actions/npm.instance.status/v1/read`).length, ...result }
}

const caViews: { state: string; routes: Record<string, StubHandler>; networks: string[]; ready: () => Promise<unknown> }[] = [
  { state: "the Expose form", routes: {}, networks: ["bridge", "backend"], ready: () => screen.findByRole("form", { name: "Expose container" }) },
  { state: "the form of a container on no user-defined network", routes: {}, networks: ["bridge"], ready: () => screen.findByRole("form", { name: "Expose container" }) },
  { state: "a host without a deployed instance", routes: { [CA_STATUS]: { body: { output: caDeployed({ deployed: false, running: false, apiReady: false, public: "no" }) } } }, networks: ["bridge"], ready: () => screen.findByText(/not deployed/) },
  { state: "a failed status read", routes: { [CA_STATUS]: CA_DOWN }, networks: ["bridge"], ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ routes, networks, ready }) => {
  const { container } = caRender(routes, networks)
  await ready()
  await expectNoAxeViolations(container)
})

test("the form with an invalid port has no axe violations and marks the port invalid", async () => {
  const { container } = caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "http")
  await expectNoAxeViolations(container)
  expect(within(form).getByLabelText("Port").getAttribute("aria-invalid")).toBe("true")
})

test("the plan and applied dialogs of the exposure have no axe violations", async () => {
  caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await expectNoAxeViolations(dialog)
})

test("Tab goes through every field of the form in reading order with its label as name", async () => {
  caRender()
  await screen.findByRole("form", { name: "Expose container" })
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["backend", "Port", "Domains", "Websockets"])
})

test("the exposure dialog keeps Tab inside it and Escape gives focus back to Plan after the form re-renders", async () => {
  const { statusReads } = caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  const before = statusReads()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(statusReads()).toBeGreaterThan(before))
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: "Expose container" })).getByRole("button", { name: "Plan" }))
})
```

`npm-change.a11y.test.tsx` covers `NpmChange` (an agent action) and `NpmControllerChange` (a composite) in five states each.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState, type ReactElement } from "react"
import { describe, expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { NpmChange, NpmControllerChange } from "./npm-change"

interface NcVariant {
  name: string
  base: string // the path prefix of the action's plan, apply and availability requests
  hostId: string | null
  action: string
  title: string
  class: string
  element: (open: boolean, close: () => void) => ReactElement
}

const ncVariants: NcVariant[] = [
  {
    name: "NpmChange",
    hostId: "h1",
    base: "/api/v1/hosts/h1/actions/npm.proxyhost.remove/v1",
    action: "npm.proxyhost.remove",
    title: "Remove proxy host app.example.com",
    class: "compensable",
    element: (open, close) => (
      <NpmChange hostId="h1" action="npm.proxyhost.remove" title="Remove proxy host app.example.com" input={open ? { domain: "app.example.com" } : null} onClose={close} />
    ),
  },
  {
    name: "NpmControllerChange",
    hostId: null,
    base: "/api/v1/actions/npm.expose.container/v1",
    action: "npm.expose.container",
    title: "Expose web at app.example.com",
    class: "composite",
    element: (open, close) => (
      <NpmControllerChange
        action="npm.expose.container"
        title="Expose web at app.example.com"
        input={open ? { hostId: "h1", container: "web", port: 80, network: "backend", domains: ["app.example.com"] } : null}
        onClose={close}
      />
    ),
  },
]

function ncHarness(variant: NcVariant) {
  return function NcHarness() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open change
        </button>
        {variant.element(open, () => setOpen(false))}
      </>
    )
  }
}

function ncPlan(variant: NcVariant, confirmation: string | null): StubResponse {
  return { body: { planId: "p1", hostId: variant.hostId, action: variant.action, version: 1, inputHash: "h", diff: [{ op: "~", text: variant.action }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation } }
}

describe.each(ncVariants)("$name", (variant) => {
  const done = { id: "ch1", state: "completed", class: variant.class, error: null, output: null, revertDeadline: null }
  const states: { state: string; routes: Record<string, StubHandler>; ready: (d: HTMLElement) => Promise<unknown>; apply?: true }[] = [
    { state: "planning", routes: { [`POST ${variant.base}/plan`]: () => new Promise<StubResponse>(() => {}) }, ready: (d) => within(d).findByLabelText("Planning the change") },
    { state: "the plan", routes: {}, ready: (d) => within(d).findByLabelText("Planned changes") },
    {
      state: "a plan error",
      routes: { [`POST ${variant.base}/plan`]: { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "NPM is busy", code: "npm.busy" } } },
      ready: (d) => within(d).findByText("NPM is busy"),
    },
    { state: "the typed confirmation", routes: { [`POST ${variant.base}/plan`]: ncPlan(variant, "app.example.com") }, ready: (d) => within(d).findByLabelText("Type app.example.com to confirm") },
    { state: "the applied change", routes: {}, ready: (d) => within(d).findByText("completed"), apply: true },
  ]

  test.each(states)("$state has no axe violations, keeps Tab inside the dialog and gives focus back to its trigger", async ({ routes, ready, apply }) => {
    stubFetch({
      "GET /api/v1/hosts/h1/modules/npm/availability": { body: { module: "npm", available: true, reason: null, actions: [] } },
      [`POST ${variant.base}/plan`]: ncPlan(variant, null),
      [`POST ${variant.base}/apply`]: { body: done },
      "GET /api/v1/changes/ch1": { body: done },
      "GET /api/v1/changes/ch1/steps": { body: { steps: [] } },
      ...routes,
    })
    const Harness = ncHarness(variant)
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "Open change" }))
    const dialog = await screen.findByRole("dialog", { name: variant.title })
    if (apply !== undefined) {
      const button = await within(dialog).findByRole("button", { name: "Apply" })
      await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false))
      await user.click(button)
    }
    await ready(dialog)
    await expectNoAxeViolations(dialog)
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open change" }))
  })
})

test("NpmChange on a host whose module is unavailable shows why and has no axe violations", async () => {
  const variant = ncVariants[0] as NcVariant
  stubFetch({
    "GET /api/v1/hosts/h1/modules/npm/availability": { body: { module: "npm", available: false, reason: "Docker is not installed", actions: [] } },
    [`POST ${variant.base}/plan`]: ncPlan(variant, null),
  })
  const Harness = ncHarness(variant)
  renderWithProviders(<Harness />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: variant.title })
  await within(dialog).findByText("Docker is not installed")
  await expectNoAxeViolations(dialog)
})
```

`cloudflare/container-actions.a11y.test.tsx` covers the gating states of `ExposeAction` and the links to the page that fixes each.

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import { cfConnector, cfTunnel } from "./fixtures"

const CA_HOST = "/api/v1/hosts/h1"
const CA_TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const CA_CONNECTORS = `POST ${CA_HOST}/actions/cloudflare.connector.list/v1/read`
const CA_INSPECT = `POST ${CA_HOST}/actions/docker.container.inspect/v1/read`
const CA_NETWORKS = `GET ${CA_HOST}/resources/docker/networks`
const CA_DOWN = { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Cloudflare account is not connected", code: "cloud.not_connected" } }

const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function caRender(routes: Record<string, StubHandler> = {}) {
  stubFetch({
    [CA_TUNNELS]: { body: { output: { tunnels: [cfTunnel({ id: "t1", name: "darkpanel-a" })] } } },
    [CA_CONNECTORS]: { body: { output: { connectors: [cfConnector({ tunnel: "t1" })] } } },
    [CA_INSPECT]: {
      body: { output: dkDetail({ id: "c1", ports: [{ privatePort: 80, proto: "tcp" }], networks: ["bridge", "backend"].map((network) => ({ network, networkId: "id-" + network, container: "web" })) }) },
    },
    [CA_NETWORKS]: { body: { networks: [dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }), dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" })] } },
    ...routes,
  })
  return renderWithProviders(<ExposeAction container={caContainer} onDone={() => {}} />)
}

const caViews: { state: string; routes: Record<string, StubHandler>; ready: () => Promise<unknown> }[] = [
  { state: "the Expose via tunnel form", routes: {}, ready: () => screen.findByRole("form", { name: "Expose container via tunnel" }) },
  { state: "a host with no tunnel", routes: { [CA_TUNNELS]: { body: { output: { tunnels: [] } } } }, ready: () => screen.findByText("No Cloudflare tunnel exists yet. Create a tunnel first.") },
  { state: "a host with no connector", routes: { [CA_CONNECTORS]: { body: { output: { connectors: [] } } } }, ready: () => screen.findByText("No Cloudflare connector runs on this host. Deploy one first.") },
  { state: "a failed read", routes: { [CA_TUNNELS]: CA_DOWN }, ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ routes, ready }) => {
  const { container } = caRender(routes)
  await ready()
  await expectNoAxeViolations(container)
})

const caGaps: { state: string; routes: Record<string, StubHandler>; link: string }[] = [
  { state: "no tunnel", routes: { [CA_TUNNELS]: { body: { output: { tunnels: [] } } } }, link: "Open the tunnels page" },
  { state: "no connector", routes: { [CA_CONNECTORS]: { body: { output: { connectors: [] } } } }, link: "Open the connectors page" },
]

test.each(caGaps)("with $state, Tab reaches the link to the page that fixes it", async ({ routes, link }) => {
  caRender(routes)
  await screen.findByRole("link", { name: link })
  expect((await tabSequence(userEvent.setup(), 1))[0]).toBe(link)
})
```

`expose-dialog.a11y.test.tsx` covers `ExposeTunnelForm`.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { dkNetwork } from "@/modules/docker/fixtures"
import type { DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { ExposeTunnelForm } from "./expose-dialog"

const ED_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"
const ED_FORM = "Expose container via tunnel"

const edContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }
const edTunnels = [
  { id: "t1", name: "darkpanel-a" },
  { id: "t2", name: "darkpanel-b" },
]

function edNetworks(): DockerNetwork[] {
  return ["bridge", "backend", "alpha"].map((name) => dkNetwork({ id: "id-" + name, name, driver: "bridge" }))
}

function edRender(endpoints: string[] = ["bridge", "backend", "alpha"], onDone: () => void = () => {}) {
  stubFetch({
    [ED_PLAN]: (call: FetchCall) => ({
      body: { planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h", diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null },
    }),
    "GET /api/v1/changes/ch1": { body: { id: "ch1", state: "completed", class: "composite", error: null, output: null, revertDeadline: null } },
  })
  return renderWithProviders(<ExposeTunnelForm container={edContainer} tunnels={edTunnels} endpoints={endpoints} networks={edNetworks()} ports={[9000, 80]} onDone={onDone} />)
}

test.each([
  { state: "the form", endpoints: ["bridge", "backend", "alpha"] },
  { state: "the form of a container on no user-defined network", endpoints: ["bridge"] },
])("$state has no axe violations", async ({ endpoints }) => {
  const { container } = edRender(endpoints)
  await screen.findByRole("form", { name: ED_FORM })
  await expectNoAxeViolations(container)
})

test("the form with an invalid port has no axe violations and marks the port invalid", async () => {
  const { container } = edRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "http")
  await expectNoAxeViolations(container)
  expect(within(form).getByLabelText("Port").getAttribute("aria-invalid")).toBe("true")
})

test("Tab goes through the tunnel, the network, the port and the hostname, then Plan once it is enabled", async () => {
  edRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  expect(await tabSequence(user, 4)).toEqual(["darkpanel-a", "alpha", "Port", "Hostname"])
  await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
  expect(await tabSequence(user, 1)).toEqual(["Plan"])
})

test("the plan dialog keeps Tab inside it and Escape gives focus back to Plan, with onDone called once", async () => {
  const onDone = vi.fn()
  edRender(["bridge", "backend", "alpha"], onDone)
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(onDone).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: ED_FORM })).getByRole("button", { name: "Plan" }))
})
```

`cloudflare-change.a11y.test.tsx` covers `CloudflareChange` as a controller action and as an agent action.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState, type ReactElement } from "react"
import { describe, expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { CloudflareChange } from "./cloudflare-change"

interface CcVariant {
  name: string
  base: string // the path prefix of the action's plan and apply requests
  hostId: string | null
  action: string
  title: string
  class: string
  element: (open: boolean, close: () => void) => ReactElement
}

const ccVariants: CcVariant[] = [
  {
    name: "a controller action",
    hostId: null,
    base: "/api/v1/actions/cloudflare.expose.container/v1",
    action: "cloudflare.expose.container",
    title: "Expose web at app.example.com",
    class: "composite",
    element: (open, close) => (
      <CloudflareChange action="cloudflare.expose.container" title="Expose web at app.example.com" input={open ? { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "backend", hostname: "app.example.com" } : null} onClose={close} />
    ),
  },
  {
    name: "an agent action",
    hostId: "h1",
    base: "/api/v1/hosts/h1/actions/cloudflare.connector.stop/v1",
    action: "cloudflare.connector.stop",
    title: "Stop the connector of darkpanel-a",
    class: "compensable",
    element: (open, close) => (
      <CloudflareChange action="cloudflare.connector.stop" title="Stop the connector of darkpanel-a" hostId="h1" input={open ? { tunnel: "t1" } : null} onClose={close} />
    ),
  },
]

function ccHarness(variant: CcVariant) {
  return function CcHarness() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open change
        </button>
        {variant.element(open, () => setOpen(false))}
      </>
    )
  }
}

function ccPlan(variant: CcVariant, confirmation: string | null): StubResponse {
  return { body: { planId: "p1", hostId: variant.hostId, action: variant.action, version: 1, inputHash: "h", diff: [{ op: "~", text: variant.action }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation } }
}

describe.each(ccVariants)("$name", (variant) => {
  const done = { id: "ch1", state: "completed", class: variant.class, error: null, output: null, revertDeadline: null }
  const states: { state: string; routes: Record<string, StubHandler>; ready: (d: HTMLElement) => Promise<unknown>; apply?: true }[] = [
    { state: "planning", routes: { [`POST ${variant.base}/plan`]: () => new Promise<StubResponse>(() => {}) }, ready: (d) => within(d).findByLabelText("Planning the change") },
    { state: "the plan", routes: {}, ready: (d) => within(d).findByLabelText("Planned changes") },
    {
      state: "a plan error",
      routes: { [`POST ${variant.base}/plan`]: { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the tunnel changed", code: "cloud.conflict" } } },
      ready: (d) => within(d).findByText("the tunnel changed"),
    },
    { state: "the typed confirmation", routes: { [`POST ${variant.base}/plan`]: ccPlan(variant, "darkpanel-a") }, ready: (d) => within(d).findByLabelText("Type darkpanel-a to confirm") },
    { state: "the applied change", routes: {}, ready: (d) => within(d).findByText("completed"), apply: true },
  ]

  test.each(states)("$state has no axe violations, keeps Tab inside the dialog and gives focus back to its trigger", async ({ routes, ready, apply }) => {
    stubFetch({
      "GET /api/v1/hosts/h1/modules/cloudflare/availability": { body: { module: "cloudflare", available: true, reason: null, actions: [] } },
      [`POST ${variant.base}/plan`]: ccPlan(variant, null),
      [`POST ${variant.base}/apply`]: { body: done },
      "GET /api/v1/changes/ch1": { body: done },
      "GET /api/v1/changes/ch1/steps": { body: { steps: [] } },
      ...routes,
    })
    const Harness = ccHarness(variant)
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "Open change" }))
    const dialog = await screen.findByRole("dialog", { name: variant.title })
    if (apply !== undefined) {
      const button = await within(dialog).findByRole("button", { name: "Apply" })
      await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false))
      await user.click(button)
    }
    await ready(dialog)
    await expectNoAxeViolations(dialog)
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open change" }))
  })
})

test("an agent action on a host whose module is unavailable shows why and has no axe violations", async () => {
  const variant = ccVariants[1] as CcVariant
  stubFetch({
    "GET /api/v1/hosts/h1/modules/cloudflare/availability": { body: { module: "cloudflare", available: false, reason: "Docker is not installed", actions: [] } },
    [`POST ${variant.base}/plan`]: ccPlan(variant, null),
  })
  const Harness = ccHarness(variant)
  renderWithProviders(<Harness />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: variant.title })
  await within(dialog).findByText("Docker is not installed")
  await expectNoAxeViolations(dialog)
})
```

The three global pages each get a title, axe, restore-on-unmount and Tab-order test. The sub-pages are replaced by markers, as the existing `global.test.tsx` of each module does.

```tsx
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import CloudflareGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the page's own title, landmarks and keyboard order.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./tunnels-page", () => ({ TunnelsPage: () => <p>tunnels page</p> }))
vi.mock("./connectors-page", () => ({ ConnectorsPage: () => <p>connectors page</p> }))
vi.mock("./hostnames-page", () => ({ HostnamesPage: () => <p>hostnames page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/cloudflare/*" element={<CloudflareGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/cloudflare", "account panel"],
  ["/cloudflare/tunnels", "tunnels page"],
  ["/cloudflare/connectors", "connectors page"],
  ["/cloudflare/hostnames", "hostnames page"],
  ["/cloudflare/nowhere", "Page not found"],
])("%s has the page title Cloudflare and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("Cloudflare · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/cloudflare")
  await waitFor(() => expect(document.title).toBe("Cloudflare · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})

test("Tab reaches the four page links in order, named by their text", async () => {
  gaRender("/cloudflare")
  await screen.findByText("account panel")
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["Account", "Tunnels", "Connectors", "Hostnames"])
})
```

```tsx
import { screen, waitFor } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import DerpGlobal from "./global"

// The page of another file is replaced by a marker; this file tests the page's own title and landmarks.
vi.mock("./derp-page", () => ({ DerpPage: () => <p>derp page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/derp/*" element={<DerpGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/derp", "derp page"],
  ["/derp/nowhere", "Page not found"],
])("%s has the page title DERP and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("DERP · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/derp")
  await waitFor(() => expect(document.title).toBe("DERP · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})
```

```tsx
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import TailnetGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the page's own title, landmarks and keyboard order.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./devices-page", () => ({ DevicesPage: () => <p>devices page</p> }))
vi.mock("./keys-page", () => ({ KeysPage: () => <p>keys page</p> }))
vi.mock("./policy-page", () => ({ PolicyPage: () => <p>policy page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/tailnet/*" element={<TailnetGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/tailnet", "account panel"],
  ["/tailnet/devices", "devices page"],
  ["/tailnet/keys", "keys page"],
  ["/tailnet/policy", "policy page"],
  ["/tailnet/nowhere", "Page not found"],
])("%s has the page title Tailnet and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("Tailnet · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/tailnet")
  await waitFor(() => expect(document.title).toBe("Tailnet · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})

test("Tab reaches the four page links in order, named by their text", async () => {
  gaRender("/tailnet")
  await screen.findByText("account panel")
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["Account", "Devices", "Auth keys", "Policy"])
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w9-tmp/20 && TMPDIR=/root/.cache/w9-tmp/20 GOTOOLCHAIN=local timeout 600 pnpm -C web exec vitest run src/modules/docker/containers-tab.a11y.test.tsx src/modules/docker/container-page.a11y.test.tsx src/modules/docker/docker-change.a11y.test.tsx src/modules/firewall/container-actions.a11y.test.tsx src/modules/npm/container-actions.a11y.test.tsx src/modules/npm/npm-change.a11y.test.tsx src/modules/cloudflare/container-actions.a11y.test.tsx src/modules/cloudflare/expose-dialog.a11y.test.tsx src/modules/cloudflare/cloudflare-change.a11y.test.tsx src/modules/cloudflare/global.a11y.test.tsx src/modules/derp/global.a11y.test.tsx src/modules/tailnet/global.a11y.test.tsx`
Expected: 18 failed and 81 passed on this task's base (21 and 78 on `3c5e837`, where the three heading-order failures listed last also fail; Task 14 fixes those). The failures are:
- the page-title tests of the three global pages (15): `expected '' to be 'Tailnet · Darkpanel'` (and `'DERP · Darkpanel'`, `'Cloudflare · Darkpanel'`), and `expected 'Before' to be ...` for the restore tests;
- `when an applied Stop leaves the row without that action, focus moves to the row's first enabled action`: `expected '' to be 'Start web'`;
- `when an applied Remove deletes the row, focus moves to the containers group`: `Unable to find role="group" and name "Containers"`;
- `when a confirmed Deny leaves nothing to deny, the Plan button is disabled and focus moves to the form`: `expected <body ...> to be <form aria-label="Deny" ...>`;
- three `... module ... unavailable ... has no axe violations` tests (the Docker, NPM and Cloudflare change wrappers): `axe found 1 violation(s): heading-order: h5`. The source is `AlertTitle` in `web/src/components/change-dialog.tsx` (an h5 after the dialog's h2), a file this task does not own: Task 14 fixes it, and Task 14 is in this task's base, so these three pass at once; if they fail, stop and report.

All other tests pass already; they are the evidence of the audit and guard against regressions. Quote the real output. A test that fails for any other reason means the test or its fixture is wrong: fix it, never weaken an assertion.

- [ ] **Step 3: Implement**

1. `tailnet/global.tsx`, `derp/global.tsx`, `cloudflare/global.tsx`: call `usePageTitle` with the page's label (`Tailnet`, `DERP`, `Cloudflare`, the `label` of its entry in `web/src/modules/registry.ts`) as the first statement of the default-exported component, so the title is `<label> · Darkpanel` on every route of the page and the previous title returns on unmount. Replace each file's not-found route element, `<EmptyState title="Page not found" description="Nothing lives at this address." />`, with a plain element that keeps both texts and renders `Page not found` as an `h2` (darkraise-ui's `EmptyState` always renders its title as an `h3`, straight after the page header's `h1`, which axe reports as `heading-order`); remove the `EmptyState` import when it is no longer used. The existing `global.test.tsx` of each module finds the texts and must keep passing.
2. `docker/containers-tab.tsx`: wrap the table, the empty state and the dialog in one element with `role="group"`, `aria-label="Containers"` and `tabIndex={-1}`, and carry the container id on each row (a data attribute) so a row can be found again. When the dialog closes, remember which container and which action opened it and the query's `dataUpdatedAt` at that moment. When a later render has a different `dataUpdatedAt` (the read the close started has landed) and the remembered request is still set, clear it, and if focus is not inside the group (the control that held it was removed and focus fell to the body) move focus to: the button whose accessible name equals the trigger's if the row still has it enabled; else the first enabled action button of the same container's row; else the group. Do nothing when focus is already inside the group, which is the case whenever the dialog's own restore found the trigger. Do it in a layout effect so there is no frame without focus. The existing `containers-tab.test.tsx` must keep passing.
3. `firewall/container-actions.tsx`: give the form `tabIndex={-1}` and a ref, and apply the same rule in `ContainerRuleForm` once the dialog closes and the integration is read again: the Plan button is the trigger and normally survives, but after a Deny that leaves no allowed binding it is disabled and focus falls to the body, so focus goes to the form. The hook runs before the component's early returns (the loading and error states). `FirewallChange` belongs to another task; change nothing in it.
4. Every other file of the Files block changes only if its own a11y test fails in a way this task owns, and then by the smallest change that makes the test pass: a missing accessible name, a focusable element without one, a dialog trigger that is replaced on re-render. A failure whose cause is `web/src/components/change-dialog.tsx` or another file outside this block is not fixed here: stop and report it.

- [ ] **Step 4: Run the tests to verify they pass**

Run the command of Step 2 again.
Expected: all tests pass, except that the three `unavailable` tests pass only when Task 14's fix of the change dialog's heading level is in the base (see Step 2). Then run `TMPDIR=/root/.cache/w9-tmp/20 GOTOOLCHAIN=local timeout 1200 make lint-web test-web`: eslint with `--max-warnings 0`, `tsc --noEmit` and the whole Vitest suite pass. Quote the output, then `rm -rf /root/.cache/w9-tmp/20`.

- [ ] **Step 5: Commit**

Three commits, one concern each:

```bash
git add web/src/modules/docker/containers-tab.tsx web/src/modules/docker/containers-tab.a11y.test.tsx web/src/modules/firewall/container-actions.tsx web/src/modules/firewall/container-actions.a11y.test.tsx
git commit -m "fix(web): keep focus after container changes"
git add web/src/modules/tailnet/global.tsx web/src/modules/tailnet/global.a11y.test.tsx web/src/modules/derp/global.tsx web/src/modules/derp/global.a11y.test.tsx web/src/modules/cloudflare/global.tsx web/src/modules/cloudflare/global.a11y.test.tsx
git commit -m "fix(web): title the module global pages"
git add web/src/modules/docker/container-page.a11y.test.tsx web/src/modules/docker/docker-change.a11y.test.tsx web/src/modules/npm/container-actions.a11y.test.tsx web/src/modules/npm/npm-change.a11y.test.tsx web/src/modules/cloudflare/container-actions.a11y.test.tsx web/src/modules/cloudflare/expose-dialog.a11y.test.tsx web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx
git commit -m "test(web): add a11y tests for container actions"
```
If Step 3 changed one of the conditional files, add it to the third commit.

### Task 21: harness: TestFootprint and the vm-fp job

**Files:**
- Create: `test/harness/footprint_test.go`
- Create: `test/harness/footprint_unit_test.go`
- Create: `test/harness/fpw_workflow_unit_test.go`
- Modify: `.github/workflows/harness.yml` (the `build` job's steps and a new `vm-fp` job; every other job stays as Task 11 left it)

**Interfaces:**
- Consumes: Contracts "Footprint" (the constants and the job), "Harness state directory" (Task 4: `--state-dir`, bound to `harnessStateDir`, only in builds with the `harness` tag) and "Upgrade matrix" (Task 11's `vm-upg` job and its `build` job steps stay untouched). From the existing harness: `m5dLab`, `fleet`, `agentAddress`, `versionN`, `m2Flag`, `osFlag`, `waitHosts`, `hostsAt`, `findHost`, `m3Run`, `update.ControllerUnit`, `update.AgentUnit`, `core.HostCreateInput`, `core.EnrollmentGrant`, `coreactions.ModuleInput`, and for the workflow check `m3dxExpoJob`, `m4lStep`, `m4lSetup`, `m4lJobMax`, `m3dxExpoLimitRE`, `m3dxExpoTimeoutRE`.
- Produces: `TestFootprint`, the build `bin/harness/darkpanel-0.2.0-state`, the job `vm-fp`, and the unit tests of the pure helpers. Identifiers are prefixed `fp`/`Fp` (`fpYml` for the workflow check, whose `fpw` prefix gosec's G101 mistakes for a password).

**Items:** 40

**Depends on:** Task 4, Task 11
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

- [ ] **Step 1: Verify the unverified assumption in the code**

The Assumptions bullet "nine extra agents on the agent VM can enroll under `--state-dir`, run as `systemd-run` units, and have every module but `hostinfo` disabled before their firewall preflight starts a reconcile" is unverified. The proof is `TestFootprint`'s first step, `extra-agents`, on a harness VM; it cannot run on this host, so check what the code can show now:

Run: `rg -n 'installed := p == l.Paths' cmd/darkpanel/agent.go && rg -n 'func \(r \*Runtime\) moduleEnabled' -A12 internal/agent/runtime/enablement.go && rg -n 'DO UPDATE SET enabled = 0' internal/controller/coreactions/enablement.go && rg -n 'state-dir' cmd/darkpanel/paths_harness.go`
Expected: the first line shows that an agent whose paths differ from the installed layout (every `--state-dir` agent) gets `devSystemd` and no systemd connection; the second shows an agent enabled by default until the controller pushes a flag, so the extra agents' flags are written before they first connect (the step creates the host, disables the modules, and only then enrolls and starts the agent); the third shows `core.module.disable` writing a row for a host the controller has not heard from; the fourth shows Task 4's flag. Record the four lines. If any is missing, stop and report. The step then proves the rest on a VM: it fails when an extra host lists a module besides `hostinfo` enabled, when its unit is not active, or when its journal shows a reconcile pass. If that step fails on the first harness run, the fix is outside this task's Files: stop and report what the journal showed.

- [ ] **Step 2: Write the failing tests**

Create `test/harness/footprint_test.go`. It holds the flag, the constants of Contracts "Footprint" and `TestFootprint`: it boots the lab with `m5dLab` (controller, agent, scanner, releases served, the harness build installed and the agent enrolled), then runs four steps. `extra-agents` (gate) makes nine more hosts on the agent VM and checks the assumption above; `warm-up` (gate) waits `fpWarmup`; `window` (gate) runs `fpSchedule`: the RSS of the real agent and of the controller every `fpSampleEvery` and the controller's host list every `fpHostsEvery`, and every host list must show all eleven hosts enrolled and online; `budgets` compares the maxima with `fpAgentMax` and `fpControllerMax`. A failed sample is logged and left out; `fpCheckBudget` requires nine tenths of the samples. The extra agents' units are stopped in a cleanup so a failed run leaves nothing running.

```go
//go:build harness

package harness

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var fpFlag = flag.Bool("fp", false, "run TestFootprint; needs -os and -m2, and is a go test run of its own")

// The footprint budgets (bytes, decimal megabytes) and the sampling plan of TestFootprint.
const (
	fpAgentMax      = 40_000_000
	fpControllerMax = 150_000_000
	fpWarmup        = 5 * time.Minute
	fpWindow        = 10 * time.Minute
	fpSampleEvery   = 10 * time.Second
	fpHostsEvery    = 15 * time.Second
	fpExtraAgents   = 9
	// fpDeadline bounds TestFootprint. The workflow step that runs it must keep its -timeout at least fpDeadline
	// plus 10 minutes, for the cleanups that stop the VMs.
	fpDeadline = 45 * time.Minute
)

// fpRun is what the steps of TestFootprint share.
type fpRun struct {
	ctx context.Context
	f   *fleet
	// extra are the hosts of the extra agents, in the order they were created.
	extra []core.Host
	// agent and controller are the RSS samples in bytes.
	agent, controller []int64
	// problems are the failures of the window that did not stop it: a refused host list, a host that went offline.
	problems []string
}

// TestFootprint is the footprint gate of spec section 9 (R76, S6): the real agent of an enrolled VM, idle with
// every module enabled, stays within fpAgentMax of resident memory, and the controller, which manages its own host,
// that VM and fpExtraAgents harness-only agents on that VM and has its host list requested every fpHostsEvery,
// stays within fpControllerMax. Both are sampled every fpSampleEvery for fpWindow after fpWarmup. The workflow
// selects it with -run, -os, -m2 and -fp.
func TestFootprint(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*fpFlag {
		t.Skip("no -os, -m2 or -fp given: the VM tests run in .github/workflows/harness.yml")
	}
	if _, err := os.Stat(filepath.Join(*m2Flag, fpBinaryName)); err != nil {
		t.Fatalf("%v; the build job builds it with -tags harnesskeys,harness", err)
	}
	if err := fpCheckLayout(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fpDeadline)
	t.Cleanup(cancel)
	r := &fpRun{ctx: ctx, f: m5dLab(ctx, t)}
	t.Cleanup(func() { r.stopExtraAgents() })
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"extra-agents", true, r.extraAgents},
		{"warm-up", true, r.warmUp},
		{"window", true, r.window},
		{"budgets", false, r.budgets},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// extraAgents makes fpExtraAgents more hosts on the agent VM, each an agent of the harness build under its own
// --state-dir, run as a systemd-run unit. A host's modules but hostinfo are disabled before its agent first
// connects, so no background task of the firewall module starts beside the real agent's. The step checks that
// this held: every extra host lists only hostinfo enabled, its unit is active and its journal shows no reconcile
// pass; and that the real agent's host lists every module enabled.
func (r *fpRun) extraAgents(t *testing.T) {
	v := r.f.agent
	for _, cmd := range fpPrepareCmds() {
		m3Run(r.ctx, t, v, cmd)
	}
	if err := v.CopyFile(r.ctx, filepath.Join(*m2Flag, fpBinaryName), fpBinaryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= fpExtraAgents; n++ {
		var grant core.EnrollmentGrant
		input := core.HostCreateInput{Name: fmt.Sprintf("fp-%d", n), ControllerAddress: agentAddress}
		if _, err := r.f.api.Apply(r.ctx, "", "core.host.create/v1", input, &grant); err != nil {
			t.Fatal(err)
		}
		r.extra = append(r.extra, grant.Host)
		for _, id := range fpDisableTargets(grant.Host) {
			in := coreactions.ModuleInput{HostID: grant.Host.ID, ModuleID: id}
			if _, err := r.f.api.Apply(r.ctx, "", "core.module.disable/v1", in, nil); err != nil {
				t.Fatalf("disable %s on %s: %v", id, grant.Host.Name, err)
			}
		}
		tokenFile := fmt.Sprintf("/root/fp-token-%d", n)
		if _, err := v.RunInput(r.ctx, "sudo sh -c 'umask 077 && cat > "+tokenFile+"'", strings.NewReader(grant.Token)); err != nil {
			t.Fatal(err)
		}
		out, err := v.Run(r.ctx, fpEnrollCmd(n, tokenFile))
		m3Run(r.ctx, t, v, "sudo rm -f "+tokenFile)
		if err != nil {
			t.Fatalf("enroll extra agent %d: %v\n%s", n, err, out)
		}
		m3Run(r.ctx, t, v, fpRunCmd(n))
	}
	ids := make([]string, len(r.extra))
	for i, h := range r.extra {
		ids[i] = h.ID
	}
	hosts := waitHosts(r.ctx, t, r.f, 3*time.Minute, hostsAt(versionN, ids...))
	for n, h := range r.extra {
		listed, _ := findHost(hosts, h.ID)
		if err := fpOnlyHostinfoEnabled(listed); err != nil {
			t.Error(err)
		}
		if state := strings.TrimSpace(m3Run(r.ctx, t, v, "systemctl is-active "+fpUnit(n+1))); state != "active" {
			t.Errorf("%s is %q, want active", fpUnit(n+1), state)
		}
		journal := m3Run(r.ctx, t, v, "sudo journalctl -u "+fpUnit(n+1)+" --no-pager -o cat")
		if err := fpNoReconcile(journal); err != nil {
			t.Errorf("%s: %v", fpUnit(n+1), err)
		}
	}
	realAgent, _ := findHost(hosts, r.f.agentID)
	if err := fpAllEnabled(realAgent); err != nil {
		t.Error(err)
	}
	if err := fpFleetOnline(hosts, fpExtraAgents+2); err != nil {
		t.Error(err)
	}
}

// warmUp lets every process settle for fpWarmup.
func (r *fpRun) warmUp(t *testing.T) {
	select {
	case <-r.ctx.Done():
		t.Fatal(r.ctx.Err())
	case <-time.After(fpWarmup):
	}
}

// window runs the sampling plan: the RSS of the real agent and of the controller every fpSampleEvery and the
// controller's host list every fpHostsEvery, for fpWindow.
func (r *fpRun) window(t *testing.T) {
	start := time.Now()
	for _, ev := range fpSchedule(fpWindow, fpSampleEvery, fpHostsEvery) {
		select {
		case <-r.ctx.Done():
			t.Fatal(r.ctx.Err())
		case <-time.After(time.Until(start.Add(ev.At))):
		}
		switch ev.Kind {
		case fpSampleEvent:
			r.sample(t, ev.At)
		case fpHostsEvent:
			hosts, err := r.f.api.Hosts(r.ctx)
			if err == nil {
				err = fpFleetOnline(hosts, fpExtraAgents+2)
			}
			if err != nil {
				r.problems = append(r.problems, fmt.Sprintf("host list at %s: %v", ev.At, err))
			}
		}
	}
}

// sample reads both processes' resident memory. A failed read is logged and left out; budgets requires enough
// samples to make the maximum mean something.
func (r *fpRun) sample(t *testing.T, at time.Duration) {
	for _, s := range []struct {
		name string
		v    *vm.VM
		unit string
		into *[]int64
	}{
		{"agent", r.f.agent, update.AgentUnit, &r.agent},
		{"controller", r.f.controller, update.ControllerUnit, &r.controller},
	} {
		pid, err := fpMainPID(r.ctx, s.v, s.unit)
		var rss int64
		if err == nil {
			rss, err = fpRSS(r.ctx, s.v, pid)
		}
		if err != nil {
			t.Logf("%s at %s: no sample: %v", s.name, at, err)
			continue
		}
		*s.into = append(*s.into, rss)
		t.Logf("%s at %s: %d bytes", s.name, at, rss)
	}
}

// budgets compares the maxima with the budgets and reports the problems of the window.
func (r *fpRun) budgets(t *testing.T) {
	for _, p := range r.problems {
		t.Error(p)
	}
	want := fpSampleCount(fpWindow, fpSampleEvery)
	if err := fpCheckBudget("agent", r.agent, fpAgentMax, want); err != nil {
		t.Error(err)
	}
	if err := fpCheckBudget("controller", r.controller, fpControllerMax, want); err != nil {
		t.Error(err)
	}
}

// stopExtraAgents stops the extra agents' units, so that a failed run leaves no process on the VM. It runs on a
// context of its own: the test's may have ended.
func (r *fpRun) stopExtraAgents() {
	if r.f == nil || r.f.agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for n := 1; n <= fpExtraAgents; n++ {
		_, _ = r.f.agent.Run(ctx, fpStopCmd(n))
	}
}
```

Create `test/harness/footprint_unit_test.go`, the tests of the pure parts: the `MainPID` and `VmRSS` parsers (the awk command prints the number of kB, which `/proc` counts in units of 1024 bytes, so the parser returns bytes and 39 063 kB is over the agent's 40 000 000), the maximum against the budget (the largest sample decides, and too few samples fail), the sampling schedule (60 samples and 40 host lists in ten minutes, the sample first when both are due), the extra-agent commands and the short state directories that keep each agent socket within a Unix socket's 107 bytes, and the module and fleet checks.

```go
//go:build harness

package harness

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestFpUnitParsePID(t *testing.T) {
	for _, c := range []struct {
		out     string
		want    int
		wantErr string // "" accepts
	}{
		{"4321\n", 4321, ""},
		{"  17 \n", 17, ""},
		{"0\n", 0, "not a running process"},
		{"", 0, "not a running process"},
		{"-3\n", 0, "not a running process"},
		{"4321 4322\n", 0, "not a running process"},
		{"n/a\n", 0, "not a running process"},
	} {
		got, err := fpParsePID(c.out)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) || got != c.want {
			t.Errorf("fpParsePID(%q) = %d, %v; want %d, %q", c.out, got, err, c.want, c.wantErr)
		}
	}
}

// The awk command prints the second field of the VmRSS line: a number of kB, which /proc counts in units of 1024
// bytes. The budgets are decimal megabytes in bytes, so the parser returns bytes.
func TestFpUnitParseRSS(t *testing.T) {
	for _, c := range []struct {
		out     string
		want    int64
		wantErr string
	}{
		{"12345\n", 12345 * 1024, ""},
		{"1\n", 1024, ""},
		{" 39062 \n", 39062 * 1024, ""},
		{"", 0, "not a positive number"},
		{"0\n", 0, "not a positive number"},
		{"-4\n", 0, "not a positive number"},
		{"12 kB\n", 0, "not a positive number"},
		{"1.5\n", 0, "not a positive number"},
		{"awk: cannot open /proc/9/status\n", 0, "not a positive number"},
	} {
		got, err := fpParseRSS(c.out)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) || got != c.want {
			t.Errorf("fpParseRSS(%q) = %d, %v; want %d, %q", c.out, got, err, c.want, c.wantErr)
		}
	}
	// 40 000 000 bytes is 39062.5 KiB: a sample of 39063 KiB is over the agent budget, one of 39062 KiB is not.
	over, _ := fpParseRSS("39063\n")
	under, _ := fpParseRSS("39062\n")
	if over <= fpAgentMax || under > fpAgentMax {
		t.Errorf("39063 KiB = %d and 39062 KiB = %d bytes, want one over and one within %d", over, under, int64(fpAgentMax))
	}
}

func TestFpUnitBudget(t *testing.T) {
	full := func(n int, v int64) []int64 { return slices.Repeat([]int64{v}, n) }
	within := full(60, fpAgentMax)
	maxLast := append(full(59, 10_000_000), fpAgentMax+1)
	for _, c := range []struct {
		name    string
		samples []int64
		limit   int64
		want    int
		wantErr string // "" accepts
	}{
		{"every sample at the limit", within, fpAgentMax, 60, ""},
		{"nine tenths of the samples", full(54, 1), fpAgentMax, 60, ""},
		{"the maximum is the last sample", maxLast, fpAgentMax, 60, "40000001 bytes, over the 40000000"},
		{"the maximum is the first sample", append([]int64{fpControllerMax + 1}, full(59, 1)...), fpControllerMax, 60, "150000001 bytes, over the 150000000"},
		{"no samples", nil, fpAgentMax, 60, "0 RSS samples"},
		{"too few samples", full(53, 1), fpAgentMax, 60, "53 RSS samples, want at least 54 of 60"},
	} {
		err := fpCheckBudget("agent", c.samples, c.limit, c.want)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: fpCheckBudget = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitSchedule(t *testing.T) {
	if got := fpSampleCount(fpWindow, fpSampleEvery); got != 60 {
		t.Errorf("fpSampleCount = %d, want 60 samples of 10 s in 10 minutes", got)
	}
	evs := fpSchedule(fpWindow, fpSampleEvery, fpHostsEvery)
	var samples, hosts int
	for i, ev := range evs {
		switch ev.Kind {
		case fpSampleEvent:
			samples++
		case fpHostsEvent:
			hosts++
		}
		if ev.At <= 0 || ev.At > fpWindow {
			t.Errorf("event %d at %v is outside (0, %v]", i, ev.At, fpWindow)
		}
		if i > 0 && (ev.At < evs[i-1].At || (ev.At == evs[i-1].At && ev.Kind < evs[i-1].Kind)) {
			t.Errorf("event %d (%v %d) comes before event %d (%v %d)", i, ev.At, ev.Kind, i-1, evs[i-1].At, evs[i-1].Kind)
		}
	}
	if samples != 60 || hosts != 40 {
		t.Errorf("%d sample and %d host list events, want 60 and 40", samples, hosts)
	}
	if evs[0].At != fpSampleEvery || evs[len(evs)-1].At != fpWindow {
		t.Errorf("the plan runs from %v to %v, want %v to %v", evs[0].At, evs[len(evs)-1].At, fpSampleEvery, fpWindow)
	}
	// At 30 s both are due; the sample comes first, so the host list does not delay it.
	if i := slices.IndexFunc(evs, func(e fpEvent) bool { return e.At == 30*time.Second }); evs[i].Kind != fpSampleEvent || evs[i+1].Kind != fpHostsEvent {
		t.Errorf("events at 30 s: %+v %+v, want the sample first", evs[i], evs[i+1])
	}
	if got := fpSchedule(time.Minute, 0, fpHostsEvery); len(got) != 4 {
		t.Errorf("a zero sample interval left %d events, want only the 4 host lists", len(got))
	}
	if got := fpSampleCount(time.Second, 0); got != 0 {
		t.Errorf("fpSampleCount with a zero interval = %d, want 0", got)
	}
}

func TestFpUnitExtraAgentCommands(t *testing.T) {
	if fpExtraAgents != 9 {
		t.Fatalf("fpExtraAgents = %d, want 9: the controller manages eleven hosts, one more than ten", fpExtraAgents)
	}
	if err := fpCheckLayout(); err != nil {
		t.Fatal(err)
	}
	units := map[string]bool{}
	for n := 1; n <= fpExtraAgents; n++ {
		dir, unit := fpStateDir(n), fpUnit(n)
		if units[unit] {
			t.Errorf("unit %s is used twice", unit)
		}
		units[unit] = true
		if !strings.HasPrefix(dir, fpStateRoot+"/") || strings.Count(strings.TrimPrefix(dir, fpStateRoot+"/"), "/") != 0 {
			t.Errorf("state dir %q is not directly under %s", dir, fpStateRoot)
		}
		// A Unix socket path holds 107 bytes; the longest path under a state directory is the agent's socket.
		if sock := paths.Under(dir).AgentSocket; len(sock) > fpSocketMax {
			t.Errorf("agent socket %s is %d bytes, over %d", sock, len(sock), fpSocketMax)
		}
		run := fpRunCmd(n)
		for _, want := range []string{"sudo systemd-run", "--unit=" + unit, fpBinaryPath + " agent run", "--state-dir " + dir} {
			if !strings.Contains(run, want) {
				t.Errorf("fpRunCmd(%d) = %q lacks %q", n, run, want)
			}
		}
		enroll := fpEnrollCmd(n, "/root/fp-token-1")
		for _, want := range []string{"sudo " + fpBinaryPath + " agent enroll", "--state-dir " + dir, "--token-file /root/fp-token-1"} {
			if !strings.Contains(enroll, want) {
				t.Errorf("fpEnrollCmd(%d) = %q lacks %q", n, enroll, want)
			}
		}
		if stop := fpStopCmd(n); !strings.Contains(stop, unit) || !strings.HasPrefix(stop, "sudo systemctl stop ") {
			t.Errorf("fpStopCmd(%d) = %q", n, stop)
		}
	}
	if err := fpSocketProblem(fpStateRoot + "/" + strings.Repeat("a", 90)); err == nil || !strings.Contains(err.Error(), "over the 107") {
		t.Errorf("a 90-character state directory: %v, want an error naming the 107-byte limit", err)
	}
	if fpBinaryName != "darkpanel-0.2.0-state" || !strings.HasPrefix(fpBinaryPath, fpStateRoot+"/") {
		t.Errorf("binary %q at %q", fpBinaryName, fpBinaryPath)
	}
	if got := fpPIDCmd("darkpanel-agent.service"); got != "systemctl show -p MainPID --value darkpanel-agent.service" {
		t.Errorf("fpPIDCmd = %q", got)
	}
	if got := fpRSSCmd(77); got != "awk '/^VmRSS/{print $2}' /proc/77/status" {
		t.Errorf("fpRSSCmd = %q", got)
	}
}

func fpModules(enabled map[string]bool) []core.ModuleState {
	var ms []core.ModuleState
	for _, id := range []string{"hostinfo", "firewall", "docker", "tailscale", "tailnet", "derp", "npm", "cloudflare", "panel"} {
		on, listed := enabled[id]
		if listed {
			ms = append(ms, core.ModuleState{ID: id, Enabled: on})
		}
	}
	return ms
}

func TestFpUnitModuleChecks(t *testing.T) {
	all := map[string]bool{"hostinfo": true, "firewall": true, "docker": true, "tailscale": true, "tailnet": true, "derp": true, "npm": true, "cloudflare": true, "panel": true}
	onlyInfo := map[string]bool{"hostinfo": true, "firewall": false, "docker": false, "tailscale": false, "tailnet": false, "derp": false, "npm": false, "cloudflare": false, "panel": false}
	h := func(ms map[string]bool) core.Host { return core.Host{Name: "fp-1", Modules: fpModules(ms)} }

	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := fpDisableTargets(h(all)); !slices.Equal(got, want) {
		t.Errorf("fpDisableTargets = %v, want %v (every module but hostinfo, sorted)", got, want)
	}
	if got := fpDisableTargets(core.Host{}); len(got) != 0 {
		t.Errorf("fpDisableTargets of a host with no modules = %v", got)
	}

	for _, c := range []struct {
		name    string
		host    core.Host
		check   func(core.Host) error
		wantErr string
	}{
		{"only hostinfo: accepted", h(onlyInfo), fpOnlyHostinfoEnabled, ""},
		{"only hostinfo: firewall still on", h(map[string]bool{"hostinfo": true, "firewall": true, "docker": false}), fpOnlyHostinfoEnabled, "firewall enabled true"},
		{"only hostinfo: hostinfo off", h(map[string]bool{"hostinfo": false, "firewall": false}), fpOnlyHostinfoEnabled, "hostinfo enabled false"},
		{"only hostinfo: not listed", h(map[string]bool{"firewall": false}), fpOnlyHostinfoEnabled, "no hostinfo module listed"},
		{"only hostinfo: no modules", core.Host{Name: "fp-1"}, fpOnlyHostinfoEnabled, "no hostinfo module listed"},
		{"all enabled: accepted", h(all), fpAllEnabled, ""},
		{"all enabled: docker off", h(map[string]bool{"hostinfo": true, "docker": false}), fpAllEnabled, "[docker]"},
		{"all enabled: no modules", core.Host{Name: "agent"}, fpAllEnabled, "every module enabled"},
	} {
		err := c.check(c.host)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitFleetOnline(t *testing.T) {
	up := func(name string) core.Host { return core.Host{Name: name, State: core.HostEnrolled, Online: true} }
	hosts := []core.Host{up("controller"), up("agent"), up("fp-1")}
	for _, c := range []struct {
		name    string
		hosts   []core.Host
		want    int
		wantErr string
	}{
		{"all up", hosts, 3, ""},
		{"one offline", []core.Host{up("controller"), {Name: "fp-1", State: core.HostEnrolled}, up("agent")}, 3, "fp-1"},
		{"one pending", []core.Host{up("controller"), {Name: "fp-2", State: core.HostPending, Online: true}, up("agent")}, 3, "fp-2"},
		{"a host is missing", hosts[:2], 3, "2 hosts, want 3"},
		{"an extra host", append(slices.Clone(hosts), up("stray")), 3, "4 hosts, want 3"},
	} {
		err := fpFleetOnline(c.hosts, c.want)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitNoReconcile(t *testing.T) {
	quiet := "level=INFO msg=\"agent: serving\" socket=/var/lib/dpx/3/run/agent.sock\nlevel=INFO msg=\"daemon: controller session\" state=up\n"
	if err := fpNoReconcile(quiet); err != nil {
		t.Errorf("a quiet journal: %v", err)
	}
	loud := quiet + "level=WARN msg=\"reconcile skipped: DOCKER-USER holds foreign rules\"\n"
	if err := fpNoReconcile(loud); err == nil || !strings.Contains(err.Error(), "DOCKER-USER holds foreign rules") {
		t.Errorf("a journal with a reconcile line: %v, want an error quoting it", err)
	}
	if err := fpNoReconcile("task firewall.RECONCILE pass\n"); err == nil {
		t.Error("an upper-case RECONCILE passes: the match must ignore case")
	}
}
```

Create `test/harness/fpw_workflow_unit_test.go`, which checks the text of `.github/workflows/harness.yml` in the style of `m5dw_workflow_unit_test.go`: the build job builds the footprint agent with the harness tags and version `0.2.0` before it uploads `bin/harness`, its unit test step also runs the `cmd/darkpanel` tests under the `harness` tag, the `vm-fp` job has the vm jobs' setup, `timeout-minutes: 80` and a step with the lab flags, a work directory of its own, `-timeout 60m` (at least `fpDeadline` plus ten minutes) and failure logs, and no other job holds the test. The fixture mutations prove each check.

```go
//go:build harness

package harness

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	fpYmlBuildStep = "Build the footprint agent"
	fpYmlUnitStep  = "Harness unit tests"
	fpYmlRunStep   = "Run the footprint harness"
	fpYmlJob       = "vm-fp"
	// fpYmlStepTimeout is the -timeout in minutes of the TestFootprint step and fpYmlJobLimit the job's
	// timeout-minutes.
	fpYmlStepTimeout = 60
	fpYmlJobLimit    = 80
)

var (
	// fpYmlUploadRE finds the line of the step that uploads the bin/harness directory, and not the path of a cache
	// step that names files inside it.
	fpYmlUploadRE = regexp.MustCompile(`(?m)^\s*path: bin/harness$`)
	// fpYmlJobRE finds the key of a job: two spaces, a name and a colon.
	fpYmlJobRE = regexp.MustCompile(`(?m)^  ([a-z0-9][a-z0-9-]*):\s*$`)
)

// fpYmlWorkflow checks the text of harness.yml for TestFootprint: the build job builds the extra agent's binary with
// the harness tags and the version of the M2 install, before it uploads bin/harness, and its unit test step also
// runs the tests of cmd/darkpanel under the harness tag; the vm-fp job has the vm jobs' setup, a timeout-minutes
// of fpYmlJobLimit, and a step that runs the test with the lab flags, a work directory of its own, a -timeout of
// fpYmlStepTimeout minutes and its failure logs; and no other job holds the test.
func fpYmlWorkflow(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return errors.New("harness: the workflow has no build job")
	}
	step, ok := m4lStep(build, fpYmlBuildStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", fpYmlBuildStep)
	}
	for _, want := range []string{
		"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "go build", "-tags harnesskeys,harness",
		"-X github.com/darkraise/darkpanel/internal/buildinfo.Version=" + versionN,
		"-o bin/harness/darkpanel-" + versionN + "-state", "./cmd/darkpanel",
	} {
		if !strings.Contains(step, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlBuildStep, want)
		}
	}
	upload := fpYmlUploadRE.FindStringIndex(build)
	if at := strings.Index(build, "- name: "+fpYmlBuildStep); upload == nil || at < 0 || at > upload[0] {
		return fmt.Errorf("harness: the build job does not run the step %q before it uploads path: bin/harness", fpYmlBuildStep)
	}
	units, ok := m4lStep(build, fpYmlUnitStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", fpYmlUnitStep)
	}
	for _, want := range []string{"go test -tags harness ./test/harness/...", "go test -tags harness ./cmd/darkpanel/"} {
		if !strings.Contains(units, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlUnitStep, want)
		}
	}

	job, ok := m3dxExpoJob(yml, fpYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", fpYmlJob)
	}

	for _, name := range fpYmlJobs(yml) {
		if name == fpYmlJob || name == "build" {
			continue
		}
		other, _ := m3dxExpoJob(yml, name)
		for _, text := range []string{"TestFootprint", "harness-fp", " -fp"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, fpYmlJob)
			}
		}
	}

	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", fpYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", fpYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != fpYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", fpYmlJob, have, fpYmlJobLimit)
	}

	run, ok := m4lStep(job, fpYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", fpYmlJob, fpYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestFootprint$'", " -fp", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-fp"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", fpYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != fpYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", fpYmlRunStep, m[1], fpYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < fpDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", fpYmlRunStep, m[1], fpDeadline)
	}
	for _, want := range []string{"name: logs-fp-${{ matrix.os }}", "harness-fp/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", fpYmlJob, want)
		}
	}
	return nil
}

// fpYmlJobs returns the names of the jobs of the workflow text, in order.
func fpYmlJobs(yml string) []string {
	_, jobs, ok := strings.Cut(yml, "\njobs:\n")
	if !ok {
		return nil
	}
	var names []string
	for _, m := range fpYmlJobRE.FindAllStringSubmatch(jobs, -1) {
		names = append(names, m[1])
	}
	return names
}

const (
	fpYmlBuildSteps = `      - name: Build the footprint agent
        run: >-
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags harnesskeys,harness
          -ldflags "-X github.com/darkraise/darkpanel/internal/buildinfo.Version=0.2.0"
          -o bin/harness/darkpanel-0.2.0-state ./cmd/darkpanel
      - name: Harness unit tests
        run: |
          go test -tags harness ./test/harness/...
          go test -tags harness ./cmd/darkpanel/
`
	fpYmlUploadStep = `      - uses: actions/upload-artifact@x
        with:
          path: bin/harness
`
	// fpYmlWorkflowDoc has the parts of harness.yml that fpYmlWorkflow reads.
	fpYmlWorkflowDoc = `name: Harness
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Cache the derper build
        uses: actions/cache@x
        with:
          path: bin/harness/derper-*
` + fpYmlBuildSteps + fpYmlUploadStep + `  vm-m5d:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 60
    steps:
      - name: Run the M5 DERP harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM5DERP$' ./test/harness/
          -args -os ${{ matrix.os }} -m5d
  vm-upg:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 90
    steps:
      - name: Run the upgrade matrix
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/
          -args -os ${{ matrix.os }} -upg
  vm-fp:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 80
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: test -e /dev/kvm
      - run: sudo apt-get install -y qemu-system-x86
      - uses: actions/checkout@x
      - uses: actions/setup-go@x
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-linux-amd64
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-harness
      - uses: actions/cache@x
      - run: go vet -tags harness ./test/...
      - name: Run the footprint harness
        run: >-
          go test -tags harness -timeout 60m -v -run '^TestFootprint$' ./test/harness/
          -args -os ${{ matrix.os }} -fp
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-fp"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-fp-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-fp/**/*.log
`
)

func TestFpwUnitWorkflow(t *testing.T) {
	if err := fpYmlWorkflow(fpYmlWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	if got := fpYmlJobs(fpYmlWorkflowDoc); !strings.EqualFold(strings.Join(got, " "), "build vm-m5d vm-upg vm-fp") {
		t.Fatalf("fpYmlJobs = %v, want build vm-m5d vm-upg vm-fp", got)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no build job", "  build:\n", "  compile:\n", "no build job"},
		{"no agent build step", "Build the footprint agent", "Build something", `no step named "Build the footprint agent"`},
		{"both architectures", "GOARCH=amd64 go build", "go build", "GOARCH=amd64"},
		{"no harness tag", "-tags harnesskeys,harness\n", "-tags harnesskeys\n", "-tags harnesskeys,harness"},
		{"no keys tag", "-tags harnesskeys,harness\n", "-tags harness\n", "-tags harnesskeys,harness"},
		{"no version stamp", "buildinfo.Version=0.2.0", "buildinfo.Commit=abc", "buildinfo.Version=0.2.0"},
		{"version of another build", "buildinfo.Version=0.2.0", "buildinfo.Version=0.3.0", "buildinfo.Version=0.2.0"},
		{"binary name", "-o bin/harness/darkpanel-0.2.0-state", "-o bin/darkpanel-0.2.0-state", "bin/harness/darkpanel-0.2.0-state"},
		{"no unit test step", "Harness unit tests", "Unit tests", `no step named "Harness unit tests"`},
		{"no cmd test", "          go test -tags harness ./cmd/darkpanel/\n", "", "./cmd/darkpanel/"},
		{"cmd test without the tag", "go test -tags harness ./cmd/darkpanel/", "go test ./cmd/darkpanel/", "go test -tags harness ./cmd/darkpanel/"},
		{"no harness package tests", "go test -tags harness ./test/harness/...", "go test ./test/harness/...", "./test/harness/..."},
		{"no vm-fp job", "  vm-fp:\n", "  vm-other:\n", "no vm-fp job"},
		{"limit of vm-m5d", "timeout-minutes: 80", "timeout-minutes: 60", "timeout-minutes 60, want 80"},
		{"limit past 120", "timeout-minutes: 80", "timeout-minutes: 130", "timeout-minutes 130, want 80"},
		{"no needs", "    needs: build\n    runs-on: ubuntu-24.04\n    timeout-minutes: 80", "    runs-on: ubuntu-24.04\n    timeout-minutes: 80", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "/dev/kvm", "/dev/none", "/dev/kvm"},
		{"no run step", "Run the footprint harness", "Run footprint", `no step named "Run the footprint harness"`},
		{"wrong test", "'^TestFootprint$'", "'^TestOther$'", "TestFootprint"},
		{"no flag", " -fp\n", "\n", " -fp"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-fp\"\n", "", "-workdir"},
		{"workdir of another job", "-workdir \"$RUNNER_TEMP/harness-fp\"", "-workdir \"$RUNNER_TEMP/harness-m5d\"", "harness-fp"},
		{"short timeout", "-timeout 60m -v -run", "-timeout 50m -v -run", "-timeout 50m"},
		{"long timeout", "-timeout 60m -v -run", "-timeout 70m -v -run", "-timeout 70m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-fp/**/*.log\n", "", "harness-fp/**/*.log"},
		{"artifact name of another job", "name: logs-fp-${{ matrix.os }}", "name: logs-m5d-${{ matrix.os }}", "logs-fp-"},
		{"test added to vm-m5d", "-run '^TestM5DERP$'", "-run '^TestM5DERP$|^TestFootprint$'", "vm-m5d job holds TestFootprint"},
		{"flag added to vm-upg", "-upg\n", "-upg -fp\n", "vm-upg job holds  -fp"},
	} {
		if !strings.Contains(fpYmlWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", tc.name, tc.from)
		}
		doc := strings.ReplaceAll(fpYmlWorkflowDoc, tc.from, tc.to)
		if err := fpYmlWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}

	moved := strings.Replace(fpYmlWorkflowDoc, fpYmlBuildSteps, "", 1)
	moved = strings.Replace(moved, fpYmlUploadStep, fpYmlUploadStep+fpYmlBuildSteps, 1)
	if err := fpYmlWorkflow(moved); err == nil || !strings.Contains(err.Error(), "before it uploads path: bin/harness") {
		t.Errorf("the agent build after the upload: %v, want an error naming the upload", err)
	}
}

// TestFpwUnitWorkflowFile checks the committed workflow, which is the only place TestFootprint runs.
func TestFpwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := fpYmlWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w9-tmp/21 && TMPDIR=/root/.cache/w9-tmp/21 GOTOOLCHAIN=local timeout 300 go vet -tags harness ./test/harness/`
Expected: FAIL with a build error naming the first undefined identifier: `undefined: fpBinaryName`.

- [ ] **Step 4: Implement**

Add the pure helpers to `test/harness/footprint_test.go`, below `TestFootprint`. Every name below is the one the tests use; the unit tests pin the behaviour.

1. Constants: `fpBinaryName = "darkpanel-" + versionN + "-state"`; `fpStateRoot = "/var/lib/dpx"` (short on purpose: the longest path under a state directory is the agent socket, `<dir>/run/agent.sock`, and a Unix socket path holds 108 bytes with its NUL); `fpBinaryPath = fpStateRoot + "/bin/darkpanel-state"` (outside every path uninstall removes); `fpSocketMax = 107`; `fpHostinfo = "hostinfo"`.
2. Commands, each returning the shell text the test runs on the agent VM: `fpStateDir(n int) string` is `/var/lib/dpx/<n>`; `fpUnit(n int) string` is `dpx-agent-<n>`; `fpPrepareCmds() []string` is one `sudo install -d -m 0755` of the state root and its `bin`; `fpEnrollCmd(n int, tokenFile string) string` is `sudo <fpBinaryPath> agent enroll --state-dir <dir> --token-file <file>`; `fpRunCmd(n int) string` is `sudo systemd-run --unit=<unit> --collect <fpBinaryPath> agent run --state-dir <dir>`; `fpStopCmd(n int) string` is `sudo systemctl stop <unit>`; `fpPIDCmd(unit string) string` is `systemctl show -p MainPID --value <unit>`; `fpRSSCmd(pid int) string` is `awk '/^VmRSS/{print $2}' /proc/<pid>/status`.
3. Layout: `fpSocketProblem(dir string) error` fails, naming the socket path, its length and the limit, when `paths.Under(dir).AgentSocket` (from `internal/paths`) is longer than `fpSocketMax`; `fpCheckLayout() error` applies it to `fpStateDir(1..fpExtraAgents)`.
4. Parsers: `fpParsePID(out string) (int, error)` trims the output and accepts a positive integer only (`0` means the unit is not running); `fpParseRSS(out string) (int64, error)` trims it, accepts a positive integer of kB only and returns kB times 1024 (the budgets are decimal megabytes in bytes). Both errors start with `harness:` and quote the output.
5. VM glue: `fpMainPID(ctx, v *vm.VM, unit string) (int, error)` and `fpRSS(ctx, v *vm.VM, pid int) (int64, error)` run the command and parse the answer; no unit test covers them.
6. Budget and schedule: `fpSampleCount(window, every time.Duration) int` is `window / every`, and 0 for a non-positive `every` or a window shorter than `every`; `fpCheckBudget(name string, samples []int64, limit int64, want int) error` fails when fewer than `want * 9 / 10` samples exist (including none) with `<n> RSS samples, want at least <m> of <want>`, and when the largest sample is over `limit` with `the largest of <n> RSS samples is <bytes> bytes, over the <limit>`; `fpKind`, `fpSampleEvent`, `fpHostsEvent` and `fpEvent{At time.Duration; Kind fpKind}`; `fpSchedule(window, sampleEvery, hostsEvery time.Duration) []fpEvent` returns one event at every multiple of each interval up to and including the window (an interval of zero or less adds none), sorted by time with the sample event before the host list event at the same time.
7. Host checks: `fpDisableTargets(h core.Host) []string` is the sorted ids of the host's listed modules other than `hostinfo`; `fpOnlyHostinfoEnabled(h core.Host) error` fails, naming the host and each offender as `<id> enabled <bool>` or `no hostinfo module listed`; `fpAllEnabled(h core.Host) error` fails when the host lists no module or any is disabled, naming the disabled ids; `fpFleetOnline(hosts []core.Host, want int) error` fails when the list does not have `want` hosts (`<n> hosts, want <want>`) or names, joined, every host not `core.HostEnrolled` and online; `fpNoReconcile(journal string) error` fails on the first journal line containing `reconcile` in any case, quoting the line.

Then edit `.github/workflows/harness.yml` (read Task 11's version first and keep its jobs and steps as they are):

1. In the `build` job, insert this step immediately before the step named `Harness unit tests` (and so before the upload of `bin/harness`):

```yaml
      # The extra agents of TestFootprint run this build under --state-dir, which only a harness build has.
      - name: Build the footprint agent
        run: >-
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags harnesskeys,harness
          -ldflags "-s -w -X github.com/darkraise/darkpanel/internal/buildinfo.Version=0.2.0"
          -o bin/harness/darkpanel-0.2.0-state ./cmd/darkpanel
```

2. In the step `Harness unit tests`, keep every command it already runs and add `go test -tags harness ./cmd/darkpanel/` as another line, turning `run: go test -tags harness ./test/harness/...` into:

```yaml
        run: |
          go test -tags harness ./test/harness/...
          go test -tags harness ./cmd/darkpanel/
```

3. Append the job after the last job of the file:

```yaml
  vm-fp:
    # The footprint test's job. The limit is the step's -timeout plus 20 minutes of setup.
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 80
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: |
          if [ ! -e /dev/kvm ]; then
            echo "::error::/dev/kvm is missing on this runner; the harness needs KVM (ADR 0008)"
            exit 1
          fi
          echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
          sudo udevadm control --reload-rules
          sudo udevadm trigger --name-match=kvm
      - name: Install QEMU and cloud-image-utils
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends qemu-system-x86 qemu-utils cloud-image-utils
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-linux-amd64
          path: bin
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-harness
          path: bin/harness
      - uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: ~/.cache/darkpanel-harness
          key: harness-images-${{ matrix.os }}-${{ hashFiles('test/harness/images.json') }}
      - run: go vet -tags harness ./test/...
      # TestFootprint boots a lab of its own with one agent, adds nine agents of the
      # footprint build on the agent VM, and samples the resident memory of the real
      # agent and of the controller. Its own deadline is 45 minutes, and the extra 15
      # leave its VM cleanups time to finish.
      - name: Run the footprint harness
        run: >-
          go test -tags harness -timeout 60m -v -run '^TestFootprint$' ./test/harness/
          -args -os ${{ matrix.os }} -fp
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-fp"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-fp-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-fp/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w9-tmp/21 GOTOOLCHAIN=local && timeout 300 go vet -tags harness ./test/... && timeout 300 go test -tags harness -count=1 -run 'FpUnit|FpwUnit' -v ./test/harness/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 300 go test -tags harness -count=1 -run '^TestFootprint$' -v ./test/harness/`
Expected: vet prints nothing; every `TestFpUnit*` and `TestFpwUnit*` passes (`TestFpwUnitWorkflowFile` too, which was the one failing test once the helpers compiled: `harness: the build job has no step named "Build the footprint agent"`); every `Unit` test passes; the `cmd/darkpanel` tests pass; the last command prints `--- SKIP: TestFootprint` (no `-os`, `-m2` or `-fp` given, so it does not touch this host).

Then the lines of Global Constraints for a task that edits `test/harness/`: `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./test/harness/` with the `harness` tag, `golangci-lint run --allow-serial-runners ./...` and `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` (`0 issues.`), `go mod tidy -diff` (nothing). Quote the outputs, then `rm -rf /root/.cache/w9-tmp/21 && go clean -testcache`.

`TestFootprint` itself runs only on harness VMs. The plan's closing gates dispatch `harness.yml` on the branch; record the `vm-fp` conclusion of each OS there, with the `extra-agents` step's result, which settles the Assumptions bullet.

- [ ] **Step 6: Commit**

```bash
git add test/harness/footprint_test.go test/harness/footprint_unit_test.go
git commit -m "test(harness): add TestFootprint"
git add .github/workflows/harness.yml test/harness/fpw_workflow_unit_test.go
git commit -m "chore(ci): add the footprint job to the harness"
```

### Task 22: web a11y: browser pass for contrast, zoom and reflow

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/e2e/fixtures.ts` (test support: every `/api/` answer)
- Test: `web/e2e/a11y.pw.ts`
- Modify: `web/tsconfig.node.json` (include the config and `e2e/`, add the DOM library and the `@/*` path so `tsc` checks them)
- Modify: `web/src/styles/globals.css` (append the contrast overrides)
- Modify: `web/src/components/change-dialog.tsx` (class names only; the browser pass found the plan body of `ChangeDialogBody` clipping at 320 px)
- Modify: any other file under `web/src/` that Step 3's red run names as failing and that Tasks 13-15 and 20 own, only where the failure is a class name or an ARIA attribute; list each in the report with the rule that failed. No text, markup structure, behaviour or test file changes.
- Create: `docs/accessibility/<slug>-<theme>-<check>.png` (56 screenshots, see Interfaces)

**Interfaces:**
- Consumes: the pages and dialogs that Tasks 13-15 and 20 leave, reached through their accessible names: the login form (`Username`, `Password`, `Authentication code`, `Log in`), the firewall page (`Add rule`, `To port`, `Plan`, the change dialog's `Override firewall.lockout`, `Apply`, `Confirm`, `Revert`), the Docker containers table (`Docker containers`, `Stop web`, `Type web to confirm`) and the container page (the `Expose container` form, `Domains`, `Plan`). If a merged task renamed one of them, change the locator in `a11y.pw.ts`, not the component. The wire shapes come from `web/src/api/types.ts` and the modules' `types.ts` files, imported as types only. The theme comes from `darkraise-ui`'s `ThemeProvider`: it reads the `localStorage` key `mode` (`light` or `dark`) and sets `data-mode` on `<html>`, and it writes its colour tokens as inline styles on `<html>` (`web/src/providers.tsx`, `web/src/theme.config.ts`).
- Produces: `web/e2e/a11y.pw.ts`, run with `pnpm -C web exec playwright test -c playwright.config.ts` (Vite picks a free `127.0.0.1` port and Playwright passes it to the tests as `E2E_PORT`; `E2E_CHROMIUM` names a Chromium executable when the build this Playwright pins is not installed). Its checks, by describe name: `contrast` (axe `color-contrast`, 1.4.3), `reflow-320` (1.4.10), `zoom-200` (1.4.4), `non-text contrast` (text-field boundaries, 1.4.11), `focus visible` (2.4.7) and `change dialog names` (accessible names, 4.1.2). Task 23 cites it and the screenshots `docs/accessibility/<slug>-<theme>-<check>.png` with `<slug>` one of `login`, `hosts`, `firewall-change-plan`, `firewall-change-applied`, `container-actions-list`, `container-actions-dialog`, `container-page`, `expose-plan`; `<theme>` `light` or `dark`; `<check>` `contrast`, `reflow-320` and `zoom-200` for every slug, and `names` for the four slugs that end in a change dialog (`firewall-change-plan`, `firewall-change-applied`, `container-actions-dialog`, `expose-plan`). The contrast overrides in `globals.css` apply to the default accent (blue) and surface (zinc) in both modes.

**Items:** 41

**Depends on:** Task 13, Task 14, Task 15, Task 20
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 1 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Verify the unverified assumptions**

Run from the repository root:

```bash
export TMPDIR=/root/.cache/w9-tmp/22 GOTOOLCHAIN=local && mkdir -p "$TMPDIR"
(cd web && timeout 60 node --input-type=module -e 'import { createRequire } from "node:module"; const r = createRequire(import.meta.url); console.log(createRequire(r.resolve("vitest-axe")).resolve("axe-core/axe.min.js"))')
(cd web && timeout 60 node --input-type=module -e 'import { chromium } from "@playwright/test"; const b = await chromium.launch({ executablePath: process.env.E2E_CHROMIUM || undefined }); console.log(b.version()); await b.close()')
```

Expected, as observed on this host on 2026-11-05: the first command prints a path ending `/axe-core@4.14.0/node_modules/axe-core/axe.min.js`. The second fails with `browserType.launch: Executable doesn't exist at /root/.cache/ms-playwright/chromium_headless_shell-1248/...`: Playwright 1.64.0 pins build 1248 and the cache holds 1217, 1223, 1228 and 1243. Then export the newest cached shell and run the second command again; it prints a version (`153.0.8010.12` for build 1243):

```bash
export E2E_CHROMIUM=$(ls -d ~/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell | sort -V | tail -1)
```

Do not download a browser unless no cached shell launches. Quote both outputs in the report. If the first command fails, stop and report BLOCKED: the pass needs `axe-core` from `vitest-axe`'s own `node_modules`.

- [ ] **Step 2: Write the failing test**

Create `web/playwright.config.ts`:

```ts
import { defineConfig, devices } from "@playwright/test"

export default defineConfig({
  testDir: "e2e",
  testMatch: "*.pw.ts",
  outputDir: "node_modules/.playwright-results",
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: "list",
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // A Chromium of another cached build may stand in when the build this
        // Playwright pins is not installed.
        launchOptions: { executablePath: process.env.E2E_CHROMIUM || undefined },
      },
    },
  ],
  // Vite picks a free port itself; the tests read it from E2E_PORT, which
  // Playwright sets from the named group of the server's first output line.
  webServer: {
    command: "pnpm exec vite --host 127.0.0.1 --port 0 --strictPort false",
    wait: { stdout: /Local:\s+http:\/\/127\.0\.0\.1:(?<e2e_port>\d+)/ },
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
```

Create `web/e2e/fixtures.ts`:

```ts
import type { Page, Route } from "@playwright/test"
import type { Change, Host, Plan, Session } from "../src/api/types"
import type { ContainerDetail, DockerContainer, DockerNetwork } from "../src/modules/docker/types"
import type { DockerStatus, ExposureView, FirewallStatus } from "../src/modules/firewall/types"
import type { InstanceStatus } from "../src/modules/npm/types"

export const HOST_ID = "01JE00000000000000000000H1"
export const OFFLINE_HOST_ID = "01JE00000000000000000000H2"
export const CONTAINER_ID = "c0ffee0123456789abcdef"
export const FIREWALL_CHANGE_ID = "01JE000000000000000000C001"
export const EXPOSE_CHANGE_ID = "01JE000000000000000000C002"

const NOW = "2026-11-05T09:00:00Z"

const session: Session = {
  user: { id: "01JE000000000000000000U001", username: "admin", role: "admin", state: "active", createdAt: NOW },
  csrfToken: "csrf-fixture",
  expiresAt: null,
  token: null,
}

function moduleState(id: string, over: Partial<Host["modules"][number]> = {}): Host["modules"][number] {
  return { id, version: 1, enabled: true, available: true, reason: null, checkedAt: NOW, ...over }
}

const hosts: Host[] = [
  {
    id: HOST_ID,
    name: "web-1",
    isLocal: true,
    online: true,
    modules: [moduleState("hostinfo"), moduleState("firewall"), moduleState("docker"), moduleState("npm"), moduleState("tailscale", { enabled: false })],
    ingestion: { ok: true, since: NOW, error: null },
  },
  {
    id: OFFLINE_HOST_ID,
    name: "db-1",
    isLocal: false,
    online: false,
    modules: [
      moduleState("hostinfo"),
      moduleState("firewall", { available: false, reason: "UFW is not installed." }),
      moduleState("docker", { enabled: false }),
    ],
    ingestion: { ok: false, since: NOW, error: "The agent has not answered since 08:12." },
  },
]

const firewallStatus: FirewallStatus = {
  supported: true,
  unsupported: [],
  active: true,
  inSync: true,
  outOfSync: [],
  configured: { enabled: true, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } },
  loaded: {
    variant: "nft",
    ipv6: true,
    sysctl: { "net/ipv4/ip_forward": "0" },
    chains: [{ family: "v4", table: "filter", chain: "INPUT", policy: "DROP", rules: 3 }],
  },
  rules: [
    { id: "01JE0000000000000000000R01", action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "22", family: "both", origin: "darkpanel", comment: "ssh" },
    { id: "manual-0123456789abcdef", action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "8080", family: "v4", origin: "manual" },
  ],
}

const firewallDocker: DockerStatus = {
  available: true,
  version: "29.9.1",
  driver: "iptables",
  dockerVariant: "nft",
  ufwVariant: "nft",
  rawProtection: true,
  supported: true,
  enabled: true,
  health: "ok",
  networks: [{ name: "bridge", id: "0123456789abcdef", bridge: "docker0", subnets: ["172.17.0.0/16"], protected: true }],
  settings: { privateSources: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"], forwardExemptions: [] },
  rules: [],
  bindings: [
    { binding: { selector: "web", hostPort: 8080, proto: "tcp" }, id: "a1b2c3d4e5f6", containers: ["web"], containerPort: 80, allowed: [], denied: [] },
  ],
}

const exposure: ExposureView = {
  rows: [
    {
      proto: "tcp",
      addr: "0.0.0.0",
      port: 8080,
      family: "v4",
      container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false },
      classes: [{ class: "internet", verdict: "closed" }],
    },
  ],
}

const containers: DockerContainer[] = [
  {
    id: CONTAINER_ID,
    name: "web",
    image: "nginx:1.27",
    imageId: "sha256:0123456789abcdef",
    state: "running",
    status: "Up 3 hours",
    created: NOW,
    ports: [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }],
    usage: { cpuPercent: 0.4, memUsage: 31_457_280, memLimit: 2_147_483_648 },
  },
  {
    id: "deadbeef0123456789abcd",
    name: "worker",
    image: "example.com/worker:3",
    imageId: "sha256:fedcba9876543210",
    state: "exited",
    status: "Exited (0) 2 days ago",
    created: NOW,
    ports: [],
  },
]

const containerDetail: ContainerDetail = {
  id: CONTAINER_ID,
  name: "/web",
  image: "nginx:1.27",
  imageId: "sha256:0123456789abcdef",
  created: NOW,
  state: "running",
  running: true,
  startedAt: NOW,
  exitCode: 0,
  restartPolicy: "unless-stopped",
  env: [{ name: "NGINX_HOST", value: "<redacted>" }],
  ports: [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }],
  mounts: [{ type: "bind", source: "/srv/www", destination: "/usr/share/nginx/html", rw: false }],
  networks: [{ network: "apps", networkId: "feedface0123", container: "web", ipv4: "172.20.0.2" }],
}

const networks: DockerNetwork[] = [
  { id: "feedface0123", name: "apps", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.20.0.0/16"], containers: ["web"], created: NOW },
  { id: "0123456789ab", name: "bridge", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.17.0.0/16"], containers: [], created: NOW },
]

const npmStatus: InstanceStatus = {
  deployed: true,
  containerId: "npm0123456789",
  image: "jc21/nginx-proxy-manager:2.12.6",
  pinnedImage: "jc21/nginx-proxy-manager:2.12.6",
  running: true,
  apiReady: true,
  public: "no",
  adminEmail: "admin@example.com",
  upgradeAvailable: false,
}

function change(over: Partial<Change>): Change {
  return {
    id: FIREWALL_CHANGE_ID,
    hostId: HOST_ID,
    action: "firewall.rule.add",
    version: 1,
    class: "revertible",
    state: "pending",
    actor: "user:admin",
    createdAt: NOW,
    updatedAt: NOW,
    revertDeadline: null,
    output: null,
    error: null,
    compensatedBy: null,
    parentChangeId: null,
    owner: null,
    repairedBy: null,
    repairOutcome: null,
    override: null,
    ...over,
  }
}

function plan(over: Partial<Plan>): Plan {
  return {
    planId: "plan-fixture",
    hostId: HOST_ID,
    action: "firewall.rule.add",
    version: 1,
    inputHash: "sha256:fixture",
    diff: [],
    guards: [],
    expiresAt: "2099-01-01T00:00:00Z",
    confirmation: null,
    ...over,
  }
}

const LOCKOUT = "firewall.lockout"

function firewallPlan(overrides: unknown): Plan {
  const overridden = Array.isArray(overrides) && overrides.includes(LOCKOUT)
  return plan({
    planId: "plan-firewall-rule-add",
    action: "firewall.rule.add",
    diff: [
      { op: " ", text: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in" },
      { op: "+", text: "### tuple ### deny tcp 22 0.0.0.0/0 any 198.51.100.0/24 in" },
      { op: "-", text: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in" },
    ],
    guards: [
      {
        id: LOCKOUT,
        ok: overridden,
        message: "The rule would block the address this session reaches the host from.",
        overridable: true,
        overridden,
      },
      { id: "firewall.persist", ok: true, message: "UFW's files and the loaded rules stay in sync.", overridable: false, overridden: false },
    ],
  })
}

type Reply = { status?: number; json?: unknown; sse?: string }

function problem(status: number, code: string, detail: string): Reply {
  return { status, json: { type: "about:blank", title: detail, status, detail, code } }
}

function sse(events: { event: string; data: unknown }[]): Reply {
  return { sse: events.map((e) => `event: ${e.event}\ndata: ${JSON.stringify(e.data)}\n\n`).join("") }
}

function bodyOf(route: Route): { input?: Record<string, unknown> } {
  const text = route.request().postData()
  return text === null ? {} : (JSON.parse(text) as { input?: Record<string, unknown> })
}

const ACTION = /^\/api\/v1\/(?:hosts\/([^/]+)\/)?actions\/([^/]+)\/v\d+\/(read|plan|apply)$/
const RESOURCE = /^\/api\/v1\/hosts\/[^/]+\/resources\/([^/]+)\/([^/]+)$/
const STREAM = /^\/api\/v1\/hosts\/[^/]+\/streams\/docker\/(logs|stats)$/
const AVAILABILITY = /^\/api\/v1\/hosts\/[^/]+\/modules\/([^/]+)\/availability$/
const CHANGE = /^\/api\/v1\/changes\/([^/]+)(?:\/(confirm|revert|steps))?$/

const READS: Record<string, () => unknown> = {
  "core.host.list": () => ({ hosts }),
  "npm.instance.list": () => ({ instances: [] }),
  "panel.exposure.status": () => ({ exposed: false }),
  "npm.instance.status": () => npmStatus,
  "docker.container.inspect": () => containerDetail,
}

const RESOURCES: Record<string, unknown> = {
  "firewall.status": firewallStatus,
  "firewall.docker": firewallDocker,
  "firewall.exposure": exposure,
  "docker.containers": { containers },
  "docker.networks": { networks },
}

function plans(name: string, input: Record<string, unknown>): Reply {
  switch (name) {
    case "firewall.rule.add":
      return { json: firewallPlan(input.overrides) }
    case "docker.container.stop":
      return {
        json: plan({
          planId: "plan-docker-stop",
          action: "docker.container.stop",
          diff: [{ op: "-", text: "container web: running" }, { op: "+", text: "container web: exited" }],
          guards: [{ id: "docker.not_owned", ok: true, message: "No other module owns the container.", overridable: false, overridden: false }],
          confirmation: "web",
        }),
      }
    case "npm.expose.container":
      return {
        json: plan({
          planId: "plan-npm-expose",
          hostId: null,
          action: "npm.expose.container",
          diff: [
            { op: "+", text: "proxy host app.example.com -> web:80 on network apps" },
            { op: "+", text: "certificate app.example.com (Let's Encrypt)" },
          ],
          guards: [{ id: "npm.network", ok: true, message: "Nginx Proxy Manager shares the network apps with web.", overridable: false, overridden: false }],
        }),
      }
    default:
      return problem(404, "action.unknown", `The fixtures do not plan ${name}.`)
  }
}

function applies(name: string, changes: Map<string, Change>): Reply {
  let applied: Change
  switch (name) {
    case "firewall.rule.add":
      applied = change({ state: "pending", revertDeadline: new Date(Date.now() + 60_000).toISOString() })
      break
    case "npm.expose.container":
      applied = change({ id: EXPOSE_CHANGE_ID, hostId: null, action: "npm.expose.container", class: "composite", state: "completed" })
      break
    default:
      return problem(404, "action.unknown", `The fixtures do not apply ${name}.`)
  }
  changes.set(applied.id, applied)
  return { json: applied }
}

// Fixtures is what a test reads back from mockApi.
export interface Fixtures {
  // The "<method> <path>" of every API request no handler answered; a test
  // fails on any, because a 404 would render an error alert and skew an audit.
  unhandled: string[]
}

// mockApi answers every /api/ request of page from the fixtures above, so a
// browser pass needs no controller. A signed-out page gets auth.required from
// the session probe and the login form answers auth.invalid.
export async function mockApi(page: Page, opts: { signedIn: boolean }): Promise<Fixtures> {
  const fixtures: Fixtures = { unhandled: [] }
  const changes = new Map<string, Change>()
  await page.route(
    (url) => url.pathname.startsWith("/api/"),
    async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname
      const reply = answer(method, path, route, opts.signedIn, changes)
      if (reply === null) {
        fixtures.unhandled.push(`${method} ${path}`)
        await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify(problem(404, "not_found", "No fixture").json) })
        return
      }
      if (reply.sse !== undefined) {
        await route.fulfill({ status: 200, contentType: "text/event-stream", body: reply.sse })
        return
      }
      if (reply.json === undefined) {
        await route.fulfill({ status: reply.status ?? 204 })
        return
      }
      await route.fulfill({ status: reply.status ?? 200, contentType: "application/json", body: JSON.stringify(reply.json) })
    },
  )
  return fixtures
}

function answer(method: string, path: string, route: Route, signedIn: boolean, changes: Map<string, Change>): Reply | null {
  if (path === "/api/v1/auth/session") {
    return signedIn ? { json: session } : problem(401, "auth.required", "Sign in to continue.")
  }
  if (path === "/api/v1/auth/login") return problem(401, "auth.invalid", "Login failed.")
  if (path === "/api/v1/auth/logout") return { status: 204 }
  if (path === "/api/v1/events") return { status: 204 }
  if (path === "/api/v1/controller/listeners") {
    return { json: { tailscaleBound: false, message: "No Tailscale address: the controller listens on loopback only." } }
  }
  const action = ACTION.exec(path)
  if (action !== null && method === "POST") {
    const name = action[2] ?? ""
    switch (action[3]) {
      case "read": {
        const read = READS[name]
        return read === undefined ? null : { json: { output: read() } }
      }
      case "plan":
        return plans(name, bodyOf(route).input ?? {})
      default:
        return applies(name, changes)
    }
  }
  const resource = RESOURCE.exec(path)
  if (resource !== null && method === "GET") {
    const found = RESOURCES[`${resource[1]}.${resource[2]}`]
    return found === undefined ? null : { json: found }
  }
  const stream = STREAM.exec(path)
  if (stream !== null) {
    return stream[1] === "logs"
      ? sse([
          { event: "data", data: { stream: "stdout", text: "172.20.0.1 - - GET / 200\n" } },
          { event: "data", data: { stream: "stderr", text: "[warn] upstream timed out\n" } },
          { event: "end", data: { reason: "done" } },
        ])
      : sse([
          { event: "data", data: { read: NOW, cpuPercent: 0.4, memUsage: 31_457_280, memLimit: 2_147_483_648, netRx: 1024, netTx: 2048, pids: 4 } },
          { event: "end", data: { reason: "done" } },
        ])
  }
  const availability = AVAILABILITY.exec(path)
  if (availability !== null && method === "GET") {
    return { json: { module: availability[1], available: true, reason: null, actions: [] } }
  }
  const changeMatch = CHANGE.exec(path)
  if (changeMatch !== null) {
    const found = changes.get(decodeURIComponent(changeMatch[1] ?? ""))
    if (found === undefined) return null
    const verb = changeMatch[2]
    if (verb === "steps") return { json: { steps: [] } }
    if (verb === "confirm" || verb === "revert") {
      const next = { ...found, state: verb === "confirm" ? "confirmed" : "reverted", revertDeadline: null }
      changes.set(next.id, next)
      return { json: next }
    }
    return { json: found }
  }
  return null
}
```

Create `web/e2e/a11y.pw.ts`:

```ts
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"
import { expect, test, type Page } from "@playwright/test"
import { CONTAINER_ID, HOST_ID, mockApi } from "./fixtures"

// The port reaches the tests through the environment: Vite picks a free one
// and playwright.config.ts captures it from the server's output.
test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const shots = fileURLToPath(new URL("../../docs/accessibility/", import.meta.url))

type Theme = "light" | "dark"
const THEMES: Theme[] = ["light", "dark"]

interface View {
  // The <flow> of the screenshot names; the flows of spec section 9 are
  // login, hosts, firewall-change-*, container-* and expose-plan.
  slug: string
  signedIn: boolean
  open: (page: Page) => Promise<void>
  // The title of the change dialog this view ends in, and the controls whose
  // accessible names it must offer.
  dialog?: { title: string; controls: [role: "button" | "checkbox" | "textbox" | "spinbutton", name: string][] }
}

async function openFirewallPlan(page: Page): Promise<void> {
  await page.goto(`/hosts/${HOST_ID}/firewall`)
  await page.getByRole("button", { name: "Add rule" }).click()
  await page.getByLabel("To port").fill("443")
  await page.getByRole("button", { name: "Plan", exact: true }).click()
  await expect(page.getByRole("checkbox", { name: "Override firewall.lockout" })).toBeVisible()
}

async function openContainerPage(page: Page): Promise<void> {
  await page.goto(`/hosts/${HOST_ID}/docker/containers/${CONTAINER_ID}`)
  await expect(page.getByRole("form", { name: "Expose container" })).toBeVisible()
  await expect(page.getByText("[warn] upstream timed out")).toBeVisible()
  await expect(page.getByLabel("Container usage")).toBeVisible()
}

const VIEWS: View[] = [
  {
    slug: "login",
    signedIn: false,
    open: async (page) => {
      await page.goto("/login")
      await page.getByLabel("Username").fill("admin")
      await page.getByLabel("Password", { exact: true }).fill("placeholder")
      await page.getByLabel("Authentication code").fill("000000")
      await page.getByRole("button", { name: "Log in" }).click()
      await expect(page.getByRole("alert")).toContainText("Login failed")
    },
  },
  {
    slug: "hosts",
    signedIn: true,
    open: async (page) => {
      await page.goto("/")
      await expect(page.getByRole("table")).toContainText("This controller")
      await expect(page.getByRole("table")).toContainText("Journal ingestion failing")
    },
  },
  {
    slug: "firewall-change-plan",
    signedIn: true,
    open: openFirewallPlan,
    dialog: {
      title: "Add a rule",
      controls: [
        ["checkbox", "Override firewall.lockout"],
        ["spinbutton", "Revert after (seconds)"],
        ["button", "Apply"],
      ],
    },
  },
  {
    slug: "firewall-change-applied",
    signedIn: true,
    open: async (page) => {
      await openFirewallPlan(page)
      await page.getByRole("checkbox", { name: "Override firewall.lockout" }).click()
      await page.getByRole("button", { name: "Apply" }).click()
      await expect(page.getByRole("timer")).toContainText("Reverts automatically in")
    },
    dialog: {
      title: "Add a rule",
      controls: [
        ["button", "Confirm"],
        ["button", "Revert"],
      ],
    },
  },
  {
    slug: "container-actions-list",
    signedIn: true,
    open: async (page) => {
      await page.goto(`/hosts/${HOST_ID}/docker`)
      await expect(page.getByRole("table", { name: "Docker containers" })).toContainText("worker")
    },
  },
  {
    slug: "container-actions-dialog",
    signedIn: true,
    open: async (page) => {
      await page.goto(`/hosts/${HOST_ID}/docker`)
      await page.getByRole("button", { name: "Stop web" }).click()
      await expect(page.getByRole("textbox", { name: "Type web to confirm" })).toBeVisible()
    },
    dialog: {
      title: "Stop web",
      controls: [
        ["textbox", "Type web to confirm"],
        ["button", "Apply"],
      ],
    },
  },
  {
    slug: "container-page",
    signedIn: true,
    open: openContainerPage,
  },
  {
    slug: "expose-plan",
    signedIn: true,
    open: async (page) => {
      await openContainerPage(page)
      await page.getByLabel("Domains").fill("app.example.com")
      await page.getByRole("form", { name: "Expose container" }).getByRole("button", { name: "Plan" }).click()
      await expect(page.getByRole("dialog", { name: "Expose web at app.example.com" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Apply" })).toBeEnabled()
    },
    dialog: {
      title: "Expose web at app.example.com",
      controls: [["button", "Apply"]],
    },
  },
]

// A view is ready for measuring once its dialog animation has finished, so
// axe never samples a half-transparent element.
async function settle(page: Page, theme: Theme): Promise<void> {
  await expect(page.locator("html")).toHaveAttribute("data-mode", theme)
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => undefined)),
    ),
  )
}

async function visit(page: Page, view: View, theme: Theme): Promise<string[]> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: view.signedIn })
  await view.open(page)
  await settle(page, theme)
  return fixtures.unhandled
}

interface AxeNode {
  target: unknown[]
  any: { message: string }[]
}

async function contrastViolations(page: Page): Promise<string[]> {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: { id: string; nodes: AxeNode[] }[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "rule", values: ["color-contrast"] }, resultTypes: ["violations"] })
    return result.violations
  })
  return violations.flatMap((v) => v.nodes.map((n) => `${v.id}: ${n.target.join(" ")}: ${n.any[0]?.message ?? ""}`))
}

async function scrollExtent(page: Page): Promise<{ scrollWidth: number; clientWidth: number }> {
  return page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }))
}

// The page may not scroll sideways while an open dialog still clips its own
// content, because a dialog is fixed; this lists what lies outside the
// viewport, leaving out what a scrolling ancestor inside the dialog holds.
async function clippedInDialog(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const dialog = document.querySelector('[role="dialog"]')
    if (dialog === null) return []
    const width = document.documentElement.clientWidth
    const clipped: string[] = []
    for (const el of Array.from(dialog.querySelectorAll("*"))) {
      const box = el.getBoundingClientRect()
      if (box.width === 0 || (box.left >= -0.5 && box.right <= width + 0.5)) continue
      let scrolls = false
      for (let parent = el.parentElement; parent !== null && parent !== dialog; parent = parent.parentElement) {
        const overflow = getComputedStyle(parent).overflowX
        if (overflow === "auto" || overflow === "scroll") scrolls = true
      }
      if (!scrolls) clipped.push(`${el.tagName.toLowerCase()}.${el.getAttribute("class") ?? ""} spans ${Math.round(box.left)} to ${Math.round(box.right)} of ${width}`)
    }
    return clipped
  })
}

const CHECKS = [
  // Axe's contrast rules at the size of a desktop window.
  { check: "contrast", viewport: { width: 1280, height: 800 }, scale: 1 },
  // WCAG 1.4.10 sets the reflow width at 320 CSS px.
  { check: "reflow-320", viewport: { width: 320, height: 640 }, scale: 1 },
  // 200 % browser zoom on a 1280x800 window is a 640x400 CSS px viewport with
  // twice the device pixels; emulating it that way, rather than with CSS
  // zoom, keeps media queries and the layout viewport what a zoomed browser
  // reports.
  { check: "zoom-200", viewport: { width: 640, height: 400 }, scale: 2 },
] as const

for (const { check, viewport, scale } of CHECKS) {
  test.describe(check, () => {
    test.use({ viewport, deviceScaleFactor: scale })
    for (const theme of THEMES) {
      for (const view of VIEWS) {
        test(`${view.slug} ${theme}`, async ({ page }) => {
          const unhandled = await visit(page, view, theme)
          await page.screenshot({ path: `${shots}${view.slug}-${theme}-${check}.png` })
          expect(unhandled, "API requests without a fixture").toEqual([])
          if (check === "contrast") {
            expect(await contrastViolations(page)).toEqual([])
          } else {
            const { scrollWidth, clientWidth } = await scrollExtent(page)
            expect(scrollWidth, `scrollWidth ${scrollWidth} against clientWidth ${clientWidth}`).toBeLessThanOrEqual(clientWidth)
            expect(await clippedInDialog(page), "dialog content outside the viewport").toEqual([])
          }
        })
      }
    }
  })
}

// WCAG 1.4.11: the boundary of a text field must reach 3:1 against what lies
// behind it, by its border or by its fill. Axe does not measure this. What lies
// behind a field is the stack of its ancestors' backgrounds over white.
async function weakFieldBoundaries(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const canvas = document.createElement("canvas")
    canvas.width = 1
    canvas.height = 1
    const context = canvas.getContext("2d", { willReadFrequently: true })
    if (context === null) return ["no canvas to read colours with"]
    const rgba = (css: string): number[] => {
      context.clearRect(0, 0, 1, 1)
      context.fillStyle = "#000"
      context.fillStyle = css
      context.fillRect(0, 0, 1, 1)
      const [r = 0, g = 0, b = 0, a = 0] = context.getImageData(0, 0, 1, 1).data
      return [r, g, b, a / 255]
    }
    const over = (top: number[], bottom: number[]): number[] => {
      const alpha = top[3] ?? 1
      return [0, 1, 2].map((i) => (top[i] ?? 0) * alpha + (bottom[i] ?? 0) * (1 - alpha))
    }
    const luminance = (c: number[]): number => {
      const [r = 0, g = 0, b = 0] = c.map((v) => {
        const unit = v / 255
        return unit <= 0.03928 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4
      })
      return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
    const ratio = (a: number[], b: number[]): number => {
      const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x)
      return ((light ?? 0) + 0.05) / ((dark ?? 0) + 0.05)
    }
    const backdrop = (el: Element): number[] => {
      const chain: Element[] = []
      for (let parent = el.parentElement; parent !== null; parent = parent.parentElement) chain.unshift(parent)
      return chain.reduce((behind, parent) => over(rgba(getComputedStyle(parent).backgroundColor), behind), [255, 255, 255])
    }
    const weak: string[] = []
    const fields = document.querySelectorAll(
      'input:not([type="radio"]):not([type="checkbox"]):not([type="hidden"]):not(:disabled), textarea:not(:disabled), select:not(:disabled), [role="combobox"]:not(:disabled)',
    )
    for (const el of Array.from(fields)) {
      if (el.getBoundingClientRect().width === 0) continue
      const style = getComputedStyle(el)
      const behind = backdrop(el)
      const border = Number.parseFloat(style.borderTopWidth) > 0 ? ratio(over(rgba(style.borderTopColor), behind), behind) : 1
      const fill = ratio(over(rgba(style.backgroundColor), behind), behind)
      if (Math.max(border, fill) < 3) {
        weak.push(`${el.tagName.toLowerCase()}#${el.id} border ${border.toFixed(2)}:1, fill ${fill.toFixed(2)}:1`)
      }
    }
    return weak
  })
}

test.describe("non-text contrast", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS) {
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        expect(await weakFieldBoundaries(page), "text fields whose boundary is under 3:1").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})

// WCAG 2.4.7: every control that Tab reaches must look different while it
// has focus, which darkraise-ui does with a ring (box-shadow) or an outline.
// A press that leaves the page puts focus on the body, so four presses reach
// at least one control from wherever the view left focus.
async function focusIndicators(page: Page): Promise<{ reached: number; bare: string[] }> {
  // Transitions would leave the styles read right after a blur half-way.
  await page.addStyleTag({ content: "*, *::before, *::after { transition: none !important; animation: none !important; }" })
  let reached = 0
  const bare: string[] = []
  for (let press = 0; press < 4; press++) {
    await page.keyboard.press("Tab")
    const found = await page.evaluate(() => {
      const el = document.activeElement
      if (!(el instanceof HTMLElement) || el === document.body) return null
      const look = () => {
        const style = getComputedStyle(el)
        return [style.outlineStyle, style.outlineWidth, style.outlineColor, style.boxShadow, style.borderColor].join(" ")
      }
      const focused = look()
      el.blur()
      const idle = look()
      el.focus()
      return { name: `${el.tagName.toLowerCase()} ${el.getAttribute("aria-label") ?? el.textContent?.trim().slice(0, 40) ?? ""}`, visible: focused !== idle }
    })
    if (found === null) continue
    reached++
    if (!found.visible) bare.push(found.name)
  }
  return { reached, bare }
}

test.describe("focus visible", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS) {
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        const { reached, bare } = await focusIndicators(page)
        expect(reached, "controls Tab reached").toBeGreaterThan(0)
        expect(bare, "controls without a focus indicator").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})

const NAMED = /^\s*- (button|checkbox|textbox|spinbutton|radio|combobox|link|switch|slider|searchbox)\b(.*)$/

test.describe("change dialog names", () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  for (const theme of THEMES) {
    for (const view of VIEWS.filter((v) => v.dialog !== undefined)) {
      const dialogSpec = view.dialog
      if (dialogSpec === undefined) continue
      test(`${view.slug} ${theme}`, async ({ page }) => {
        const unhandled = await visit(page, view, theme)
        const dialog = page.getByRole("dialog", { name: dialogSpec.title })
        await expect(dialog).toBeVisible()
        await expect(dialog).toHaveAccessibleName(dialogSpec.title)
        await expect(dialog.getByRole("heading", { name: dialogSpec.title })).toBeVisible()
        for (const [role, name] of dialogSpec.controls) {
          await expect(dialog.getByRole(role, { name })).toBeVisible()
        }
        const unnamed = (await dialog.ariaSnapshot())
          .split("\n")
          .filter((line) => {
            const found = NAMED.exec(line)
            return found !== null && !(found[2] ?? "").trimStart().startsWith('"')
          })
        expect(unnamed, "buttons, fields and links without an accessible name").toEqual([])
        await page.screenshot({ path: `${shots}${view.slug}-${theme}-names.png` })
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  }
})
```

Replace `web/tsconfig.node.json` with:

```json
{
  "compilerOptions": {
    "target": "ES2023",
    "lib": ["ES2023", "DOM"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "types": ["node"],
    "skipLibCheck": true,
    "noEmit": true,
    "strict": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["vite.config.ts", "vitest.config.ts", "playwright.config.ts", "e2e"]
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `export E2E_CHROMIUM=<from Step 1, when it was needed>; timeout 600 pnpm -C web exec playwright test -c playwright.config.ts --reporter=line`

Expected: FAIL, and only for the reasons Step 4 fixes. On the tree this plan was drafted against, 27 of the 88 tests fail and 61 pass: `contrast` fails in `login`, `hosts`, `firewall-change-plan`, `firewall-change-applied`, `container-actions-list`, `container-actions-dialog`, `container-page` and `expose-plan` in light mode and in `hosts`, `firewall-change-plan` and `container-actions-dialog` in dark mode (11 tests; for example `Element has insufficient color contrast of 3.63 (foreground color: #ffffff, background color: #3c83f6 ...)` on the primary button and badges, `3.01` on the `text-warning` footer text, `2.9` on `text-success`, `3.4` on `text-destructive` in dark mode); `non-text contrast` fails with `input#login-username border 1.27:1, fill 1.10:1` and its siblings in the 12 tests whose view has a text field; `reflow-320` fails in `firewall-change-plan` and `expose-plan` in both themes with `dialog content outside the viewport` (`div.dr-overlay-header spans 25 to 469 of 320` and the dialog's other children). `zoom-200`, `focus visible` and `change dialog names` pass. After Tasks 13-15 and 20 the exact list can differ; quote the red output verbatim and treat each failure the same way. The run writes screenshots before it asserts, so the PNGs exist even while it is red; the green run overwrites them. A failure that names an unfixtured request (`API requests without a fixture`) is a gap in `fixtures.ts`: add the fixture, never skip the assertion.

- [ ] **Step 4: Implement**

1. `web/src/styles/globals.css`: append the overrides below after the existing `@theme inline` block. They are configuration values, measured against the surfaces of each mode (white `#fff`, card `#18181b`, muted `#f4f4f5`, `#27272a`): light `--primary-fill` 5.4:1 against the white button label, `--destructive` 5.5:1 and `--success` 5.5:1 and `--warning` 5.5:1 as text on `#f4f4f5`; dark `--destructive` 5.0:1 as text on `#27272a` with a near-black `--destructive-foreground` on the fill (6.7:1); `--input` 4.4:1 on `#f4f4f5` and 3.7:1 on the dark card for the text-field border. `!important` is required: `ThemeProvider` writes these tokens as inline styles on `<html>`, which a plain stylesheet rule never beats.

```css
[data-mode="light"] {
  --primary-fill: 217 91% 48% !important;
  --destructive: 0 84% 42% !important;
  --success: 160 84% 24% !important;
  --warning: 38 92% 28% !important;
  --input: 240 4% 46% !important;
}

[data-mode="dark"] {
  --destructive: 0 72% 68% !important;
  --destructive-foreground: 240 10% 4% !important;
  --input: 240 4% 46% !important;
}
```

2. `web/src/components/change-dialog.tsx`, `ChangeDialogBody`: the `div` that wraps the plan (the one whose first child is the `<pre aria-label="Planned changes">`) is a grid item of the dialog's content, and the `pre`'s longest line sets the item's minimum width, so every row of the dialog grows past a 320 px viewport. Add the class `min-w-0` to that `div`'s `className`. Change nothing else in the file. If Task 14 reworked this markup, add `min-w-0` to the outermost element the red output lists as spanning past the viewport and re-run.
3. For any other failure of Step 3, fix it in the owning file by class name or ARIA attribute only. A new colour failure that no override above covers gets a new variable in the same two blocks, with at least 4.5:1 for text and 3:1 for control boundaries against that mode's surfaces; check it with the ratio the failing message prints.
4. Do not edit `a11y.pw.ts` to make a check pass, loosen an assertion or skip a view; a locator renamed by an earlier task is the only edit it may take.

- [ ] **Step 5: Run the test to verify it passes**

Run: `timeout 600 pnpm -C web exec playwright test -c playwright.config.ts --reporter=line`

Expected: PASS with `88 passed`. Then:

```bash
ls docs/accessibility/*.png | wc -l
pgrep -af 'vite --host|chrome-headless' | grep -v pgrep
```

Expected: `56`, and no process listed (Playwright stops its Vite server). Open `docs/accessibility/login-dark-contrast.png`, `docs/accessibility/firewall-change-applied-light-contrast.png` and `docs/accessibility/expose-plan-dark-reflow-320.png` and confirm they show the intended state (the login error alert, the pending change with `Confirm` and `Revert`, the expose dialog inside a 320 px column with nothing cut off). Fixtures use placeholders only; confirm no screenshot shows a real name, address or token.

- [ ] **Step 6: Run the web checks**

Run: `export TMPDIR=/root/.cache/w9-tmp/22 GOTOOLCHAIN=local; timeout 1200 make lint-web test-web; rm -rf "$TMPDIR"`

Expected: ESLint and both `tsc` projects print no error (`tsconfig.node.json` now checks `playwright.config.ts` and `e2e/`), and Vitest passes without running any `*.pw.ts` file.

- [ ] **Step 7: Commit**

```bash
git add web/playwright.config.ts web/e2e/fixtures.ts web/e2e/a11y.pw.ts web/tsconfig.node.json
git commit -m "test(web): add the Playwright accessibility pass"
git add web/src/styles/globals.css web/src/components/change-dialog.tsx
git commit -m "fix(web): meet contrast and reflow in both themes"
git add docs/accessibility
git commit -m "docs(accessibility): add browser pass screenshots"
```

Add every other file Step 4 changed to the second commit.

### Task 23: docs: accessibility conformance record

**Files:**
- Create: `docs/accessibility.md`
- Test: `web/src/test/accessibility-doc.test.ts`
- Modify (only when the manual check of Step 3 finds a defect): `docs/user/first-admin.md`, `docs/user/lockout-recovery.md` (heading levels and link text only)

**Interfaces:**
- Consumes: Contracts "Conformance record" and "Documentation paths"; every `*.a11y.test.tsx` under `web/src` that Tasks 13-15 and 20 add (find them with `rg --files web/src -g '*.a11y.test.tsx'`); `web/e2e/a11y.pw.ts` and the screenshots of Task 22 (names in Task 22's Interfaces); the pages `docs/user/first-admin.md` and `docs/user/lockout-recovery.md` of Task 16.
- Produces: `docs/accessibility.md`, which Task 24's `TestWcDocsLinks` checks for links. The six flow names below are fixed: the test and the document both use them.

**Items:** 41

**Depends on:** Task 22, Task 16
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 0 = 4

- [ ] **Step 1: Write the failing test**

Create `web/src/test/accessibility-doc.test.ts`:

```ts
/// <reference types="node" />
import { readdirSync, readFileSync, existsSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..")

const FLOWS = [
  "Login with TOTP",
  "First-admin and lockout recovery pages",
  "Host list",
  "Firewall change with confirm and revert",
  "Container actions",
  "Expose a container",
]

const CRITERIA: Record<string, string> = {
  "1.1.1": "A",
  "1.3.1": "A",
  "1.4.3": "AA",
  "1.4.4": "AA",
  "1.4.10": "AA",
  "1.4.11": "AA",
  "2.1.1": "A",
  "2.1.2": "A",
  "2.4.2": "A",
  "2.4.3": "A",
  "2.4.7": "AA",
  "3.3.1": "A",
  "3.3.2": "A",
  "4.1.2": "A",
  "4.1.3": "AA",
}

const COLUMNS = ["Criterion", "Level", "Method", "Evidence"]

interface Row {
  flow: string
  criterion: string
  level: string
  method: string
  evidence: string[]
}

function record(): string {
  return readFileSync(path.join(root, "docs", "accessibility.md"), "utf8")
}

// section is the text under the "## <title>" heading, up to the next "## " heading.
function section(doc: string, title: string): string {
  const lines = doc.split("\n")
  const start = lines.findIndex((line) => line === `## ${title}`)
  if (start < 0) return ""
  const end = lines.findIndex((line, i) => i > start && line.startsWith("## "))
  return lines.slice(start + 1, end < 0 ? undefined : end).join("\n")
}

function cells(line: string): string[] {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cell.trim())
}

// tables returns the header and rows of the table under each "### <flow>"
// heading of the Criteria section.
function tables(doc: string): Map<string, { header: string[]; rows: Row[] }> {
  const found = new Map<string, { header: string[]; rows: Row[] }>()
  let flow = ""
  for (const line of section(doc, "Criteria").split("\n")) {
    if (line.startsWith("### ")) {
      flow = line.slice(4).trim()
      found.set(flow, { header: [], rows: [] })
      continue
    }
    const table = found.get(flow)
    if (table === undefined || !line.trim().startsWith("|")) continue
    const row = cells(line)
    if (table.header.length === 0) {
      table.header = row
    } else if (!row.every((cell) => /^:?-+:?$/.test(cell))) {
      const [criterion = "", level = "", method = "", evidence = ""] = row
      table.rows.push({ flow, criterion, level, method, evidence: [...evidence.matchAll(/`([^`]+)`/g)].map((m) => m[1] ?? "") })
    }
  }
  return found
}

function allRows(): Row[] {
  return [...tables(record()).values()].flatMap((t) => t.rows)
}

describe("docs/accessibility.md", () => {
  it("names the six core flows in Scope", () => {
    const scope = section(record(), "Scope")
    for (const flow of FLOWS) expect(scope, `Scope does not name ${flow}`).toContain(flow)
  })

  it("has one table of criteria for each flow, with the four columns", () => {
    const found = tables(record())
    expect([...found.keys()]).toEqual(FLOWS)
    for (const [flow, table] of found) {
      expect(table.header, `table of ${flow}`).toEqual(COLUMNS)
      expect(table.rows.length, `rows of ${flow}`).toBeGreaterThan(0)
    }
  })

  it("accounts for each criterion, at its level, in a table or in Known issues", () => {
    const rows = allRows()
    const seen = new Set<string>()
    for (const row of rows) {
      const number = /^(\d\.\d\.\d+)\b/.exec(row.criterion)?.[1] ?? ""
      expect(Object.keys(CRITERIA), `${row.flow}: unknown criterion "${row.criterion}"`).toContain(number)
      expect(row.level, `${row.flow}: level of ${number}`).toBe(CRITERIA[number])
      seen.add(number)
    }
    for (const found of section(record(), "Known issues").matchAll(/\b(\d\.\d\.\d+)\b/g)) {
      if (found[1] !== undefined && found[1] in CRITERIA) seen.add(found[1])
    }
    expect([...seen].sort(), "criteria that no table and no known issue names").toEqual(Object.keys(CRITERIA).sort())
  })

  it("marks every row automated or manual", () => {
    for (const row of allRows()) {
      expect(["automated", "manual"], `${row.flow}: ${row.criterion}`).toContain(row.method)
    }
  })

  it("cites existing files as evidence on every row", () => {
    for (const row of allRows()) {
      expect(row.evidence.length, `${row.flow}: ${row.criterion} has no evidence`).toBeGreaterThan(0)
      for (const file of row.evidence) {
        expect(existsSync(path.join(root, file)), `${row.flow}: ${row.criterion}: ${file} does not exist`).toBe(true)
      }
    }
  })

  it("cites every accessibility test file under web/src", () => {
    const cited = new Set(allRows().flatMap((row) => row.evidence))
    const tests = readdirSync(path.join(root, "web", "src"), { recursive: true, encoding: "utf8" })
      .filter((file) => file.endsWith(".a11y.test.tsx"))
      .map((file) => `web/src/${file.split(path.sep).join("/")}`)
    expect(tests.length, "no *.a11y.test.tsx file found").toBeGreaterThan(0)
    for (const file of tests) expect(cited, `${file} is not cited as evidence`).toContain(file)
  })

  it("has a Known issues section", () => {
    expect(section(record(), "Known issues").trim()).not.toBe("")
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `export TMPDIR=/root/.cache/w9-tmp/23 GOTOOLCHAIN=local && mkdir -p "$TMPDIR" && timeout 300 pnpm -C web exec vitest run src/test/accessibility-doc.test.ts`

Expected: FAIL, 7 tests, each with `ENOENT: no such file or directory, open '.../docs/accessibility.md'`.

- [ ] **Step 3: Implement**

Write `docs/accessibility.md` in the documentation style of the Global Constraints (plain Markdown, ATX headings, sentence-case headings, second person where it addresses the reader, present tense, no HTML, no mention of a plan, wave, task or register row). Its structure is the one the test reads:

1. `# Accessibility` (the only `#`), then an introduction of a few sentences: the target is WCAG 2.1 level AA; the record covers the six core flows below; each criterion is marked `automated` (a Vitest test with `vitest-axe` or a keyboard test, run by `pnpm -C web test`) or `manual` (the Playwright pass in Chromium against fixtures, run by `pnpm -C web exec playwright test -c playwright.config.ts`, which rewrites the screenshots under `docs/accessibility/`, or a reading of a Markdown page); one `sh` block holds those two commands; one sentence says that `E2E_CHROMIUM` names a Chromium executable when the build Playwright pins is not installed. Say once that the browser pass covers the default theme (blue accent, zinc surface) in light and dark mode and Chromium only.
2. `## Scope`: the six flows, each by the exact name below with one sentence naming its pages and dialogs: `Login with TOTP` (the login page), `First-admin and lockout recovery pages` (`docs/user/first-admin.md` and `docs/user/lockout-recovery.md`), `Host list` (the hosts page and the shell around it), `Firewall change with confirm and revert` (the firewall page, the rule form and the change dialog through its pending, confirm and revert states), `Container actions` (the Docker containers table, its change dialogs and the container page with the firewall allow and deny forms), `Expose a container` (the expose form of the container page and its change dialog). Add one sentence that says what is not covered: other accent or surface choices of the theme switcher, other browsers, and screen readers other than the accessible-name check of the change dialog.
3. `## Criteria`: six subsections, `### Login with TOTP`, `### First-admin and lockout recovery pages`, `### Host list`, `### Firewall change with confirm and revert`, `### Container actions`, `### Expose a container`, in this order. Each holds one table with the header `| Criterion | Level | Method | Evidence |`. A row's Criterion cell starts with the criterion number and its name (for example `1.4.3 Contrast (minimum)`); Level is the criterion's level (A: 1.1.1, 1.3.1, 2.1.1, 2.1.2, 2.4.2, 2.4.3, 3.3.1, 3.3.2, 4.1.2; AA: 1.4.3, 1.4.4, 1.4.10, 1.4.11, 2.4.7, 4.1.3); Method is exactly `automated` or `manual`; Evidence is one or more repository-relative paths, each in backticks, separated by commas. A flow lists the criteria that apply to it, a row for each, and one row never mixes methods: a criterion proven both ways takes two rows. Across the six tables every one of the fifteen criteria appears. Choose the evidence by reading what each file asserts, never by its name:
   - automated, from the `*.a11y.test.tsx` files that cover the flow's pages: `1.1.1`, `1.3.1`, `3.3.2` and `4.1.2` for the `expectNoAxeViolations` runs, `2.1.1`, `2.4.3` and `2.1.2` for the `tabSequence` and `focusedName` tests (Tab order, focus trap with Escape, focus return), `2.4.2` for the `document.title` assertions, `3.3.1` and `4.1.3` for the tests of error alerts and status regions. Every `*.a11y.test.tsx` file appears as evidence at least once; a file for a module page outside the six flows goes under `Container actions` or `Expose a container` when it lives under `docker`, `firewall`, `npm` or `cloudflare`, and under `Host list` otherwise.
   - manual, from the Playwright pass: `1.4.3` cites `web/e2e/a11y.pw.ts` and the `-contrast.png` screenshots of the flow's views in both themes, `1.4.4` the `-zoom-200.png` ones, `1.4.10` the `-reflow-320.png` ones, `1.4.11` and `2.4.7` cite `web/e2e/a11y.pw.ts`, and `4.1.2` for the change dialog also cites the `-names.png` screenshots. The views per flow: `login` for the login flow, `hosts` for the host list, `firewall-change-plan` and `firewall-change-applied` for the firewall change, `container-actions-list`, `container-actions-dialog` and `container-page` for container actions, `container-page` and `expose-plan` for exposing a container.
   - manual, for `First-admin and lockout recovery pages`: `1.3.1` (heading order) and `2.4.2` (one `#` title that names the page) citing `docs/user/first-admin.md` and `docs/user/lockout-recovery.md`; after the table add a sentence saying the pages were read for heading order and for link text that names its target, and when.
   Do not cite a file for a criterion it does not assert. A criterion that no test or screenshot supports is not claimed: list it under `## Known issues` with its number and the test it lacks, and say so in the report.
4. `## Known issues`: `None open at WCAG 2.1 AA.` when every criterion is evidenced, otherwise one bullet per issue that starts with its criterion number. The test requires the section to be non-empty.
5. The manual check of the two documentation pages, before the rows are written: run `rg -n '^#+ ' docs/user/first-admin.md docs/user/lockout-recovery.md` and confirm each file has one `#` first and no heading level that skips (`##` to `####`); run `rg -n '\]\(' docs/user/first-admin.md docs/user/lockout-recovery.md` and confirm every link's text names its target (never `here`, `this page` or a bare URL). Fix a defect in the page itself (heading level or link text only) and then run `export TMPDIR=/root/.cache/w9-tmp/23 GOTOOLCHAIN=local; timeout 600 go test -count=1 ./internal/docs/ -run 'Du'` so the pages still name what Task 16's tests require.

- [ ] **Step 4: Run the test to verify it passes**

Run: `timeout 300 pnpm -C web exec vitest run src/test/accessibility-doc.test.ts`

Expected: PASS, 7 tests. If `accounts for each criterion` fails, the message names the criteria no table and no known issue names.

- [ ] **Step 5: Run the web checks**

Run: `timeout 1200 make lint-web test-web; rm -rf /root/.cache/w9-tmp/23`

Expected: ESLint and both `tsc` projects print no error (the test file carries the line `/// <reference types="node" />` because `tsconfig.json` loads no Node types), and the Vitest suite passes.

- [ ] **Step 6: Commit**

```bash
git add web/src/test/accessibility-doc.test.ts docs/accessibility.md
git commit -m "docs(accessibility): add the WCAG conformance record"
```

When Step 3 fixed a page, commit it separately: `git add docs/user/first-admin.md docs/user/lockout-recovery.md && git commit -m "docs(user): fix heading levels and link text"`.

### Task 24: wave close: fuzz workflow, inventory check, findings and spec rulings

**Files:**
- Test: `internal/docs/links_test.go` (package `docs_test`, beside Task 12's `helpers_test.go`)
- Create: `.github/workflows/fuzz.yml`
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section 9 only)
- Modify: `docs/dforge/registers/2026-10-21-v1-completion.md` (four new rows, written by the `register` script)
- Modify: `docs/security/parsers.md` (only the inventory rows whose package or target name a fuzz task changed)
- Modify (only when `make api-docs` regenerates them): files under `docs/api/`
- Modify (only when `TestWcDocsLinks` names a broken link): the Markdown page that holds it, the link target only

**Interfaces:**
- Consumes: `dcRoot` and `dcLinks` of Contracts "Docs checks" (Task 12); `scripts/fuzz.sh --check` and `docs/security/parsers.md` (Task 1) with the targets of Tasks 5-10; the pages of Contracts "Documentation paths" (Tasks 16-19) and `docs/accessibility.md` (Task 23); `make api-docs` (Task 3); the action SHAs `.github/workflows/ci.yml` pins (read, not edited); the plan rulings W9-R1 to W9-R10 in the plan file `docs/dforge/plans/2026-11-05-v1-w9-hardening.md` under Assumptions, **Plan rulings**.
- Produces: Contracts "Fuzz workflow"; `TestWcDocsLinks`; the spec's section 9 sub-list `Plan rulings (wave 9)`; four register rows for the adjacent findings (`toolVariant`, unlabelled clipboard inputs, `policy.Apply` with a repeated member, the audit log's focus drop). No Go identifier other than `TestWcDocsLinks`, `wcRequiredPages` and `wcPages` (prefix `Wc`).

**Items:** 38, 42

**Depends on:** Task 1, Task 3, Task 5, Task 6, Task 7, Task 8, Task 9, Task 10, Task 12, Task 16, Task 17, Task 18, Task 19, Task 20, Task 23
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 1 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/docs/links_test.go`:

```go
package docs_test

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wcRequiredPages are the pages the documentation is not complete without;
// walking the directories alone would pass when a page was never written.
var wcRequiredPages = []string{
	"docs/accessibility.md",
	"docs/api/README.md",
	"docs/dev/module-guide.md",
	"docs/user/README.md",
	"docs/user/administration.md",
	"docs/user/first-admin.md",
	"docs/user/install.md",
	"docs/user/lockout-recovery.md",
	"docs/user/uninstall.md",
	"docs/user/modules/cloudflare.md",
	"docs/user/modules/derp.md",
	"docs/user/modules/docker.md",
	"docs/user/modules/firewall.md",
	"docs/user/modules/hostinfo.md",
	"docs/user/modules/npm.md",
	"docs/user/modules/panel.md",
	"docs/user/modules/tailnet.md",
	"docs/user/modules/tailscale.md",
}

// wcPages lists every Markdown page under the documentation directories, as
// slash paths relative to the repository root, and the conformance record.
func wcPages(t *testing.T, root string) []string {
	t.Helper()
	var pages []string
	for _, dir := range []string{"docs/user", "docs/dev", "docs/api"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			pages = append(pages, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("docs: walk %s: %v", dir, err)
		}
	}
	return append(pages, "docs/accessibility.md")
}

// TestWcDocsLinks checks that every relative link of the user documentation,
// the module guide, the API reference and the conformance record resolves to
// a file in the repository, and that none of the required pages is missing.
func TestWcDocsLinks(t *testing.T) {
	pages := wcPages(t, dcRoot(t))
	for _, want := range wcRequiredPages {
		if !slices.Contains(pages, want) {
			t.Errorf("docs: the page %s does not exist", want)
		}
	}
	for _, rel := range pages {
		t.Run(rel, func(t *testing.T) { dcLinks(t, rel) })
	}
}
```

- [ ] **Step 2: Run the test to verify it can fail**

On the merged tree every page exists, so the test is red only through a broken link. Prove it by breaking one on purpose and undoing it:

```bash
export TMPDIR=/root/.cache/w9-tmp/24 GOTOOLCHAIN=local && mkdir -p "$TMPDIR"
printf '\n[broken](missing-page.md)\n' >> docs/user/README.md
timeout 300 go test -count=1 -run '^TestWcDocsLinks$' ./internal/docs/
git checkout -- docs/user/README.md
```

Expected: FAIL on the subtest `TestWcDocsLinks/docs/user/README.md`, the message naming `missing-page.md`; no other subtest fails. Quote it. Then run `timeout 300 go test -count=1 -run '^TestWcDocsLinks$' ./internal/docs/` on the clean tree. A FAIL there names a real broken link or a missing page (`docs: the page <path> does not exist`): fix the link in the page that holds it, or report the missing page as BLOCKED for the task that owns it.

- [ ] **Step 3: Check the inventory and the generated reference**

```bash
timeout 120 bash scripts/fuzz.sh --check
timeout 120 bash scripts/fuzz.test.sh
timeout 300 make api-docs
git status --short docs/api
timeout 600 go test -count=1 -run '^TestAdAPIDocsCurrent$' ./internal/apidocs/
```

Expected: `fuzz: inventory ok (57 targets)`, 57 being the distinct package and target pairs of Contracts "Fuzz targets"; `scripts/fuzz.test.sh` passes; `git status --short docs/api` prints nothing, or the files `make api-docs` regenerated, which this task commits; `TestAdAPIDocsCurrent` passes. When `fuzz.sh --check` prints `fuzz: <package>: no target <target>` or `fuzz: <package>: <target> is not in the inventory`, a fuzz task named a package or target differently from Contracts: correct the row in `docs/security/parsers.md` (the `## Fuzzed parsers` table only; never a test file) until the check prints `inventory ok`. A target that exists in no package is a missing fuzz target: report it BLOCKED.

- [ ] **Step 4: Add the fuzz workflow**

The check is red now: `test -f .github/workflows/fuzz.yml || echo missing` prints `missing`. `.github/workflows/fuzz.yml` is configuration (Contracts "Fuzz workflow"); create it with exactly this content, now that Step 3 shows every inventory target exists. The SHAs are the ones `ci.yml` pins (`rg -n 'uses: actions/' .github/workflows/ci.yml` shows them; stop and report when they differ from the ones below). The matrix and budgets follow W9-R2, and the nightly job's `timeout-minutes` follows the ruling that no job exceeds GitHub's six-hour limit.


```yaml
name: Fuzz

on:
  push:
  schedule:
    - cron: "41 2 * * *"
  workflow_dispatch:

permissions:
  contents: read

env:
  GOTOOLCHAIN: local

jobs:
  inventory:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
          # The runner caches an older 1.27.x than go.mod's toolchain line.
          check-latest: true
      - run: bash scripts/fuzz.test.sh
      - run: bash scripts/fuzz.sh --check

  fuzz:
    needs: inventory
    runs-on: ubuntu-24.04
    # Every push runs each target for 30 s in four shards; the nightly run
    # gives each target 10 minutes in twelve shards, so no job nears GitHub's
    # six-hour limit.
    timeout-minutes: ${{ github.event_name == 'schedule' && 300 || 60 }}
    strategy:
      fail-fast: false
      matrix:
        shard: ${{ fromJSON(github.event_name == 'schedule' && '[1,2,3,4,5,6,7,8,9,10,11,12]' || '[1,2,3,4]') }}
    env:
      FUZZ_TIME: ${{ github.event_name == 'schedule' && '10m' || '30s' }}
      FUZZ_SHARDS: ${{ github.event_name == 'schedule' && '12' || '4' }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
          # The runner caches an older 1.27.x than go.mod's toolchain line.
          check-latest: true
      - run: bash scripts/fuzz.sh --time "$FUZZ_TIME" --shard "${{ matrix.shard }}/$FUZZ_SHARDS"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: fuzz-failures-${{ matrix.shard }}
          path: "**/testdata/fuzz/**"
          if-no-files-found: ignore
```

Then run:

```bash
timeout 60 python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/fuzz.yml"))'
test -f .github/workflows/fuzz.yml || echo missing
```

Expected: no output from either (if PyYAML is not installed, say so in the report and skip the parse).

- [ ] **Step 5: Write the spec rulings**

Run this check first; it prints one line per gap and is red now:

```bash
spec=docs/dforge/specs/2026-10-21-v1-completion-design.md
section=$(sed -n '/^### 9\. Hardening/,/^### 10\. Release/p' "$spec")
bullet() { awk -v start="$1" 'index($0, "- **" start) == 1 { p = 1; print; next } /^- \*\*/ { p = 0 } p' <<<"$section"; }
for n in 1 2 3 4 5 6 7 8 9 10; do
  [ "$(grep -c "^  - \*\*W9-R$n " <<<"$section")" = 1 ] || echo "section 9 lacks W9-R$n"
done
grep -qF -- '- **Plan rulings (wave 9).**' <<<"$section" || echo "section 9 lacks the Plan rulings sub-list"
bullet 'Fuzzing' | grep -qF -- 'scripts/fuzz.sh --check' || echo "Fuzzing bullet lacks scripts/fuzz.sh --check"
bullet 'Upgrade matrix' | grep -qF -- 'upgrade_sources.txt' || echo "Upgrade matrix bullet lacks upgrade_sources.txt"
bullet 'Footprint' | grep -qF -- 'eleven hosts' || echo "Footprint bullet lacks eleven hosts"
bullet 'Accessibility' | grep -qF -- 'web/e2e/a11y.pw.ts' || echo "Accessibility bullet lacks web/e2e/a11y.pw.ts"
```

Then edit only section 9 of the spec, wrapping lines at about 76 columns like the text around them:

1. After the `Documentation (R78, R79)` bullet, before `### 10. Release (R80, R81)`, add the bullet `- **Plan rulings (wave 9).** Wave 9 (plan `v1-w9-hardening`) settles these gaps and underdetermined points of the text above:` followed by ten nested bullets, each indented two spaces and starting `- **W9-R<n> <name>.**`, with the ruling text of the plan's Assumptions copied word for word (re-wrapped only). This is the format of the `Wave 8 rulings` bullet of section 8. W9-R4 and W9-R5 stay in the sub-list; the Documentation bullet is not edited.
2. Fuzzing bullet: replace "a test fails when a parser in the inventory has no target" with the statement that `bash scripts/fuzz.sh --check`, which `fuzz.yml`'s `inventory` job and `make fuzz` run first, fails when a row names a target that does not exist and when a fuzz function has no row; state that `docs/security/parsers.md` has the sections `## Fuzzed parsers` (columns Parser, Input, Package, Target, Tags) and `## Not fuzzed` (columns Parser, Input, Reason); state that every push runs each target 30 s in four shards and the nightly run runs each target 10 minutes in twelve shards on GitHub-hosted `ubuntu-24.04`; state that a defect a target finds in a non-frozen file is fixed in the same task with the failing input committed under `testdata/fuzz/` and a named unit test, and in a frozen file the integrator fixes it on `main` (W9-R1 to W9-R3).
3. Upgrade matrix bullet: state that the baseline labelled `1.0.0` is the first line of `test/harness/upgrade_sources.txt`, built from the commit wave 9 starts from, that each later real `v1.*` tag is added as a line, and that for each source the test installs controller and agent at that version, rolls out `1.1.0` while `hostinfo.summary` answers on the agent host one minor behind, then rolls out `1.1.1`, which rolls back within two minutes and leaves the fleet at `1.1.0` (W9-R7, W9-R8).
4. Footprint bullet: state that megabytes are decimal (40 000 000 and 150 000 000 bytes of `VmRSS`), that the controller manages its own host, the agent VM and nine extra agents on the agent VM, which makes eleven hosts, and that `--state-dir` exists only in `harness` builds, binds to a package variable so the nil-command rollback fallback honours it, and refuses `--dev` (W9-R9, W9-R10).
5. Accessibility bullet: state that the automated checks are the `*.a11y.test.tsx` files using `expectNoAxeViolations`, `tabSequence` and `focusedName` plus page titles through `usePageTitle`, that the manual pass is `web/e2e/a11y.pw.ts` (`pnpm -C web exec playwright test -c playwright.config.ts`), which runs axe's contrast rules in both themes, checks 200 % zoom and 320 px reflow, reads the change dialog's accessible names and saves screenshots under `docs/accessibility/`, that contrast fixes are CSS variable overrides in `web/src/styles/globals.css`, and that the first-admin and lockout-recovery pages are checked by hand for heading order and link text (W9-R6).

Run the check again; it prints nothing. Then `git diff --stat` shows the spec as the only file changed by this step.

- [ ] **Step 6: Record the adjacent finding**

Run `bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register --help` first, then look up the current lines with `rg -n 'func toolVariant' internal/modules/firewall/loaded/` and `rg -n 'TrimSpace' internal/modules/firewall/preflight.go`. The check is red now: `grep -c toolVariant docs/dforge/registers/2026-10-21-v1-completion.md` prints `0`. Add one row (use the line numbers just found):

```bash
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register add docs/dforge/registers/2026-10-21-v1-completion.md "w9 firewall (minor): internal/modules/firewall/loaded/read.go:153 — toolVariant reads the first line of iptables --version without strings.TrimSpace, while checkTools in internal/modules/firewall/preflight.go:157 trims first, so output with a leading blank line is read differently by the two; found by FuzzToolVariant, which asserts only toolVariant's own property" --state open
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register add docs/dforge/registers/2026-10-21-v1-completion.md "w9 web (minor, a11y): the read-only clipboard inputs in web/src/pages/create-token-dialog.tsx and web/src/modules/tailnet/keys-page.tsx have no accessible label, the defect wave 9 fixed in the Add host dialog; neither page is a core flow of spec section 9" --state open
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register add docs/dforge/registers/2026-10-21-v1-completion.md "w9 tailnet (minor): internal/modules/tailnet/policy policy.Apply does not converge on a policy document whose object names a member twice: hujson keeps both members and an edit such as delete reaches only the first, so a second delete /a on {\"a\":1,\"a\":2} still changes the text; found by FuzzApply, which skips such documents in its convergence check" --state open
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register add docs/dforge/registers/2026-10-21-v1-completion.md "w9 web (minor, a11y): the Load older entries button of web/src/pages/audit.tsx unmounts when the last page loads, so keyboard focus drops to the document body; the audit log is not a core flow of spec section 9" --state open
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register check docs/dforge/registers/2026-10-21-v1-completion.md
git diff --stat docs/dforge/registers/2026-10-21-v1-completion.md
```

Expected: `grep -c toolVariant` now prints `1`, `grep -c create-token-dialog` prints `1`, `grep -c 'policy.Apply does not converge'` prints `1`, `grep -c 'Load older entries'` prints `1` (all on the register file), `register check` exits 0, and the diff shows four inserted lines. Do not run `register set` on any row.

- [ ] **Step 7: Run the test to verify it passes, and the full checks**

Run, with `TMPDIR` and `GOTOOLCHAIN` as in Step 2, each under `timeout`:

```bash
timeout 300 go test -count=1 -run '^TestWcDocsLinks$' -v ./internal/docs/
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/docs/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf "$TMPDIR" && go clean -testcache
```

Expected: `TestWcDocsLinks` passes with one subtest per page (every `docs/user/`, `docs/dev/` and `docs/api/` page and `docs/accessibility.md`); `go vet`, `go mod tidy -diff` print nothing; every `go test` line is `ok` or `?`; `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 8: Commit**

```bash
git add internal/docs/links_test.go
git commit -m "test(docs): check every documentation link"
git add .github/workflows/fuzz.yml
git commit -m "chore(ci): run the fuzz targets in CI"
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): add the wave 9 rulings to section 9"
git add docs/dforge/registers/2026-10-21-v1-completion.md
git commit -m "docs(register): record four wave 9 findings"
```

Commit `docs/security/parsers.md` as `docs(security): correct the parser inventory` and the regenerated `docs/api/` files as `docs(api): regenerate the API reference` only when Step 3 changed them, and a link fix as `docs(user): fix a broken relative link` with the page it touched.
