# v1 wave 11a: firewall changes without ufw reload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close v1-completion rows 437 and 438: no firewall change on an active UFW leaves the built-in INPUT policy at ACCEPT, because Darkpanel stops running `ufw reload` for rule, logging, container and Docker block changes and their reverts.

**Architecture:** A new transition `fwstate.TransLive` means "UFW is active and stays active; its own commands apply live". The predictor models ufw's live path (`_reload_user_rules`: flush the user logging chains, load `user.rules` with `iptables-restore -n`; `update_logging` after a `ufw logging` command). The Docker block still goes to `after.rules`/`after6.rules`, and its chains are loaded through a journaled `-n` restore of only Darkpanel's chains and DOCKER-USER, built by `dockerfw.ChainsInput`. `ufw reload` stays only where UFW must restart: enable, disable, `state.repair`, a repair whose differences reach outside Darkpanel's chains, and `firewall.snapshot.restore`; `ufw default` restarts UFW itself. A live change's snapshot carries the loaded-state before-image, so its revert loads that image with `-n` instead of reloading.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), the firewall module's predictor (`internal/modules/firewall/predict`), the netfilter save parser (`internal/platform/netfilter`), the KVM harness in `test/harness` (GitHub Actions `harness.yml`), Markdown checked by `internal/docs`.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 with risk 3 (the transition change in `predictTarget`)

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w11a-tmp/integ && export TMPDIR=/root/.cache/w11a-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

> **External executors:** agy

**Plan review:** 2026-11-08 — dforge:judge-opus — executability 8 / coherence 10 / coverage 14 / assumptions 9 (round 2)

## Global Constraints

- Scope is spec revision 8 as amended on `main`, plus the rulings W11-R1 to W11-R6 under Assumptions, which Task 1 copies into the spec. It covers register rows `2026-10-21-v1-completion#437` and `#438`. The lockout-guard rows (wave 11b, fact sheet `docs/dforge/notes/v1-w11b-guard-factsheet.md`) and the other modules' rows (wave 11c) are not in this plan. Uninstall's `ufw reload` (`uninstall.go`) is out of scope: it removes Darkpanel's rules on the way out and is not a rule, logging, container or Docker block change.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan:
  - talks to the Docker daemon, D-Bus or systemd;
  - runs `systemctl`, `docker`, `ufw`, `iptables`, `ip6tables`, `iptables-restore`, `nft` or `nmap`;
  - binds an address other than `127.0.0.1` (port 0);
  - reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process.

  Host effects are tested through the existing fakes: the target host `newTGHost`, the emulated end-to-end hosts `newE2EHost` and `deeNewHost` (whose `restore` models `iptables-restore -n` exactly), the unit host of `host_test.go` and `kfHost`. Real `ufw` and `iptables` run only on harness VMs.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w11a-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first. When the task ends, remove it and run `go clean -testcache`. `/tmp` is an 8 GiB tmpfs. When `df -h /` shows 90 % or more before a task's first build, run `go clean -cache`.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`. After every task, `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1` on the task's packages (every line `ok`); `go test -race -count=1 ./internal/modules/firewall/...`; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`.
- Generated files: no task changes a wire type, so `schemas/**` and `docs/api/**` stay unchanged and `TestAdAPIDocsCurrent` stays green.
- TDD: write the test, run it and see the expected RED (a build failure naming the first undefined identifier, or a failed assertion), then implement and run GREEN. Quote RED and GREEN verbatim in the ledger.
- Existing behaviour that must not change: the lockout guard evaluates `target.pred`; the revert timer and its confirm window; foreign DOCKER-USER rule preservation (`keepForeign`, `finishForeign`, `reloadDockerUser`); `verifyDiffs` (`sync.go`) agreeing with the loaded state after every apply; enable, disable, `state.repair` and `firewall.snapshot.restore` keep `ufw --force enable`, `ufw disable` and `ufw reload` exactly as today; the revert of a change keeps them only when the change ran one (W11-R3, W11-R5).
- Frozen files (spec 1.12): no task edits `internal/core/**`, `internal/platform/*.go`, `internal/controller/**`, `go.mod`, `go.sum`, `test/harness/pins.go` or `schemas/**`. Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it. A pre-existing test in the task's own package whose assertion the task's change makes stale (a `reload` it no longer runs, an effect it no longer makes, a snapshot image it now takes) is updated to the new behaviour without stopping, staged by the task's `git add`, and named in the ledger with the reason.
- File ownership: only Task 1 edits the spec; only Task 9 edits `docs/security/threat-model.md` and `docs/user/`. No task edits `.github/workflows/**`, `Makefile`, `docs/runbook.md` or `CHANGELOG.md`. The plan runs on branch `v1-w11a-live-apply` in the base repository (no worktree).
- Test isolation: files under `t.TempDir()`; no TCP listener; no package-level mutable state except an existing seam restored through `t.Cleanup`. Harness tests derive names from the test name and carry `Unit` in the name of every VM-free test.
- Go identifiers: every new test function carries its task's prefix from Contracts "Test prefixes", and every new unexported test helper the same prefix in lower case.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name; a changed refusal keeps its error code.
- Documentation style (Tasks 1, 9): English, plain Markdown, sentence-case headings, second person, present tense, commands exactly as the CLI prints them. A page describes what exists on `main` and never names a plan, wave, task or register row (the spec's "Plan rulings" bullets are exempt). Every relative link resolves.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies: Task 2 edits `test/harness/` and Tasks 5-8 change the firewall module's host behaviour. Push the branch and dispatch `harness.yml` on it per the owner's standing authorization, after Task 2 (the spike) and after Task 9. No task creates or pushes a tag or runs `release.yml`.

## Contracts

### Test prefixes

| Task | Package(s) | Go test prefix |
|---|---|---|
| 1 | — (spec only) | — |
| 2 | `test/harness` | `TestW11a02…Unit…` for VM-free tests; the VM step is `m3LiveApply` |
| 3 | `internal/modules/firewall/predict`, `internal/modules/firewall/fwstate`, `internal/modules/firewall/loaded` | `TestW11a03` |
| 4 | `internal/modules/firewall/dockerfw` | `TestW11a04` |
| 5 | `internal/modules/firewall` | `TestW11a05` |
| 6 | `internal/modules/firewall` | `TestW11a06` |
| 7 | `internal/modules/firewall` | `TestW11a07` |
| 8 | `internal/modules/firewall` | `TestW11a08` |
| 9 | `internal/docs` | `TestW11a09` |

### TransLive

Produced by Task 3, consumed by Tasks 5 to 8.

- `fwstate.TransLive Transition = "live"` (`internal/modules/firewall/fwstate/fwstate.go`, beside `TransNone`, `TransReload`, `TransEnable`, `TransDisable`).
- `predict.Predict(cfg, cur, fwstate.TransLive)`: when `cfg.Enabled` is false, returns `cur` unchanged (a copy). Otherwise, for IPv4, and for IPv6 when `cfg.IPv6` is true and `cur.V6 != nil`: flush `ufw-user-logging-input`, `ufw-user-logging-output` and `ufw-user-logging-forward` (`ufw6-` for IPv6); then load every chain the user rules file of that family (`/etc/ufw/user.rules`, `/etc/ufw/user6.rules`, the bytes in `cfg.Files`) declares, replacing that chain's rules as `iptables-restore -n` does; then, when `cfg` carries `LiveLogging` (below), run `startLogging`. Built-in policies, every other chain and the sysctl values are unchanged. A missing or empty user rules file is an error.
- `fwstate.Config.LiveLogging bool`: set by `predict.Edit` when the edited command is `logging …` on an enabled configuration; cleared by nothing else. It is not stored, rendered or compared.
- `loaded.PredictSysctl(cfg, cur, fwstate.TransLive)` returns `cur` unchanged. `predict.IPv6Refusal` treats `TransLive` as `TransNone`.

### Docker chains input

Produced by Task 4, consumed by Tasks 6 and 8.

- `dockerfw.ChainsInput(block []byte, loaded *netfilter.Ruleset) []byte`, in `internal/modules/firewall/dockerfw/restore_input.go`. `block` is one family's rendered block (`dockerfw.Render` output, a `*filter` … `COMMIT` section). The result is one `iptables-restore -n` input: `*filter`, a declaration `:<chain> - [0:0]` for `DOCKER-USER` and for every chain the block declares that `dockerfw.Owned` reports, never `:ufw-user-forward` or `:ufw6-user-forward`; every `-A` line of the block in order; then `-F <chain>` and `-X <chain>` for every `dockerfw.Owned` chain loaded in `loaded`'s filter table that the block does not declare; then `COMMIT`. With `loaded == nil` no stale chain is removed.
- `dockerfw.ApplyChains(rs *netfilter.Ruleset, input []byte) (*netfilter.Ruleset, error)`, same file: the ruleset that `iptables-restore -n` of `input` leaves, starting from a copy of `rs`: a declared chain is created or flushed and refilled by its `-A` lines; `-F` flushes; `-X` deletes; no other chain and no policy changes. It is the predictor of the load step.

### Chain load step

Produced by Task 6, consumed by Tasks 7 and 8.

- The resource `firewall.chains:block` (constant `blockChainsResource` in `docker_effects.go`). Its state is a JSON object mapping each loaded family (`"v4"`, `"v6"`) to an object mapping DOCKER-USER and every `dockerfw.Owned` chain of the filter table to the list of `netfilter.Key(rule, v6)` of its rules, in order. Before is that state of the loaded rulesets; After is that state of `dockerfw.ApplyChains(loaded, input)`; the observation after apply is that state of the loaded rulesets again. Keys, not rule text, make the prediction and the kernel's save output compare equal, as `verifyDiffs` does.
- `loadBlockChains(ctx context.Context, env core.Env, d deps, variant string, inputs map[fwstate.Family][]byte) error` (`docker_effects.go`): runs `restoreProgram(fam, variant)` with `-n` and the family's input, IPv4 first, through `runDockerEffect` on `blockChainsResource`.
- Its inverse (`inverse.go`, `undoDocker`, the `blockChainsResource` case). Task 6: runs `ufw reload` when the configuration says UFW is active, as the existing `firewall.chains:docker` "loaded" case does. Task 8 adds the image branch (Contracts "Live snapshot").
- The block of a family is `dockerfw.BlockOf(<after file bytes>)` (`block, ok, err`): from `c.files[path]` when the change writes the file, otherwise from the configured `cfgN.Files[path]`. The chain step runs only when `c.reload` is set and `ok` is true; a file without a block (`firewall.docker.disable`) gets no chain step, because the cleanup step removes the chains.
- `change.reload` keeps its name and now means "the Docker block's files changed"; on a running UFW that stays active, Apply runs the chain load step after the ufw commands instead of `ufw reload`.

### Live snapshot

Produced by Tasks 5, 7 and 8 as marked; consumed by Tasks 7 and 8.

- `hasDefaultOp(c change) bool` (`target.go`, Task 5): some op of `c` has `default` as its first argument.
- `(fw).selfTransition(tg *target, cur fwstate.Loaded, tr fwstate.Transition) error` (`target.go`, Task 5): the IPv6 refusal with `tr`, `tg.pred = predict(tg.cfgN, cur, tr)`, `tg.final = tg.cfgN`, and no entry in `tg.cmds`. It predicts a transition that no Darkpanel command runs: `TransLive`, and the restart `ufw default` makes itself.
- `type imageKind int` (`snapshot.go`, Task 7) with the constants `imageNone`, `imageRepair` and `imageLive`. `capture(ctx, env, d, gc, kind imageKind)` replaces the `withImage bool` parameter: `imageRepair` and `imageLive` take the before-image, and `imageLive` also sets `snapshotState.Live`. Every existing call site maps `false` to `imageNone` and `true` to `imageRepair`.
- `snapshotState.Live bool` with tag `json:"live,omitempty"` (`store.go`, Task 7): the image was taken before a change that UFW applies live. A whole-snapshot `restore` loads such an image; `undoKeepingForeign` never does, because every inverse of a live change already ran live.
- `liveTarget(p prepared) bool` (`handler.go`, Task 7): `p.tg.tr == fwstate.TransLive`, or `hasDefaultOp(p.c) && p.tg.tr == fwstate.TransReload && len(p.tg.cmds) == 0` (W11-R4).
- `(mutation).Snapshot` (Task 7) runs `m.f.prepare(ctx, env, m.build(ctx, env, in))` under its read lock, then captures with `imageRepair` when `m.image` is set, otherwise `imageLive` when `liveTarget` holds, otherwise `imageNone`. When prepare returns an error it captures with `imageNone` (`imageRepair` when `m.image`); Apply's own prepare then returns that error.
- `restoreBaseline` (`uninstall.go`, Task 7) clears `snap.State.Image` before `restore` when `snap.State.Live` is set, so an uninstall keeps the UFW transition it chooses today.
- `imageBlock(img *beforeImage, fam fwstate.Family) ([]byte, bool)` (`image.go`, Task 8): the image's filter-table DOCKER-USER and `dockerfw.Owned` chains of family `fam` as a `*filter` … `COMMIT` block (a declaration `:<chain> - [0:0]` per chain, then each chain's rule `Text` lines in order); false when the image holds none of them for that family.
- `planFamily` (`image.go`, Task 8): after the filter table's rule lines, `-F <chain>` and `-X <chain>` for every chain of `cur`'s filter table whose name starts with `darkpanel-` or `dp-b-` and that the image's filter table lacks, sorted by name. A chain that a chain left out of the load still jumps to is not deleted and goes to `Excluded`. `imageMismatch` also reports a `darkpanel-` or `dp-b-` chain that is loaded, absent from the image and not excluded.
- `undoDocker(ctx, env, d, snap snapshot, noun, ident string, e core.Effect) error` (`inverse.go`, Task 8): gains `snap`, passed by `inverse`. When `snap.State.Image` is set, the `blockChainsResource` case and the `firewall.chains:docker` "loaded" case load each family with `-n` and `dockerfw.ChainsInput(<imageBlock of that family>, <loaded ruleset of that family>)` and run no `ufw reload`; without an image they keep the reload.

### Refusal and log texts

- The spike's failure messages (Task 2): `live ufw commands left INPUT policy ACCEPT in %s` and `live ufw %s and a reload disagree on %s chain %s`.

## Assumptions (evidence)

- ufw applies a rule command on an active UFW without stopping it: for a modified, inserted or deleted rule (and when `_need_reload` holds) `set_rule` calls `_reload_user_rules`, which flushes only `ufw[6]-user-logging-{input,output,forward}` and loads the user rules file with `iptables-restore -n` (`.dforge/drafts/v1-w1-firewall/ufw-src/ufwspike-debian-13/ufw/backend_iptables.py:570-596`, `:1104-1137`; chain list `:57-71`; read 2026-11-08); a plain add appends with `iptables -A` directly (`:1140-1143`). `update_logging` edits the running chains without a stop (`:1220-1292`). Task 2 verifies on all four harness images that a live command never leaves INPUT at ACCEPT and that its result equals a following `ufw reload` over UFW's chains.
- `ufw default` on an active UFW runs `stop_firewall()` then `start_firewall()` itself (`frontend.py:245-256`, read 2026-11-08), so its window cannot be removed by Darkpanel (ruling W11-R4).
- Every rule, default and logging action on an active UFW reloads today: `predictTarget` chooses `TransReload` when `want && (len(c.ops) > 0 || c.restore != nil || c.repair || c.reload)` (`internal/modules/firewall/target.go:118`); revert reloads through `reloadIfActive` (`revert.go:317`). Container rules always carry ufw ops (`dockerfw/realize.go:69-86`).
- The golden captures `rules` and `logging` in `internal/modules/firewall/predict/testdata` were taken after live commands on an enabled UFW — unverified; Task 3 verifies it through its `TestPredictGolden` rows. If a row fails only because a capture was taken after a reload, Task 3 stops and reports it.
- A `-n` input that declares `:ufw-user-forward` flushes UFW's live route rules, so `ChainsInput` never declares it (`dockerfw/block.go:108-115`; `predict/translate.go:30-37`).
- Foreign DOCKER-USER rules cannot be loaded when a block change applies: prepare refuses them (`plan.go:68-77`, `docker_refuse.go:22`) and reconcile skips (`reconcile.go:83-89`); the restore, revert and cleanup paths that keep them go through `keepForeign` and are unchanged.
- The engine calls `Snapshot` before it records the change and before `Apply`, which only attaches that snapshot and then prepares (`handler.go:76-108`, `plan.go:79`), so the image decision is made in `Snapshot`. `prepare` and every action's `build` only read; `Plan` runs them as a dry run (`plan.go:48-88`).
- A revert with nothing blocked, no conflict and the post-apply hashes takes the whole-snapshot `restore` (`revert.go:141-143`); a `restore` of a snapshot with an image runs no UFW transition and loads the image (`restore.go:215-229`, `:263-270`). Otherwise `undoKeepingForeign` runs the inverses and, for an image, refuses when a state file changed since the snapshot (`docker_revert.go:296-319`).
- `firewall.snapshot.restore` goes through `change{restore: …}` and its own reload, and never reads `State.Image` (`actions_snapshot.go:45`, `handler.go:232-255`); only a change's own revert and uninstall's `restoreBaseline` (`uninstall.go:203`) restore a snapshot with `restore`.
- An image load declares, flushes and appends chains but never deletes one (`image.go:158-202`), and `imageMismatch` checks only the image's chains (`image.go:271-291`), so without Task 8 a reverted container allow leaves its `dp-b-` chain loaded.
- A live Docker change's image holds no foreign DOCKER-USER rule: prepare refuses the change while one is loaded (bullet above), so `imageBlock` reloads none; foreign rules added after the apply are kept by the existing `keepForeign` wrapping of the revert.
- `cleanupSteps` reloads only outside a change (`docker_cleanup.go:228-231`). Uninstall cleans up in `cleanupRefuse` mode (`uninstall.go:86`); the inverse of `firewall.chains:docker` and the pre-cleanup of `restore` use `cleanupPreserve` (`wire.go:62`, `restore.go:199-204`).
- `e2eHost.Run` models no `*-restore` program (`e2e_guard_test.go:130-150`); `deeRestore` models `-n` input on a ruleset (`docker_e2e_host_test.go:390-396`) and serves `deeHost`, which embeds `e2eHost`.
- Plan review round 2 (the cap) scored coherence 10 and assumptions 9 (borderline) and executability 8; its 2 Critical and 3 Important findings were fixed after the round (image capture moved into `Snapshot`, the default revert and the conflict revert covered, stale chains deleted, stale tests staged; the revert task split into Tasks 7 and 8). No review round ran on those fixes, so the owner decides whether the plan executes as it stands.
- Rulings (Task 1 copies them into the spec as "Plan rulings (wave 11a)"):
  - **W11-R1 three plans.** Wave 11 fixes every row left open after wave 10 (owner, 2026-11-08). It runs as three sequential plans: `v1-w11a-live-apply` (rows 437, 438), `v1-w11b-lockout-guard` (security-review rows 27, 28, 46, 47, 48, 51, 53) and `v1-w11c-module-fixes` (security-review rows 26, 30-45, 49, 50, 52, 54).
  - **W11-R2 live apply.** On an active UFW that stays active, Darkpanel runs no `ufw reload`. UFW applies rule and logging commands live. The Docker block is written to `after.rules` and `after6.rules` and its chains load through one `iptables-restore -n` per family that declares DOCKER-USER and Darkpanel's own chains, never `ufw-user-forward`, and removes Darkpanel chains the block no longer has. The prediction is `TransLive` (Contracts). Owner decision 2026-11-08.
  - **W11-R3 where UFW still restarts.** `state.enable`, `state.disable`, `state.repair`, a `firewall.docker.repair` whose differences reach a chain outside Darkpanel's own, and snapshot restore keep their UFW transition. Their revert keeps it too.
  - **W11-R4 default policies.** `ufw default` restarts an active UFW itself, so `firewall.defaults.set` keeps a window in which the built-in policies are ACCEPT for about a second. Darkpanel adds no reload of its own after it: a change with a `default` command predicts `TransReload` and runs no `ufw reload` command. The threat model records the window as a residual.
  - **W11-R5 revert.** The snapshot taken before a change that UFW applies live, and before a `default` change on an active UFW, carries the loaded-state before-image, marked live. Reverting such a change whole restores the files and loads that image with `iptables-restore -n` per family, as a repair snapshot is restored, and removes Darkpanel chains the image lacks. An effect-by-effect revert runs the inverse ufw commands live, loads the Docker chains from the image, and loads no other part of it. Neither runs `ufw reload` while UFW stays active; an effect-by-effect revert of a `default` command restarts UFW only through `ufw default` itself. An uninstall restores its baseline snapshot without a live image, with the transition it chooses today.
  - **W11-R6 row 437 acceptance.** Row 437's acceptance (no reconcile pass or container rule change leaves INPUT at ACCEPT) is evidenced by the harness: Task 2's live-apply step, and `TestM3Docker`'s traffic-recreate scenario with no leak window, on all four images.

## Task index

1. Spec rulings for wave 11
2. Harness spike: live ufw commands keep the policy
3. Predictor: the live transition
4. Docker chains restore input
5. Rule and logging changes apply live
6. Docker block changes load their chains live
7. Live snapshots revert without reload
8. Docker chain reverts and repairs without reload
9. Threat model and user guide

---

### Task 1: Spec rulings for wave 11

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section "Parallel decomposition", section "Plans", and section 10's plan rulings list after "Plan rulings (wave 10b)")

**Interfaces:**
- Consumes: none
- Produces: none

**Implementer:** dforge:impl-sonnet-low
**Executor:** agy gemini-3.8-flash / medium
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Write the failing check**

```bash
S=docs/dforge/specs/2026-10-21-v1-completion-design.md
for s in 'Plan rulings (wave 11a)' 'W11-R1 three plans' 'W11-R2 live apply' 'W11-R3 where UFW still restarts' 'W11-R4 default policies' 'W11-R5 revert' 'W11-R6 row 437 acceptance' '| 11 | Follow-ups'; do
  grep -qF "$s" "$S" || echo "missing: $s"
done
```

- [ ] **Step 2: Run the check to verify it fails**

Expected: eight `missing:` lines.

- [ ] **Step 3: Edit the spec**

1. After the last bullet of "Plan rulings (wave 10b)" (the W10b-R14 bullet), add a bullet `- **Plan rulings (wave 11a).** Wave 11a (plan \`v1-w11a-live-apply\`) settles these points:` with one sub-bullet per ruling W11-R1 to W11-R6, copied verbatim from this plan's Assumptions, each starting `**W11-R<n> <title>.**`.
2. In "Parallel decomposition", add a row after wave 10: `| 11 | Follow-ups: live apply (11a), lockout guard (11b), module fixes (11c) | 11a: \`internal/modules/firewall/**\` except \`guard/**\` and \`eval/**\`, \`test/harness/m3_firewall*\`; 11b: \`internal/modules/firewall/guard/**\`, \`eval/**\`, \`internal/platform/netstat/**\`, \`internal/modules/panel/listener*.go\`, \`test/harness/m3_docker_traffic*\`; 11c: fixes in the modules the rows name; each plan edits \`docs/security/threat-model.md\` | 0–10 | alone |`.
3. In "Plans", after the paragraph about wave 0, add one sentence: `Wave 11 is planned as three sequential plans (W11-R1), each written after the previous one merges.`

- [ ] **Step 4: Run the check to verify it passes**

Run the Step 1 script. Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): add the wave 11 plan rulings"
```

### Task 2: Harness spike: live ufw commands keep the policy

**Files:**
- Create: `test/harness/m3_firewall_live_test.go`
- Create: `test/harness/m3_firewall_live_unit_test.go`
- Modify: `test/harness/m3_firewall_test.go` (the `steps` table of `TestM3Firewall`)

**Interfaces:**
- Consumes: `fleet`, `f.agent.Run(ctx, cmd string) (string, error)` (existing harness helpers)
- Produces: `w11a02UFWChains(save string) map[string]string` and `w11a02PolicyLeaks(log string) []string` (test-only, used only by this task); the "Refusal and log texts" spike messages

**Items:** 2026-10-21-v1-completion#437, 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

`test/harness/m3_firewall_live_unit_test.go`:

```go
//go:build harness

package harness

import (
	"reflect"
	"testing"
)

const w11a02Save = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:ufw-user-input - [0:0]
:ufw-user-logging-input - [0:0]
-A INPUT -j ufw-before-logging-input
-A DOCKER-USER -j RETURN
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`

func TestW11a02UnitUFWChainsKeepsOnlyUFWChainsAndPolicies(t *testing.T) {
	got := w11a02UFWChains(w11a02Save)
	want := map[string]string{
		"INPUT policy":           "DROP",
		"FORWARD policy":         "DROP",
		"OUTPUT policy":          "ACCEPT",
		"ufw-user-input":         "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT",
		"ufw-user-logging-input": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("w11a02UFWChains = %#v, want %#v", got, want)
	}
}

func TestW11a02UnitPolicyLeaksNamesEveryAcceptLine(t *testing.T) {
	log := "v4 -P INPUT DROP\nv6 -P INPUT DROP\nv4 -P INPUT ACCEPT\nv6 -P INPUT DROP\nv6 -P INPUT ACCEPT\n"
	got := w11a02PolicyLeaks(log)
	want := []string{"v4", "v6"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("w11a02PolicyLeaks = %q, want %q", got, want)
	}
	if got := w11a02PolicyLeaks("v4 -P INPUT DROP\nv6 -P INPUT DROP\n"); len(got) != 0 {
		t.Errorf("w11a02PolicyLeaks on a clean log = %q, want none", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -tags harness -count=1 -run 'TestW11a02Unit' ./test/harness/`
Expected: FAIL, build error `undefined: w11a02UFWChains`.

- [ ] **Step 3: Implement**

1. In `m3_firewall_live_test.go` (build tag `harness`), write `w11a02UFWChains(save string) map[string]string`: parse one family's `iptables-save` text line by line; record `"<CHAIN> policy"` for each built-in declaration (`:INPUT DROP [0:0]` gives `"INPUT policy": "DROP"`); for every chain whose name starts with `ufw-` or `ufw6-`, record its `-A` lines joined by `\n` in order (an empty string for a declared chain without rules); ignore every other chain.
2. Write `w11a02PolicyLeaks(log string) []string`: each line is `<family> -P INPUT <policy>`; return the families, in first-seen order and without duplicates, that have any line ending `ACCEPT`.
3. Write `m3LiveApply(ctx context.Context, t *testing.T, f *fleet)`: on the agent host, with UFW active,
   - start a policy poll in the background: `sudo sh -c 'while [ ! -e /run/w11a-stop ]; do echo "v4 $(iptables -S INPUT | head -1)"; echo "v6 $(ip6tables -S INPUT | head -1)"; done > /run/w11a-poll.log 2>&1 < /dev/null &'` run through `setsid` so the session does not wait for it;
   - run, one at a time, these commands, each followed by a capture of `sudo iptables-save -t filter` and `sudo ip6tables-save -t filter`: `sudo ufw allow 8097/tcp comment w11a`, `sudo ufw route allow in on eth0 out on eth0 to 192.0.2.10 port 8097 proto tcp comment w11a`, `sudo ufw insert 1 deny from 198.51.100.7 comment w11a`, `sudo ufw logging medium`, `sudo ufw delete allow 8097/tcp`, `sudo ufw logging low`;
   - stop the poll (`sudo touch /run/w11a-stop`, wait one second, read `/run/w11a-poll.log`), and fail with `live ufw commands left INPUT policy ACCEPT in <family>` for every family `w11a02PolicyLeaks` returns;
   - after the last command's capture, run `sudo ufw reload`, capture again, and for each family compare `w11a02UFWChains` of the last live capture with that of the reload capture; fail with `live ufw <last command> and a reload disagree on <family> chain <chain>` naming each differing key;
   - `t.Cleanup`: `sudo ufw delete allow 8097/tcp; sudo ufw route delete allow in on eth0 out on eth0 to 192.0.2.10 port 8097 proto tcp; sudo ufw delete deny from 198.51.100.7; sudo ufw logging low; sudo rm -f /run/w11a-stop /run/w11a-poll.log`, errors ignored.
4. Add the step `{"live-apply", false, func(t *testing.T) { m3LiveApply(ctx, t, f) }}` to `TestM3Firewall`'s `steps`, directly before `golden-recheck`.
5. Push the branch and dispatch `harness.yml` (the `vm` job on all four images). Record the run id in the ledger. If `live-apply` fails on any image, stop the plan and report to the owner with the failing output: rulings W11-R2 and W11-R5 rest on it.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -tags harness -count=1 -run 'TestW11a02Unit' ./test/harness/`
Expected: PASS. Then the harness run: `TestM3Firewall/live-apply` passes on debian-12, debian-13, ubuntu-22.04 and ubuntu-24.04.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m3_firewall_live_test.go test/harness/m3_firewall_live_unit_test.go test/harness/m3_firewall_test.go
git commit -m "test(harness): check live ufw commands keep policy"
```

### Task 3: Predictor: the live transition

**Files:**
- Modify: `internal/modules/firewall/fwstate/fwstate.go` (the `Transition` constants and `Config`)
- Modify: `internal/modules/firewall/predict/predict.go` (`Predict`)
- Modify: `internal/modules/firewall/predict/edit.go` (`Edit`, the `logging` command)
- Modify: `internal/modules/firewall/predict/refusal.go` (`IPv6Refusal`)
- Modify: `internal/modules/firewall/loaded/sysctl_predict.go` (`PredictSysctl`)
- Test: `internal/modules/firewall/predict/predict_test.go`

**Interfaces:**
- Consumes: none
- Produces: Contracts "TransLive"

**Items:** 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

Note: risk 2, not 3: the predictor is pure and no action uses `TransLive` until Task 5.

- [ ] **Step 1: Extend the test `internal/modules/firewall/predict/predict_test.go::TestPredictGolden`**

Add these rows to the case table (they reuse the golden captures of every image in `predictImages`):

```go
			{"enabled", "rules", "rules", fwstate.TransLive},
			{"rules", "rules", "rules", fwstate.TransLive},
```

and add, in the same file:

```go
// TestW11a03LiveKeepsPoliciesAndOtherChains: a live apply changes only the
// chains the user rules file declares and the user logging chains.
func TestW11a03LiveKeepsPoliciesAndOtherChains(t *testing.T) {
	for _, img := range predictImages {
		cur := predictLoaded(t, img, "enabled")
		f := cur.V4.Table("filter")
		f.Chain("INPUT").Policy = "DROP"
		f.Chains = append(f.Chains, netfilter.Chain{Name: "DOCKER-USER", Rules: []netfilter.Rule{{Text: "-A DOCKER-USER -j RETURN"}}})
		before := predictLoadedClone(cur)
		got, err := Predict(predictConfig(t, img, "rules"), cur, fwstate.TransLive)
		if err != nil {
			t.Fatalf("%s: %v", img, err)
		}
		gf, bf := got.V4.Table("filter"), before.V4.Table("filter")
		if p := gf.Chain("INPUT").Policy; p != "DROP" {
			t.Errorf("%s: INPUT policy %q after a live apply, want DROP", img, p)
		}
		predictTestSame(t, img+" DOCKER-USER", gf.Chain("DOCKER-USER"), bf.Chain("DOCKER-USER"))
		predictTestSame(t, img+" ufw-before-input", gf.Chain("ufw-before-input"), bf.Chain("ufw-before-input"))
		if reflect.DeepEqual(gf.Chain("ufw-user-input"), bf.Chain("ufw-user-input")) {
			t.Errorf("%s: ufw-user-input unchanged by a live apply of the rules configuration", img)
		}
	}
}

// TestW11a03LiveLoggingMatchesTheLoggingCapture: a live "ufw logging"
// command refreshes the logging chains as update_logging does.
func TestW11a03LiveLoggingMatchesTheLoggingCapture(t *testing.T) {
	for _, img := range predictImages {
		cfg := predictConfig(t, img, "logging")
		cfg.LiveLogging = true
		got, err := Predict(cfg, predictLoaded(t, img, "rules"), fwstate.TransLive)
		if err != nil {
			t.Fatalf("%s: %v", img, err)
		}
		want := predictLoaded(t, img, "logging")
		for _, d := range predictDiff(got.V4, want.V4, false) {
			t.Errorf("%s v4: %s", img, d)
		}
		for _, d := range predictDiff(got.V6, want.V6, true) {
			t.Errorf("%s v6: %s", img, d)
		}
	}
}

// TestW11a03LiveOnADisabledConfigurationChangesNothing: ufw applies
// nothing live while it is disabled.
func TestW11a03LiveOnADisabledConfigurationChangesNothing(t *testing.T) {
	img := predictImages[0]
	cur := predictLoaded(t, img, "disabled")
	got, err := Predict(predictConfig(t, img, "disabled"), cur, fwstate.TransLive)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range predictDiff(got.V4, cur.V4, false) {
		t.Errorf("v4: %s", d)
	}
}
```

Add `"reflect"` and `"github.com/darkraise/darkpanel/internal/platform/netfilter"` to the imports if the file lacks them.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestPredictGolden|TestW11a03' ./internal/modules/firewall/predict/`
Expected: FAIL, build error `undefined: fwstate.TransLive`.

- [ ] **Step 3: Implement**

1. `fwstate.go`: add `TransLive Transition = "live"` with a doc comment ("UFW is active and stays active; its rule and logging commands apply live, without a stop"), and the field `LiveLogging bool` on `Config` with a doc comment saying it is set by `predict.Edit` and never stored.
2. `predict.Predict`: add the `fwstate.TransLive` case per Contracts "TransLive": refuse an unsupported configuration as the start path does; per served family flush the three user logging chains, parse the user rules file bytes from `cfg.Files` with the netfilter save parser the translator already uses, and `loadChain` each declared chain of its filter table into the family's filter table; then `startLogging(cfg, …)` only when `cfg.LiveLogging`. Update `Predict`'s doc comment list with one `TransLive` item.
3. `predict.Edit`: when the command is `logging …` and the configuration is enabled, set `LiveLogging` on the result.
4. `IPv6Refusal`: treat `TransLive` like `TransNone`. `PredictSysctl`: `TransLive` returns the current values, like `TransNone`. Any other `switch` over `fwstate.Transition` that the compiler or `golangci-lint`'s `exhaustive` linter reports gets a `TransLive` case equal to its `TransNone` case.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/modules/firewall/...`
Expected: PASS. If the `{"enabled", "rules", "rules", fwstate.TransLive}` row or `TestW11a03LiveLoggingMatchesTheLoggingCapture` fails only on chains a reload rebuilds and a live command does not (before or after chains, jump order), the capture was taken after a reload: stop, record the diff in the ledger and report BLOCKED; never change the expected captures.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/fwstate/fwstate.go internal/modules/firewall/predict/ internal/modules/firewall/loaded/sysctl_predict.go
git commit -m "feat(firewall): predict live ufw commands"
```

### Task 4: Docker chains restore input

**Files:**
- Modify: `internal/modules/firewall/dockerfw/restore_input.go`
- Test: `internal/modules/firewall/dockerfw/restore_input_test.go`

**Interfaces:**
- Consumes: none
- Produces: Contracts "Docker chains input"

**Items:** 2026-10-21-v1-completion#437

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 0 - spec 2 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Append to `restore_input_test.go`:

```go
const w11a04Block = `*filter
:ufw-user-forward - [0:0]
:DOCKER-USER - [0:0]
:darkpanel-docker-bindings - [0:0]
:darkpanel-docker-deny - [0:0]
:dp-b-new - [0:0]
-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
-A DOCKER-USER -j RETURN
-A darkpanel-docker-bindings -j dp-b-new
-A dp-b-new -j ufw-user-forward
-A dp-b-new -j darkpanel-docker-deny
-A darkpanel-docker-deny -j DROP
COMMIT
`

const w11a04Loaded = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:ufw-user-forward - [0:0]
:darkpanel-docker-bindings - [0:0]
:darkpanel-docker-deny - [0:0]
:dp-b-old - [0:0]
-A DOCKER-USER -j darkpanel-docker-bindings
-A ufw-user-forward -d 172.17.0.2/32 -p tcp -m tcp --dport 80 -j ACCEPT
-A darkpanel-docker-bindings -j dp-b-old
-A dp-b-old -j ufw-user-forward
-A darkpanel-docker-deny -j DROP
COMMIT
`

func w11a04Parse(t *testing.T, s string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestW11a04ChainsInputNeverDeclaresUFWForward(t *testing.T) {
	in := string(ChainsInput([]byte(w11a04Block), w11a04Parse(t, w11a04Loaded)))
	for _, bad := range []string{":ufw-user-forward", ":ufw6-user-forward"} {
		if strings.Contains(in, bad) {
			t.Errorf("input declares %s, which would flush UFW's route rules:\n%s", bad, in)
		}
	}
	for _, want := range []string{":DOCKER-USER - [0:0]", ":dp-b-new - [0:0]", "-A dp-b-new -j ufw-user-forward", "-F dp-b-old", "-X dp-b-old"} {
		if !strings.Contains(in, want) {
			t.Errorf("input lacks %q:\n%s", want, in)
		}
	}
	if strings.Index(in, "-X dp-b-old") < strings.Index(in, "-A darkpanel-docker-bindings -j dp-b-new") {
		t.Errorf("the stale chain is deleted before the bindings chain stops jumping to it:\n%s", in)
	}
}

func TestW11a04ApplyChainsReplacesOnlyOwnedChains(t *testing.T) {
	loaded := w11a04Parse(t, w11a04Loaded)
	got, err := ApplyChains(loaded, ChainsInput([]byte(w11a04Block), loaded))
	if err != nil {
		t.Fatal(err)
	}
	f := got.Table("filter")
	if f.Chain("dp-b-old") != nil || f.Chain("dp-b-new") == nil {
		t.Errorf("chains after the load: old %v, new %v; want the old one gone and the new one loaded", f.Chain("dp-b-old"), f.Chain("dp-b-new"))
	}
	if uf := f.Chain("ufw-user-forward"); uf == nil || len(uf.Rules) != 1 {
		t.Errorf("ufw-user-forward = %+v, want its one route rule kept", uf)
	}
	if p := f.Chain("INPUT").Policy; p != "DROP" {
		t.Errorf("INPUT policy %q, want DROP", p)
	}
	if du := f.Chain("DOCKER-USER"); du == nil || len(du.Rules) != 3 {
		t.Errorf("DOCKER-USER = %+v, want the block's three lines", du)
	}
	if loaded.Table("filter").Chain("dp-b-old") == nil {
		t.Error("ApplyChains modified its input ruleset")
	}
}
```

`netfilter.Parse` (`internal/platform/netfilter/parse.go:143`) reads save text; `ApplyChains` reads its restore input with `netfilter.ParseRestore` (`restore.go:100`) or line by line, since the input carries `-F` and `-X` lines.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestW11a04' ./internal/modules/firewall/dockerfw/`
Expected: FAIL, build error `undefined: ChainsInput`.

- [ ] **Step 3: Implement**

1. `ChainsInput` per Contracts: read the block's declarations and `-A` lines (reuse the parsing `BlockRules` does); emit `*filter`, the declarations of `DOCKER-USER` and every `Owned` chain, the `-A` lines in block order, then `-F`/`-X` of every `Owned` chain of `loaded`'s filter table the block does not declare (sorted by name), then `COMMIT`. Document why `ufw-user-forward` is never declared.
2. `ApplyChains` per Contracts: copy `rs`; walk the input; a declaration creates the chain at the end of the filter table or flushes it; `-A` appends to its chain; `-F` flushes; `-X` removes the chain; refuse (error starting `dockerfw:`) a `-X` of a chain another remaining rule still jumps to, as the kernel would.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/modules/firewall/dockerfw/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/dockerfw/restore_input.go internal/modules/firewall/dockerfw/restore_input_test.go
git commit -m "feat(firewall): build the Docker chains restore input"
```


### Task 5: Rule and logging changes apply live

**Files:**
- Modify: `internal/modules/firewall/target.go` (`predictTarget`; new `selfTransition`, `hasDefaultOp`)
- Modify: `internal/modules/firewall/e2e_guard_test.go` (`(*e2eHost).ufw`: model live commands)
- Test: `internal/modules/firewall/target_test.go`
- Test (update): `internal/modules/firewall/handler_test.go` (`TestMutationLifecycle`: its effects and its last call), `internal/modules/firewall/e2e_engine_test.go` (`TestE2EApplyVerifyRevert`: the apply's `ran` list)

**Interfaces:**
- Consumes: Contracts "TransLive"
- Produces: Contracts "Live snapshot" (`hasDefaultOp`, `selfTransition`); `predictTarget` returns `tr == fwstate.TransLive` and no transition command for a change on a running UFW that stays active, has ufw ops, no `restore`, no `repair`, no `default` command and no `reload`

**Items:** 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security, silent-failures

- [ ] **Step 1: Extend `internal/modules/firewall/target_test.go`**

In `TestTargetTransitions`, replace the row "rule on an active UFW" with the first row below and add the other two after it:

```go
		{"rule on an active UFW", tgRunning, true, func(t *testing.T) change { return change{ops: []ufwOp{tgAllow80(t)}} },
			fwstate.TransLive, nil, "nft|live", true},
		{"a default command on an active UFW restarts UFW itself", tgRunning, true, func(t *testing.T) change { return change{ops: []ufwOp{w11a05Default(t)}} },
			fwstate.TransReload, nil, "nft|reload", true},
		{"a rule on an enabled but stopped UFW still reloads", tgStopped, true, func(t *testing.T) change { return change{ops: []ufwOp{tgAllow80(t)}} },
			fwstate.TransReload, []string{"reload"}, "nft|reload", true},
```

The table's sysctl check (`h.sysctlTr[0] != c.tr`) then requires that `d.sysctl` receives `TransLive` for the first row and `TransReload` for the other two. In `TestTargetCommands`, change the expected IPv6 refusal transitions from `[]fwstate.Transition{fwstate.TransReload}` to `[]fwstate.Transition{fwstate.TransLive}` and the message's "want reload" to "want live".

Add to `target_test.go`:

```go
func w11a05Default(t *testing.T) ufwOp {
	t.Helper()
	op, err := defaultOp("incoming", "REJECT")
	if err != nil {
		t.Fatal(err)
	}
	return op
}

// TestW11a05LiveChangesKeepTheInputPolicy: with the real predictor, a rule
// and a logging change on a running UFW apply without a reload, verify
// against the live prediction, and leave the INPUT policy as it was.
func TestW11a05LiveChangesKeepTheInputPolicy(t *testing.T) {
	for _, tc := range []struct{ action, input, rule string }{
		{"firewall.rule.add", `{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8097","family":"both","comment":"w11a"}}`, "--dport 8097"},
		{"firewall.logging.set", `{"level":"high"}`, ""},
	} {
		t.Run(tc.action, func(t *testing.T) {
			h := newE2EHost(t, "enabled", "enabled")
			eng := eeEngine(t, h)
			h.ran = nil
			if _, cv, err := eePlanApply(t, eng, tc.action, tc.input); err != nil || cv.State != core.StatePending || cv.Error != nil {
				t.Fatalf("%s = %+v, %v; want pending after verify", tc.action, cv, err)
			}
			if len(h.ran) == 0 || slices.Contains(h.ran, "reload") {
				t.Errorf("ufw commands %q, want the change's own and no reload", h.ran)
			}
			if c := chainOf(h.loaded.V4, "filter", "INPUT"); c == nil || c.Policy != "DROP" {
				t.Errorf("INPUT = %+v after the apply, want policy DROP", c)
			}
			if tc.rule == "" {
				return
			}
			if c := chainOf(h.loaded.V4, "filter", "ufw-user-input"); c == nil || !slices.ContainsFunc(c.Rules, func(r netfilter.Rule) bool { return strings.Contains(r.Text, tc.rule) }) {
				t.Errorf("ufw-user-input = %+v, want the %s rule loaded live", c, tc.rule)
			}
		})
	}
}
```

`eeEngine` and `eePlanApply` are the engine helpers of `e2e_engine_test.go` (`:28`, `:51`); the rule input is the shape `e2e_engine_test.go:83-84` sends, the logging input the one `TestE2EVerifySysctl` sends.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestTargetTransitions|TestTargetCommands|TestW11a05' ./internal/modules/firewall/`
Expected: FAIL: the "rule on an active UFW" and the default rows report `reload ["reload"]`, `TestTargetCommands` reports `[reload]`, and both `TestW11a05LiveChangesKeepTheInputPolicy` subtests report a reload in `h.ran`.

- [ ] **Step 3: Implement**

1. `(*e2eHost).ufw` (test emulation): when the configuration is enabled and `ufw-user-input` is loaded, a command other than `reload`, `--force enable`, `disable` and `default …` predicts `fwstate.TransLive` from the edited configuration `next` (`predict.Predict(next, h.loaded, fwstate.TransLive)`), and a `default …` command predicts `fwstate.TransReload` from `next` (ufw's own restart). Sysctl handling as for the other transitions.
2. `target.go`: add `hasDefaultOp` and `selfTransition` per Contracts "Live snapshot". In `predictTarget`, before the existing reload case, add two cases, both requiring `want && cfg0.Enabled && running` (`running` is the switch's `ufwRunning(cur)`), ops present, no `restore`, no `repair` and no `reload`: without a `default` op, `tg.tr = TransLive` through `f.selfTransition(&tg, cur, fwstate.TransLive)`; with one, `tg.tr = TransReload` through `f.selfTransition(&tg, cur, fwstate.TransReload)`. `sysctlTr` takes the same transition. `f.transition` is not called for either. Every other case is unchanged; `c.reload` changes stay on the reload path until Task 6.
3. Update the stale assertions of `TestMutationLifecycle` (the effects become `firewall.rule:<id> made` only; the last call is the rule's own ufw command, not `ufw reload`) and of `TestE2EApplyVerifyRevert` (the apply's `ran` list loses `"reload"`; its revert keeps its reload until Task 7).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/firewall/...`
Expected: PASS. Any other pre-existing test that asserted `reload` for a rule or logging change on a running UFW is updated to the live expectation under Global Constraints, and the ledger names each one.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/*.go
git commit -m "fix(firewall): apply rule changes without reload"
```

### Task 6: Docker block changes load their chains live

**Files:**
- Modify: `internal/modules/firewall/target.go` (`predictTarget`, the `c.reload` case)
- Modify: `internal/modules/firewall/handler.go` (`(fw).run`, the apply sequence)
- Modify: `internal/modules/firewall/docker_effects.go` (`blockChainsResource`, `loadBlockChains`, `dockerDoc`)
- Modify: `internal/modules/firewall/inverse.go` (`undoDocker`, the `blockChainsResource` case)
- Test: `internal/modules/firewall/target_docker_test.go`, `internal/modules/firewall/docker_e2e_test.go`
- Test (update): any `internal/modules/firewall/*_test.go` that asserts a `reload` for a container or block change on a running UFW, under Global Constraints

**Interfaces:**
- Consumes: Contracts "TransLive", "Docker chains input"
- Produces: Contracts "Chain load step"

**Items:** 2026-10-21-v1-completion#437

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security, silent-failures

- [ ] **Step 1: Extend the test `internal/modules/firewall/target_docker_test.go::TestDtgReload`**

Change the first three rows' expectations to `fwstate.TransLive` with no commands (`"reload on an active UFW"`, `"reload alone on an active UFW"`, `"reload with enable on an active UFW"`), keep every other row, and add `{"a repair still reloads", tgRunning, true, change{repair: true, reload: true}, fwstate.TransReload, []string{"reload"}}`. In `TestDtgPredictsTheBlock`, keep its assertions: it now checks the live path, whose prediction loads the block through `dockerfw.ApplyChains`.

Add to `docker_e2e_test.go`:

```go
// TestW11a06ContainerAllowRunsNoReload: allowing a binding on a running
// UFW loads the Docker chains with -n and never reloads UFW.
func TestW11a06ContainerAllowRunsNoReload(t *testing.T) {
	h := deeNewHost(t)
	h.deePublishV6(t)
	var restores []string
	eng := deeEngineWith(t, h, w11a06Exec{h: h, seen: func(cmd core.Cmd) {
		if strings.HasSuffix(cmd.Path, "-restore") {
			restores = append(restores, cmd.Path+" "+strings.Join(cmd.Args, " "))
		}
	}})
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	h.ran, restores = nil, nil
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending || cv.Error != nil {
		t.Fatalf("allow = %+v, %v; want pending after verify", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands %q include a reload", h.ran)
	}
	if !slices.ContainsFunc(restores, func(s string) bool { return strings.Contains(s, "ip6tables") && strings.HasSuffix(s, " -n") }) {
		t.Errorf("restores %q, want an ip6tables restore with -n", restores)
	}
	for addr, want := range map[string]string{"0.0.0.0": "open", "::": "open"} {
		if got := deeInternet(t, h, "tcp", addr, 8080); got != want {
			t.Errorf("port 8080 on %s is %s after the allow, want %s", addr, got, want)
		}
	}
}

// TestW11a06ReconcileAfterARecreateRunsNoReload: the reconcile pass that
// follows a recreated container updates the block live.
func TestW11a06ReconcileAfterARecreateRunsNoReload(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	deeConfirm(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	a := h.deeAddr(t, "web").As4()
	h.recreate(t, "web", netip.AddrFrom4([4]byte{a[0], a[1], a[2], 9}))
	h.ran = nil
	system := core.Principal{Actor: "system:firewall.reconcile", Scope: core.ScopeAdmin}
	env := h.env()
	env.Principal = system
	var applied []string
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			return core.ChangeView{}, err
		}
		applied = append(applied, action)
		return eng.ApplySystem(ctx, system, core.ActionRef{Name: action, Version: 1}, raw)
	}}
	if err := (module{f: h.fw()}).BackgroundTasks()[0].Run(ctx, tc); err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("the pass applied nothing although web's address changed")
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the reconcile %q include a reload", h.ran)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "open" {
		t.Errorf("port 8080 is %s after the reconcile, want open", got)
	}
}

// w11a06Exec runs every command on the end-to-end host and reports each
// one to seen after it ran.
type w11a06Exec struct {
	h    *deeHost
	seen func(core.Cmd)
}

func (e w11a06Exec) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := e.h.Run(ctx, cmd)
	e.seen(cmd)
	return res, err
}
```

`(*deeHost).Run(ctx, core.Cmd)` is the end-to-end host's command entry point (`docker_e2e_host_test.go:271`); `deeFailUserReload` wraps it the same way.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestDtgReload|TestDtgPredictsTheBlock|TestW11a06' ./internal/modules/firewall/`
Expected: FAIL: the `TestDtgReload` rows report `reload with ["reload"]`, and both `TestW11a06` tests report a reload in `h.ran`.

- [ ] **Step 3: Implement**

1. `predictTarget`: extend Task 5's live case to changes with `c.reload` (still `want && cfg0.Enabled && ufwRunning(cur)`, no `restore`, no `repair`, no `default` op). After `predict(cfgN, cur, TransLive)`, for each family whose block Contracts "Chain load step" finds, replace the family's ruleset with `dockerfw.ApplyChains(rs, dockerfw.ChainsInput(block, <cur's ruleset of that family>))`. Skip IPv6 when `cfgN.IPv6` is false or `cur.V6 == nil`, as `kfwPlanOf` does.
2. `docker_effects.go`: add `blockChainsResource`, its `dockerDoc` observation and `loadBlockChains` per Contracts "Chain load step".
3. `handler.go` apply sequence: after the ufw commands and before the transition commands, when the target is `TransLive` and `c.reload` is set, build each family's input with `ChainsInput` from the block and the loaded state read just before the step, and call `loadBlockChains`. The snapshot capture is not touched here (Task 7).
4. `inverse.go` `undoDocker`: the `blockChainsResource` case per Contracts "Chain load step" (Task 6's part: `ufw reload` when the configuration says UFW is active), so a revert by inverses never meets an unknown resource.
5. `actions_container.go`, `docker_sources.go` and `actions_docker_enable.go` stay unchanged: they already set `c.files` and `c.reload`.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/firewall/...`
Expected: PASS, including every existing `TestDee…`, `TestE2E…`, `TestKfw…` and `TestDi…` test. A pre-existing test that asserted `reload` for a container or block change on a running UFW is updated to the live expectation, and the ledger names each one.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/*.go
git commit -m "fix(firewall): load the Docker chains without reload"
```

### Task 7: Live snapshots revert without reload

**Files:**
- Modify: `internal/modules/firewall/snapshot.go` (`capture`, new `imageKind`)
- Modify: `internal/modules/firewall/store.go` (`snapshotState.Live`)
- Modify: `internal/modules/firewall/handler.go` (`(mutation).Snapshot`, new `liveTarget`)
- Modify: `internal/modules/firewall/docker_revert.go` (`undoKeepingForeign`)
- Modify: `internal/modules/firewall/revert.go` (`undoNewestFirst`)
- Modify: `internal/modules/firewall/uninstall.go` (`restoreBaseline`)
- Modify: `internal/modules/firewall/e2e_guard_test.go` (`(*e2eHost).Run`: model `*-restore -n`)
- Test: `internal/modules/firewall/handler_test.go`, `internal/modules/firewall/e2e_engine_test.go`, `internal/modules/firewall/docker_e2e_test.go`
- Test (update): every `capture(…, false|true)` call in `snapshot_test.go`, `docker_snapshot_test.go`, `docker_revert_test.go` and `revert_test.go`; `TestMutationLifecycle` in `handler_test.go`; `TestE2EApplyVerifyRevert` in `e2e_engine_test.go`; the `revert_test.go` reload assertions of reverts by inverses whose change ran no UFW transition

**Interfaces:**
- Consumes: Contracts "TransLive", "Live snapshot" (`hasDefaultOp`, Task 5)
- Produces: Contracts "Live snapshot" (`imageKind`, `capture`, `snapshotState.Live`, `liveTarget`, `Snapshot`, `restoreBaseline`)

**Items:** 2026-10-21-v1-completion#437, 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security, silent-failures

- [ ] **Step 1: Write the failing tests**

Add to `handler_test.go`:

```go
// TestW11a07SnapshotImagesALiveChange: the snapshot of a change UFW
// applies live, or of a default change on a running UFW, holds the
// before-image marked live; a repair's change holds none unless its
// action asks for its repair image.
func TestW11a07SnapshotImagesALiveChange(t *testing.T) {
	ctx := context.Background()
	defaultIncoming := func(fwstate.Config, fwstate.Loaded) (change, error) {
		op, err := defaultOp("incoming", "REJECT")
		return change{ops: []ufwOp{op}}, err
	}
	repair := func(fwstate.Config, fwstate.Loaded) (change, error) { return change{repair: true}, nil }
	for _, tc := range []struct {
		name            string
		build           buildFunc
		image           bool
		wantImage, live bool
	}{
		{"a rule applies live", nil, false, true, true},
		{"a default restarts UFW itself", defaultIncoming, false, true, true},
		{"a repair reloads", repair, false, false, false},
		{"a repair action keeps its repair image", repair, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, _ := pnHost(t)
			m := hkMutation(f, tc.build)
			m.image = tc.image
			raw, err := m.Snapshot(ctx, h.env(), hkInput{Guarded: core.Guarded{Guard: &core.GuardContext{Complete: true}}})
			if err != nil {
				t.Fatal(err)
			}
			snap, err := getSnapshot(ctx, h.db, string(raw))
			if err != nil {
				t.Fatal(err)
			}
			if (snap.State.Image != nil) != tc.wantImage || snap.State.Live != tc.live {
				t.Errorf("image %v live %v, want image %v live %v", snap.State.Image != nil, snap.State.Live, tc.wantImage, tc.live)
			}
		})
	}
}
```

`pnHost`'s loaded state holds `ufw-user-input` (`host_test.go:38-39`), so its UFW counts as running.

Add to `e2e_engine_test.go`:

```go
// TestW11a07ManualRuleAfterALiveChangeRevertsByInverses: a rule added by
// hand after a live change sends its revert by inverses: the change's rule
// is deleted live, the manual one stays, and UFW is never reloaded.
func TestW11a07ManualRuleAfterALiveChangeRevertsByInverses(t *testing.T) {
	ctx := context.Background()
	h := newE2EHost(t, "enabled", "enabled")
	eng := eeEngine(t, h)
	_, cv, err := eePlanApply(t, eng, "firewall.rule.add",
		`{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8097","family":"both","comment":"w11a"}}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("rule.add = %+v, %v; want pending", cv, err)
	}
	if res := h.ufw(strings.Fields("allow in proto tcp from any to any port 8098")); res.ExitCode != 0 {
		t.Fatalf("the manual ufw allow failed: %s", res.Stderr)
	}
	h.ran = nil
	cv, err = eng.Revert(ctx, eeUser, cv.ID)
	if err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the revert %q include a reload", h.ran)
	}
	keys := strings.Join(eeUserChain(h), "\n")
	if strings.Contains(keys, "--dport 8097") || !strings.Contains(keys, "--dport 8098") {
		t.Errorf("the kernel's user chain after the revert:\n%s\nwant the 8098 rule kept and the 8097 rule gone", keys)
	}
}
```

Add to `docker_e2e_test.go`:

```go
// TestW11a07RevertOfAContainerAllowRunsNoReload: an unconfirmed allow is
// reverted from its snapshot's before-image: the binding closes again and
// UFW is never reloaded.
func TestW11a07RevertOfAContainerAllowRunsNoReload(t *testing.T) {
	h := deeNewHost(t)
	h.deePublishV6(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("allow = %+v, %v; want pending", cv, err)
	}
	h.ran = nil
	if cv, err = eng.Revert(context.Background(), eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the revert %q include a reload", h.ran)
	}
	for addr, want := range map[string]string{"0.0.0.0": "filtered", "::": "filtered"} {
		if got := deeInternet(t, h, "tcp", addr, 8080); got != want {
			t.Errorf("port 8080 on %s is %s after the revert, want %s", addr, got, want)
		}
	}
}

// TestW11a07RevertOfADefaultAddsNoReload: the revert of a default-policy
// change loads its snapshot's before-image and runs no reload.
func TestW11a07RevertOfADefaultAddsNoReload(t *testing.T) {
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	_, cv, err := deePlanApply(t, eng, "firewall.defaults.set", `{"incoming":"REJECT"}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("defaults.set = %+v, %v; want pending", cv, err)
	}
	h.ran = nil
	if cv, err = eng.Revert(context.Background(), eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the revert %q include a reload", h.ran)
	}
}
```

The defaults input is the shape `e2e_guard_test.go:359` sends.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW11a07' ./internal/modules/firewall/`
Expected: FAIL, a build error naming `Live` (`snapshotState` has no field `Live`). After adding only the field, each test fails on its assertion: the snapshot subtests report `image false live false`, and the three revert tests report a reload in `h.ran` (a whole restore of a snapshot without an image chooses `TransReload`; the revert by inverses reloads after the rule's inverse).

- [ ] **Step 3: Implement**

1. `snapshot.go`: `imageKind` and the new `capture` parameter per Contracts "Live snapshot"; update every call site (`handler.go` and the test files listed above: `false` → `imageNone`, `true` → `imageRepair`, the `withImage` loop variable of `docker_snapshot_test.go:57` mapped the same way).
2. `store.go`: `snapshotState.Live` per Contracts, with a field comment.
3. `handler.go`: `liveTarget` and `(mutation).Snapshot` per Contracts. Keep the existing pin of a `keep` snapshot around the capture.
4. `docker_revert.go` `undoKeepingForeign`: after `undoNewestFirst`, return nil when `snap.State.Image == nil || snap.State.Live`; the image of a repair keeps its current handling.
5. `revert.go` `undoNewestFirst`: run `reloadIfActive` only when an inverse of a rule, default or logging effect ran (`ufwEffect`, as today) and `made` holds an effect with `Resource == resourceEnabled`, which only a change that ran a UFW transition (reload, enable or disable) makes (`transitionOp`, `enableOp`). A live change and a default change make none; the inverse of a `default` command restarts UFW itself (W11-R4).
6. `uninstall.go` `restoreBaseline`: clear `snap.State.Image` when `snap.State.Live` is set, before `restore` (Contracts).
7. `(*e2eHost).Run` (test emulation): a command whose path ends in `-restore` with `Args` equal to `[]string{"-n"}` applies `cmd.Stdin` with `deeRestore` to `h.loaded.V6` when the path contains `ip6tables` and to `h.loaded.V4` otherwise, and returns exit code 1 with the error on stderr when it fails.
8. Stale tests: `TestMutationLifecycle` now expects the before-image (`snap.State.Image != nil && snap.State.Live`), and its revert loads it: give its fake host an `onRun` that sets the loaded state to the image's when an `*-restore` runs, as `axImageRepair` does (`revert_test.go:663-702`). `TestE2EApplyVerifyRevert` asserts that its revert ran no `reload` instead of "the restore's reload last".

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/firewall/...` then `GOTOOLCHAIN=local timeout 900 go test -race -count=1 ./internal/modules/firewall/...`
Expected: PASS. `TestAxRevertByInversesLoadsTheBeforeImage` and the other repair-image tests keep their behaviour: their snapshots are `imageRepair`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/*.go
git commit -m "fix(firewall): revert live changes from an image"
```

### Task 8: Docker chain reverts and repairs without reload

**Files:**
- Modify: `internal/modules/firewall/image.go` (`planFamily`, `imageMismatch`, new `imageBlock`)
- Modify: `internal/modules/firewall/inverse.go` (`inverse`, `undoDocker`: the `blockChainsResource` case and the `firewall.chains:docker` "loaded" case)
- Modify: `internal/modules/firewall/docker_cleanup.go` (`cleanupChains`, `cleanupSteps`: the trailing reload)
- Modify: `internal/modules/firewall/docker_repair.go` (the `build` closure of `firewall.docker.repair`)
- Test: `internal/modules/firewall/docker_e2e_test.go`
- Test (update): the direct `undoDocker` call in `docker_revert_test.go:534`; the reload assertions of `docker_cleanup_test.go` for a cleanup in `cleanupPreserve` mode; any `revert_test.go` or `image_test.go` expectation of a restore input that a stale `darkpanel-`/`dp-b-` chain now extends

**Interfaces:**
- Consumes: Contracts "Docker chains input", "Chain load step", "Live snapshot"
- Produces: Contracts "Live snapshot" (`imageBlock`, `planFamily`, `imageMismatch`, `undoDocker`)

**Items:** 2026-10-21-v1-completion#437, 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security, silent-failures

- [ ] **Step 1: Write the failing tests**

Add to `docker_e2e_test.go`:

```go
// TestW11a08RevertLeavesNoStaleChain: the image load of a reverted allow
// deletes the binding's chain the allow created.
func TestW11a08RevertLeavesNoStaleChain(t *testing.T) {
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	before := deeOwnedChains(h)
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("allow = %+v, %v; want pending", cv, err)
	}
	if cv, err = eng.Revert(context.Background(), eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if got := deeOwnedChains(h); !reflect.DeepEqual(got, before) {
		t.Errorf("Darkpanel chains after the revert %v, want %v as before the allow", got, before)
	}
}

// TestW11a08ByInversesRevertOfAContainerAllowRunsNoReload: a manual ufw
// rule after an allow sends its revert by inverses; the Docker chains load
// from the snapshot's image and UFW is never reloaded.
func TestW11a08ByInversesRevertOfAContainerAllowRunsNoReload(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	before := deeOwnedChains(h)
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("allow = %+v, %v; want pending", cv, err)
	}
	if res, err := h.Run(ctx, core.Cmd{Path: ufwPath, Args: strings.Fields("allow in proto tcp from any to any port 8098")}); err != nil || res.ExitCode != 0 {
		t.Fatalf("the manual ufw allow = %+v, %v", res, err)
	}
	h.ran = nil
	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the revert %q include a reload", h.ran)
	}
	if got := deeOwnedChains(h); !reflect.DeepEqual(got, before) {
		t.Errorf("Darkpanel chains after the revert %v, want %v as before the allow", got, before)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "filtered" {
		t.Errorf("port 8080 is %s after the revert, want filtered", got)
	}
}

// TestW11a08RevertOfADockerEnableRunsNoReload: reverting an unconfirmed
// enable removes Darkpanel's chains without reloading UFW.
func TestW11a08RevertOfADockerEnableRunsNoReload(t *testing.T) {
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	_, cv, err := deePlanApply(t, eng, "firewall.docker.enable", `{}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("enable = %+v, %v; want pending", cv, err)
	}
	h.ran = nil
	if cv, err = eng.Revert(context.Background(), eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the revert %q include a reload", h.ran)
	}
	if owned := deeOwnedChains(h); len(owned) != 0 {
		t.Errorf("the revert left the chains %v loaded", owned)
	}
}

// TestW11a08OwnedChainRepairRunsNoReload: a repair whose differences lie
// only in Darkpanel's chains loads them live.
func TestW11a08OwnedChainRepairRunsNoReload(t *testing.T) {
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	deeConfirm(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	v4, err := deeRestore(h.loaded.V4, []byte("*filter\n-F darkpanel-docker-deny\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.loaded.V4 = v4
	h.ran = nil
	if _, cv, err := deePlanApply(t, eng, "firewall.docker.repair", `{}`); err != nil || cv.State != core.StatePending {
		t.Fatalf("repair = %+v, %v; want pending", cv, err)
	}
	if slices.Contains(h.ran, "reload") {
		t.Errorf("ufw commands of the repair %q include a reload", h.ran)
	}
	if c := chainOf(h.loaded.V4, "filter", "darkpanel-docker-deny"); c == nil || len(c.Rules) == 0 {
		t.Errorf("darkpanel-docker-deny = %+v after the repair, want its rules loaded again", c)
	}
}
```

`deeOwnedChains` (`docker_e2e_test.go:95`) lists the loaded Darkpanel chains; `deeRestore` (`docker_e2e_host_test.go:396`) applies `-n` input to a ruleset without changing it.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW11a08' ./internal/modules/firewall/`
Expected: FAIL: `TestW11a08RevertLeavesNoStaleChain` lists the allow's `dp-b-` chain still loaded; `TestW11a08ByInversesRevertOfAContainerAllowRunsNoReload` reports the reload of the `blockChainsResource` inverse; `TestW11a08RevertOfADockerEnableRunsNoReload` reports the reload of the restore's pre-cleanup (`cleanupSteps`); `TestW11a08OwnedChainRepairRunsNoReload` reports the repair's reload.

- [ ] **Step 3: Implement**

1. `image.go`: `planFamily`'s stale-chain lines, `imageMismatch`'s check and `imageBlock`, per Contracts "Live snapshot".
2. `inverse.go`: `undoDocker` gains `snap` and `inverse` passes it; the image branch of the `blockChainsResource` case and of the `firewall.chains:docker` "loaded" case, per Contracts. Each family's input is built from the loaded state read just before the load. Update the direct call at `docker_revert_test.go:534`.
3. `docker_cleanup.go`: `cleanupSteps` gains a `reload bool` parameter, passed by `cleanupChains` as `mode == cleanupRefuse`, and runs its trailing reload only when it is true. Uninstall cleans up in `cleanupRefuse` mode and keeps its reload, and `uninstall.go:150` keeps its own; the preserve-mode cleanups of an inverse and of a restore's pre-cleanup no longer reload, and `verifyCleanup` stays their check.
4. `docker_repair.go`: in the `build` closure, compute `f.syncDiffs(cfg, cur)`; when it returns no error and every difference has `Table == "filter"` and `dockerfw.Owned(Chain)`, return `dockerRepairChange`'s change with `repair` cleared, so it applies live through Task 6's chain load step; otherwise keep `repair: true, reload: true` (W11-R3).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/firewall/...` then `GOTOOLCHAIN=local timeout 900 go test -race -count=1 ./internal/modules/firewall/...`
Expected: PASS. `TestKfwRestore` and `TestDiInverseChains` keep their reload: their snapshots carry no before-image (W11-R3).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/*.go
git commit -m "fix(firewall): revert Docker chains without reload"
```

### Task 9: Threat model and user guide

**Files:**
- Modify: `docs/security/threat-model.md` (area "Firewall", parts "Mitigations" and "Residual risk")
- Modify: `docs/user/modules/firewall.md` (any sentence saying a change reloads UFW; the default-policy section)
- Test: `internal/docs/threat_model_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 2026-10-21-v1-completion#437, 2026-10-21-v1-completion#438

**Implementer:** dforge:impl-sonnet-low
**Executor:** agy gemini-3.8-flash / medium
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Add to `internal/docs/threat_model_test.go`:

```go
// TestW11a09FirewallStatesLiveApply: the firewall area says which changes
// apply live and which still restart UFW.
func TestW11a09FirewallStatesLiveApply(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for key, phrases := range map[string][]string{
		"Firewall/Mitigations": {
			"Darkpanel does not reload an active UFW for rule, logging, container or Docker block changes",
		},
		"Firewall/Residual risk": {
			"`ufw default` restarts an active UFW",
			"enabling, disabling and repairing UFW, and restoring a snapshot, restart it",
		},
	} {
		text := strings.Join(d.parts[key], "\n")
		for _, p := range phrases {
			if !strings.Contains(text, p) {
				t.Errorf("%s lacks %q", key, p)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestW11a09|TestW10Tm' ./internal/docs/`
Expected: FAIL naming `Firewall/Mitigations` and the first missing phrase.

- [ ] **Step 3: Edit the documents**

1. `threat-model.md`, area "Firewall": under "Mitigations", one bullet with the first phrase exactly, saying UFW applies those commands live and Darkpanel loads its Docker chains atomically per family, and naming `internal/modules/firewall/dockerfw/restore_input.go` in backticks (`TestW10TmMitigationsNameExistingRepositoryPaths` fails a Mitigations bullet that names no existing repository path). Under "Residual risk", one bullet containing the second and third phrases exactly (the command in backticks), saying the built-in policies are ACCEPT for about a second in that window.
2. `docs/user/modules/firewall.md`: replace any statement that a rule or container change reloads UFW with the live behaviour, and add one sentence to the default-policy section that changing a default policy restarts UFW briefly.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/docs/`
Expected: PASS. Then push the branch and dispatch `harness.yml`; record the run id. `TestM3Firewall` (including `live-apply`) and `TestM3Docker` (including the traffic-recreate scenario, with no leak window) pass on all four images (W11-R6).

- [ ] **Step 5: Commit**

```bash
git add docs/security/threat-model.md docs/user/modules/firewall.md internal/docs/threat_model_test.go
git commit -m "docs(security): record live ufw apply and residuals"
```
