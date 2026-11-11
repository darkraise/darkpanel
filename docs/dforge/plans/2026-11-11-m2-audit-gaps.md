# M2 audit gaps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the gaps the M2 milestone audit of 2026-11-11 (ca239de) found, so that M2 can be audited again and closed.

**Architecture:** Each task is one slice with its own regression test. Tasks 1 to 5 fix the three install and uninstall defects of core register rows 70, 71 and 72 and the damaged-key refusal of row 84. Task 6 runs the credential-writer tests under the race detector (row 89). Tasks 7 and 8 fix the two real defects inside the minor rows 31 and 33. Rulings M2G-R1 to M2G-R8 under Assumptions settle every other sub-item; they were written into the register when this plan was written.

**Tech Stack:** Go 1.27.4, GNU Make, GitHub Actions, Markdown ADRs.

**Spec:** docs/dforge/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented tasks total 8 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/m2g-tmp/integ && export TMPDIR=/root/.cache/m2g-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/agent/daemon/ && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Plan review:** 2026-11-11 — dforge:judge-opus — executability 14 / coherence 17 / coverage 17 / assumptions 13 (round 1)

## Global Constraints

- Scope is the twelve GAP rows of `.dforge/milestone-M2-audit-report.md`, as rulings M2G-R1 to M2G-R8 under Assumptions narrow them. No task edits the spec.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. The install and uninstall tests run on `Rooted(t.TempDir())` layouts with the fakes in `internal/install/fakes_test.go`.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/m2g-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends.
- No task runs `go clean -cache` or `go clean -testcache`: the build cache is shared with other sessions on this host.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `timeout 1300 go test -timeout 20m -count=1` on the task's packages (every line `ok`); `golangci-lint run --allow-serial-runners` on the task's packages (`0 issues.`).
- Generated files: no task changes a wire type or an action schema, so `schemas/**` and `docs/api/**` stay unchanged.
- TDD: write the test, run it and see the expected RED, then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A test that pins behaviour already present passes before the change; the step says so.
- Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- Existing tests whose expected systemd calls, plan lines or messages a task changes are updated in the same task, to exactly the new values the task states; no other expectation changes.
- The plan runs on branch `m2-audit-gaps` in the base repository, with no worktree.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name or keep the step-prefixed form of their file; a changed refusal keeps its error code.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies: the diff touches `internal/install/`. Push the branch and run `gh workflow run harness.yml --ref m2-audit-gaps` to completion before the final review. No task creates or pushes a tag or runs `release.yml`.

## Contracts

**K1. Update reservation against the uninstall barrier (Task 1), `internal/core/lifecycle`.**
- `BeginUninstall(ctx, db, stopTasks)`, with `stopTasks` true or false, refuses while any owner holds the lock key `*` (`ReserveAll`), inside the same write transaction that would set the barrier, with `errcode.UpdateInProgress` and the detail `update operation <owner> is in progress; wait for it to finish, or roll it back with darkpanel update rollback --local; a finished operation's reservation is cleared by darkpanel update recover`, where `<owner>` is the `core_locks.owner` value (for example `op:01OPA`). A refused call sets no barrier row. With `stopTasks` false the existing busy check still runs and keeps its `errcode.ChangeBusy` refusal; the reservation check runs first.
- `Reserve(ctx, db, key, owner)` refuses with `errcode.Uninstalling` (the existing `uninstallingErr` detail) while the uninstall barrier is set, before any other check and inside its own write transaction, and writes no lock row.

**K2. Uninstall service list (Task 2), `internal/install/uninstall.go`.** The services step handles, in this order, `darkpanel-controller.service`, `darkpanel-agent.service`, `darkpanel-rollback.service`, `darkpanel-update-recover.service`: it stops and waits for each present unit except the recover unit, then disables every present one in one call. Its plan line is `stop and disable darkpanel-controller.service, darkpanel-agent.service, darkpanel-rollback.service, darkpanel-update-recover.service`.

**K3. Agent lock helper (Task 5), new file `internal/install/agentlock.go`.** `func lockAgentDir(dir string) (int, error)` opens `<dir>/agent.lock` (`AgentLockName`) with `O_RDWR|O_CREAT|O_NOFOLLOW|O_CLOEXEC`, mode 0600, and takes `LOCK_EX|LOCK_NB`; it returns the descriptor, which the caller closes to release. A held lock is the error `an agent still runs and holds <path>; stop it first`; any other failure is `open <path>: <err>` or `lock <path>: <err>`. `func waitStopped(ctx context.Context, sd platform.Systemd, unit string, poll, timeout time.Duration) error` is the existing `(*Uninstall).waitStopped` loop moved to a function, with its message unchanged; `(*Uninstall).takeAgentLock` and `(*Uninstall).waitStopped` call them.

## Assumptions (evidence)

- The update operation holds `*` from `Reserve` until every terminal path releases it: `internal/update/updater.go` reserves before writing the first record, and the harness update scenarios assert empty `core_locks` after each terminal path (audit item 15). So refusing an uninstall while `*` is held covers every unfinished update, its runner unit and its deadline timer.
- `Reserve` and `BeginUninstall` both run inside `db.Write`, which serialises writers on agent.db (`internal/core/store/db.go:105`), so the check in K1 has no window between them.
- The agent daemon holds an exclusive flock on `AgentDir/agent.lock` for its whole run (`internal/agent/daemon/daemon.go:164-181`, `TestRunHoldsTheAgentLock`), so taking that lock in Task 5 proves no agent runs.
- After Task 5, an install rerun that fails at a step after `database` leaves the agent stopped, where today it stays up; the next rerun's agent step starts it again. That is accepted: the alternative, an agent running against a database a newer binary migrated, is the defect being fixed.
- A stale `*` row of a finished operation is cleared by the sweep that every `Recover` pass runs (`internal/update/recover.go` Recover); the K1 detail names `darkpanel update recover` for that case.
- `darkpanel-rollback.service` only restarts services when a record is swapped or rolling back (`internal/update/recover.go` RecoverFailure, `rollback.go` restartServices); with K1 no such record exists during an uninstall, and K2 stops a rollback a failed service stop started. Disabling a unit without an `[Install]` section changes nothing (systemd `DisableUnitFiles`); unverified on a real host — gate 7's uninstall scenario verifies it.
- `secret.LoadOrCreateKey` refuses any file content other than one prefix line of 32 bytes in standard base64; Task 4's `damaged` key fails at the prefix check (`internal/core/secret/key.go:127-129`).
- A first install that created the `darkpanel` user but failed before its database step leaves the user unrecorded, so uninstall later keeps it. That is the safe direction (an extra account left behind, never an operator's account deleted) and is accepted; Task 3 changes the `chown fails` case of `TestInstallRerunAfterEachCheckpoint` to expect it.
- The race detector works on this host: `go test -race -count=1 ./internal/agent/daemon/` passed in 8.7 s on 2026-11-11. The CI Go job's runner has a C toolchain for `-race`: unverified — Task 6 verifies it through gate 6.
- The release workflow is the only release path and runs `make release VERSION=...` with the default `KEYS=production` and no `-j` (`.github/workflows/release.yml:99`, `Makefile:20`).

- Plan review round 1 scored assumptions 13 (borderline). Decision: accepted after this round's fixes, which add the evidence it asked for (db.go:105, daemon.go:164-181, key.go:127-129) and state the stopped-agent and stale-reservation cases; the CI race-detector toolchain stays unverified until gate 6.

Rulings (written into `docs/dforge/registers/2026-10-19-m2-fleet-lifecycle-core.md` with this plan; the owner may override any of them before execution):

- **M2G-R1, row 30 (deferred).** Every item is a test gap, a duplicated query or a comment, none a behaviour defect; the recorder reset without the lock is test-only code.
- **M2G-R2, row 31 (planned, rest deferred).** Task 7 fixes the `Manifest().OwnedPaths` alias. Deferred: the Options.Commands and NewHosts comments, the per-call `core_hosts` read in Hosts.Agent (one indexed read), the slots_test literals and order, and the StaticClients generation check — comments and test gaps, no behaviour defect.
- **M2G-R3, row 32 (deferred).** A negative limit fails closed: `ParseChangeFilter` refuses a limit below 1 (`internal/agent/wire/wire.go`). A sub-second confirm timeout cannot reach the wire: the API and CLI accept whole seconds from 10 to 900 only (`internal/controller/api/actions.go:159-160`, `internal/cli/mutate.go:139`). `DBusSystemd.Close` with a nil connection is reachable only on a value `newSystemd` builds for tests (`internal/platform/systemd.go:98`). The rest is doc-comment and test-assertion text.
- **M2G-R4, row 33 (planned, rest deferred).** Task 8 reconciles ADR 0012 with the code. Deferred: TestFS's nested released files, the unwrapped `SnapshotHashes` error (it still reaches the caller), and the default-build trustedkeys loops over an empty list — test gaps.
- **M2G-R5, row 36 (verify, Acceptance amended).** The two hardening items implemented in `release/*.sh` have test rows (`sign.test.sh` checksum_mismatch_not_signed, other_version). The other six are workflow, Makefile and README text, checked where they stand (`release.yml:29,69,133,188` persist-credentials, `:106-108` stable tag pattern, `:214` `--latest`; `Makefile:7` `--match 'v*'`, `:139` git-cliff check; `release/README.md:71-73`) and exercised by release run 42960323954. A shell test that greps workflow text would only restate it. Set to `verify` for the owner's check.
- **M2G-R6, row 39 (deferred).** `make release` in a linked worktree: releases are built by `release.yml` from a plain checkout, and the project uses no worktrees. The built binary in the sign job runs only `version --json`, after `gh attestation verify` proved this workflow built it from this commit, in a step whose environment holds no secret (the secrets are scoped to the "Sign checksums.txt" step). `KEYS` values, `make -j` ordering and the on-main check concern invocations the release workflow does not make. The git-cliff warning and the `docs/versioning.md` and README wording are text.
- **M2G-R7, row 79 (deferred).** An oversized error answer is refused either way, and only the message loses the status. The hardcoded Protocol and the slicing panics are in tests, where a panic still fails the test.
- **M2G-R8, row 84 (planned, rest deferred).** Task 4 fixes the damaged-key refusal, the module-skip message, the data-step plan line, the unchecked `got[0]` and adds the failing-userdel test. Not a defect: `owned()` appends to slices that `platform.OwnedPaths` already cloned (`internal/platform/files.go` OwnedPaths) and clones `BlockIDs` before appending. Deferred: tests for a kept drop-in directory, a symlinked directory and a lock error other than EWOULDBLOCK — test gaps on code no task here changes.
- ADR 0012's Status stays `Proposed` in this plan; core row 22 sets it to `Accepted` when M2 closes, which is the closing audit's duty.

## Task index

1. Refuse an uninstall while an update holds the reservation
2. Stop the rollback unit and surface the update refusal in uninstall
3. Remove only a darkpanel user that install created
4. Plan an uninstall past a damaged agent key
5. Migrate agent.db only under the agent lock
6. Run the credential writer tests under the race detector
7. Copy the core module's owned paths into its manifest
8. Reconcile ADR 0012 with the code

---

### Task 1: Refuse an uninstall while an update holds the reservation

**Files:**
- Modify: `internal/core/lifecycle/uninstall.go` (`BeginUninstall`)
- Modify: `internal/core/lifecycle/reserve.go` (`Reserve`)
- Test: `internal/core/lifecycle/uninstall_test.go`

**Interfaces:**
- Consumes: none
- Produces: K1

**Items:** 70
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8

- [ ] **Step 1: Write the failing test**

Append to `internal/core/lifecycle/uninstall_test.go` (it already imports `context`, `strings`, `testing` and `errcode`; `opA` and `f.reserve` are in `reserve_test.go`):

```go
func TestBeginUninstallRefusesWhileAnUpdateHoldsTheReservation(t *testing.T) {
	for _, stopTasks := range []bool{false, true} {
		f := newFixture(t)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		err := BeginUninstall(context.Background(), f.db, stopTasks)
		wantCode(t, err, errcode.UpdateInProgress)
		want := "update operation " + opA + " is in progress; wait for it to finish, or roll it back with darkpanel update rollback --local; " +
			"a finished operation's reservation is cleared by darkpanel update recover"
		if msg := errcode.From(err).Detail; msg != want {
			t.Errorf("stopTasks=%v: refusal = %q, want %q", stopTasks, msg, want)
		}
		if n := f.uninstalling(); n != 0 {
			t.Errorf("stopTasks=%v: a refused BeginUninstall left %d barrier rows", stopTasks, n)
		}
	}
}

func TestReserveRefusedWhileUninstalling(t *testing.T) {
	f := newFixture(t)
	if err := BeginUninstall(context.Background(), f.db, false); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.reserve(opA), errcode.Uninstalling)
	if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("lock rows after the refused Reserve = %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestBeginUninstallRefusesWhileAnUpdateHoldsTheReservation|TestReserveRefusedWhileUninstalling' ./internal/core/lifecycle/`
Expected: FAIL — `err = <nil>, want code update.in_progress` (the code string as `errcode` prints it) for the first test, and `err = <nil>, want code core.uninstalling` for the second.

- [ ] **Step 3: Implement**

1. In `BeginUninstall`'s `db.Write` callback, before the `!stopTasks` busy check, query `SELECT owner FROM core_locks WHERE key = ? ORDER BY owner LIMIT 1` with `ReserveAll`. On a row, return `errcode.New(errcode.UpdateInProgress, ...)` with the K1 detail. On `sql.ErrNoRows` go on; any other error is returned as is (the function's existing wrapping applies).
2. In `Reserve`'s `db.Write` callback, call `uninstallingErr(ctx, tx)` first and return its error. Nothing else in `Reserve` changes, including the same-owner case that answers nil.
3. Update `BeginUninstall`'s and `Reserve`'s doc comments to state the new refusal in one sentence each.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/core/lifecycle/ ./internal/update/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/uninstall.go internal/core/lifecycle/reserve.go internal/core/lifecycle/uninstall_test.go
git commit -m "fix(lifecycle): keep updates and uninstall apart"
```

### Task 2: Stop the rollback unit and surface the update refusal in uninstall

**Files:**
- Modify: `internal/install/uninstall.go` (`plan`, the `services` slice)
- Test: `internal/install/uninstall_barrier_test.go`, `internal/install/uninstall_test.go` (`TestUninstallRemovesEverything`)

**Interfaces:**
- Consumes: K1
- Produces: K2

**Items:** 70
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Append to `internal/install/uninstall_barrier_test.go` (add `slices` to its imports):

```go
func TestUninstallRefusesWhileAnUpdateRuns(t *testing.T) {
	for _, stopTasks := range []bool{false, true} {
		h, m := installHost(t, RoleController)
		barDB(t, h, func(db *store.DB) error {
			return lifecycle.Reserve(context.Background(), db, lifecycle.ReserveAll, lifecycle.ReservationOwner("01BARUPDATE"))
		})
		cfg := h.uninstallConfig(m, false)
		cfg.StopTasks = stopTasks
		_, err := runUninstall(t, cfg)
		if e := errcode.From(err); e == nil || e.Code != errcode.UpdateInProgress ||
			!strings.Contains(e.Detail, "op:01BARUPDATE") {
			t.Fatalf("stopTasks=%v: Run = %v, want update.in_progress naming op:01BARUPDATE", stopTasks, err)
		}
		if !strings.Contains(err.Error(), "uninstall: barrier: ") {
			t.Errorf("stopTasks=%v: Run = %v, want the barrier step named", stopTasks, err)
		}
		if got := h.systemd.recorded(); slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "Stop ") }) {
			t.Errorf("stopTasks=%v: systemd calls %v, want no unit stopped while an update runs", stopTasks, got)
		}
		if len(m.calls) != 0 || !exists(t, h.layout.Paths.Binary()) {
			t.Errorf("stopTasks=%v: a refused uninstall changed the host", stopTasks)
		}
		if n := barRows(t, h); n != 0 {
			t.Errorf("stopTasks=%v: barrier rows after the refusal = %d, want 0", stopTasks, n)
		}
	}
}
```

- [ ] **Step 1b: Extend the test `internal/install/uninstall_test.go::TestUninstallRemovesEverything`**

Replace its `want` systemd call list with:

```go
	want := []string{
		"Stop darkpanel-controller.service", "Stop darkpanel-agent.service", "Stop darkpanel-rollback.service",
		"Disable darkpanel-controller.service darkpanel-agent.service darkpanel-rollback.service darkpanel-update-recover.service",
		"Stop demo.service", "Disable demo.service", "Reload",
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestUninstallRefusesWhileAnUpdateRuns|TestUninstallRemovesEverything' ./internal/install/`
Expected: `TestUninstallRefusesWhileAnUpdateRuns` PASSES already (Task 1's K1 refusal reaches the barrier step through `lifecycle.BeginUninstall`; record that it pins behaviour); `TestUninstallRemovesEverything` FAILS with the `systemd calls` diff lacking `Stop darkpanel-rollback.service`.

- [ ] **Step 3: Implement**

1. In `(*Uninstall).plan`, set `services` to K2's four units in K2's order (`update.ControllerUnit, update.AgentUnit, update.RollbackUnit, RecoverUnit`). `stopServices` needs no change: it already skips stopping the recover unit and disables every present unit.
2. In `TestUninstallStopsModuleUnits` (`internal/install/uninstall_test.go`), set `want` to:

```go
	want := []string{
		"Stop darkpanel-controller.service", "Stop darkpanel-agent.service", "Stop darkpanel-rollback.service",
		"Disable darkpanel-controller.service darkpanel-agent.service darkpanel-rollback.service darkpanel-update-recover.service",
		"Stop darkpanel-kinds.service", "Disable darkpanel-kinds.service", "Stop demo.service", "Disable demo.service", "Reload",
	}
```

3. Run `rg -n 'darkpanel-update-recover.service' internal/install/*_test.go cmd/darkpanel/*_test.go`; any other expectation of the uninstall services step that lists the old three units gets K2's list and nothing else.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/install/ ./cmd/darkpanel/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/install/uninstall.go internal/install/uninstall_barrier_test.go internal/install/uninstall_test.go
git commit -m "fix(install): stop the rollback unit on uninstall"
```

### Task 3: Remove only a darkpanel user that install created

**Files:**
- Modify: `internal/install/install.go` (`installer` struct, `ensureUser`, `database`)
- Modify: `internal/install/uninstall.go` (`load`)
- Test: `internal/install/install_test.go`, `internal/install/uninstall_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 71
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8

- [ ] **Step 1: Write the failing test**

Append to `internal/install/install_test.go`:

```go
func TestInstallKeepsAUserItDidNotCreate(t *testing.T) {
	h := newTestHost(t)
	h.users.users[ServiceUser] = [2]int{1500, 1500}
	if err := Install(context.Background(), h.config(RoleController)); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	if got := h.runner.commands(); len(got) != 0 {
		t.Errorf("commands %v, want no useradd for an existing user", got)
	}
	if got := manifest(t, h.layout); slices.Contains(got, "user "+ServiceUser) {
		t.Errorf("manifest records the user %s it did not create: %v", ServiceUser, got)
	}
	if ids, _ := h.chowns.of(h.layout.Paths.ControllerDir); ids != [2]int{1500, 1500} {
		t.Errorf("controller directory owned by %v, want the existing user's ids", ids)
	}
}

func TestInstallRerunKeepsTheUserItCreated(t *testing.T) {
	h := newTestHost(t)
	ctx := context.Background()
	for range 2 {
		if err := Install(ctx, h.config(RoleController)); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
	}
	if got := manifest(t, h.layout); !slices.Contains(got, "user "+ServiceUser) {
		t.Errorf("manifest after a rerun lacks the user %s the first run created: %v", ServiceUser, got)
	}
}
```

Append to `internal/install/uninstall_test.go`:

```go
func TestUninstallKeepsAUserItDidNotCreate(t *testing.T) {
	for _, role := range []Role{RoleController, RoleAgent} {
		h := newTestHost(t)
		h.users.users[ServiceUser] = [2]int{1500, 1500}
		cfg := h.config(role)
		if role == RoleAgent {
			cfg.Token = "dpe1.t"
		}
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("%s: Install: %v\n%s", role, err, h.out)
		}
		if role == RoleAgent {
			h.systemd.missing["darkpanel-controller.service"] = true
		}
		ucfg := h.uninstallConfig(&demoModule{l: h.layout}, false)
		ucfg.Modules = nil
		if out, err := runUninstall(t, ucfg); err != nil {
			t.Fatalf("%s: Run: %v\n%s", role, err, out)
		}
		if got := h.runner.commands(); slices.Contains(got, "/usr/sbin/userdel "+ServiceUser) {
			t.Errorf("%s: commands %v, want the pre-existing user %s kept", role, got, ServiceUser)
		}
		if _, _, found, _ := h.users.Lookup(ServiceUser); !found {
			t.Errorf("%s: the pre-existing user %s was removed", role, ServiceUser)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestInstallKeepsAUserItDidNotCreate|TestInstallRerunKeepsTheUserItCreated|TestUninstallKeepsAUserItDidNotCreate' ./internal/install/`
Expected: `TestInstallKeepsAUserItDidNotCreate` FAILS (`manifest records the user darkpanel it did not create`); `TestUninstallKeepsAUserItDidNotCreate` FAILS for both roles (`/usr/sbin/userdel darkpanel` in the commands); `TestInstallRerunKeepsTheUserItCreated` PASSES (it pins today's behaviour for the created case).

- [ ] **Step 3: Implement**

1. Give `installer` a `createdUser bool`; `ensureUser` sets it to true only on the path that ran `useradd` successfully.
2. In `(*installer).database`, record every `OwnedPaths(in.cfg.Layout, in.cfg.Role)` entry as today except an `core.OwnedUser` entry while `createdUser` is false. A manifest row a previous run recorded stays: `database` never forgets entries. The step's output line counts the entries it recorded.
3. In `(*Uninstall).load`, the loop that adds `OwnedPaths(u.cfg.Layout, RoleController)` entries missing from the manifest skips `core.OwnedUser` entries, so only a user the manifest records is removed. `OwnedPaths` and `CoreOwnedPaths` keep declaring the user (the registry's duplicate-ownership check reads them).
4. Update the doc comments of `ensureUser`, `database` and `load` to say this in one sentence each.
5. In `TestInstallRerunAfterEachCheckpoint` (`internal/install/install_test.go`), the `chown fails` case's first run creates the user and fails before the database step, so its rerun no longer records the user. Add a field `userUnrecorded bool` to the table struct, set it to `true` on the `chown fails` row only, and replace the manifest check with:

```go
			want := wantManifest(h.layout, RoleController)
			if tt.userUnrecorded {
				want = slices.DeleteFunc(want, func(s string) bool { return s == core.OwnedUser+" "+ServiceUser })
			}
			if got := manifest(t, h.layout); !reflect.DeepEqual(got, want) {
				t.Errorf("manifest\n got %v\nwant %v", got, want)
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/install/ ./cmd/darkpanel/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/install/install.go internal/install/uninstall.go internal/install/install_test.go internal/install/uninstall_test.go
git commit -m "fix(install): remove only a user install created"
```

### Task 4: Plan an uninstall past a damaged agent key

**Files:**
- Modify: `internal/install/uninstall.go` (`Uninstall` struct, `load`, `runModules`, the data step's plan line in `plan`)
- Test: `internal/install/uninstall_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 84
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Append to `internal/install/uninstall_test.go`:

```go
func TestUninstallWithADamagedAgentKey(t *testing.T) {
	h, m := installHost(t, RoleController)
	key := h.layout.Paths.AgentKey
	if err := os.WriteFile(key, []byte("damaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "uninstall: modules: the agent key "+key+" cannot be read") ||
		!strings.Contains(err.Error(), "restore it, or delete it to skip the module steps") {
		t.Fatalf("Run = %v, want the modules step to name the damaged key %s\n%s", err, key, out)
	}
	if len(m.calls) != 0 || !exists(t, key) {
		t.Error("a module step ran, or the damaged key was removed")
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	out, err = runUninstall(t, h.uninstallConfig(m, false))
	if err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "modules: no agent key at "+key+"; skipped the module steps, which need it") {
		t.Errorf("output does not say the module steps were skipped:\n%s", out)
	}
	if exists(t, h.layout.DataDir) || exists(t, h.layout.ConfigDir) {
		t.Error("the data or configuration directory is left")
	}
}

func TestUninstallStopsAtAFailingUserdel(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.runner.exit = 1
	_, err := runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "uninstall: data: userdel darkpanel exited 1: refused") {
		t.Fatalf("Run = %v, want the data step to stop at the failing userdel", err)
	}
	if !exists(t, h.layout.ConfigDir) || !exists(t, h.layout.Paths.AgentKey) {
		t.Error("the data step removed the configuration directory or the agent key after a failed userdel")
	}
}
```

- [ ] **Step 1b: Extend the tests `TestUninstallAgentRole` and `TestDescribeUninstall` in `internal/install/uninstall_test.go`**

In `TestUninstallAgentRole`, replace `if got := h.systemd.recorded(); got[0] != "Stop darkpanel-agent.service" {` with:

```go
	if got := h.systemd.recorded(); len(got) == 0 || got[0] != "Stop darkpanel-agent.service" {
		t.Fatalf("systemd calls %v, want the agent stopped first; the controller unit does not exist", got)
	}
```

and delete the old two-line body and closing brace of that `if`.

Add a second Describe check after the existing loop of `TestDescribeUninstall` (the keep-data plan):

```go
	u2, err := PlanUninstall(context.Background(), h.uninstallConfig(m, false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u2.Close() }()
	var full bytes.Buffer
	u2.Describe(&full)
	if want := "remove the users, then " + h.layout.DataDir + " except agent.db, then " + h.layout.ConfigDir +
		" with the agent key, and agent.db last"; !strings.Contains(full.String(), want) {
		t.Errorf("plan lacks %q:\n%s", want, full.String())
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestUninstallWithADamagedAgentKey|TestUninstallStopsAtAFailingUserdel|TestUninstallAgentRole|TestDescribeUninstall' ./internal/install/`
Expected: `TestUninstallWithADamagedAgentKey` FAILS with `PlanUninstall: install: plan uninstall: secret: key <path>: want one line starting with <KeyPrefix>`; `TestDescribeUninstall` FAILS (`plan lacks "remove the users, then ..."`); `TestUninstallStopsAtAFailingUserdel` and `TestUninstallAgentRole` PASS (they pin behaviour already present).

- [ ] **Step 3: Implement**

1. Give `Uninstall` a `keyErr error`. In `load`, when the agent key exists but `secret.LoadOrCreateKey` or `secret.NewSealer` fails, store that error in `keyErr` and go on; the plan no longer fails on it.
2. In `runModules`, after the `u.db == nil` case: when `keyErr` is set, return `fmt.Errorf("the agent key %s cannot be read (%v); restore it, or delete it to skip the module steps", p.AgentKey, keyErr)`; when `sealer` is nil (no key file), return the line `no agent key at <AgentKey>; skipped the module steps, which need it`.
3. Change the data step's first plan line (the non-keep-data branch) to `remove the users, then <DataDir> except agent.db, then <ConfigDir> with the agent key, and agent.db last`, the order `removeData` already follows.
4. `TestUninstallResumeWithoutAgentKeySkipsModules` checks only the prefix `modules: no agent key`, so it passes unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/install/ ./cmd/darkpanel/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/install/uninstall.go internal/install/uninstall_test.go
git commit -m "fix(install): plan uninstall past a damaged key"
```

### Task 5: Migrate agent.db only under the agent lock

**Files:**
- Create: `internal/install/agentlock.go`
- Modify: `internal/install/install.go` (`database`)
- Modify: `internal/install/uninstall.go` (`takeAgentLock`, `waitStopped`)
- Test: `internal/install/install_test.go`

**Interfaces:**
- Consumes: none
- Produces: K3

**Items:** 72
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8

- [ ] **Step 1: Write the failing test**

Append to `internal/install/install_test.go` (add `golang.org/x/sys/unix` to its imports):

```go
// holdAgentLock takes the agent lock on h as a running agent would and
// returns its release.
func holdAgentLock(t *testing.T, h *testHost) (string, func()) {
	t.Helper()
	path := filepath.Join(h.layout.Paths.AgentDir, AgentLockName)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("lock %s: %v", path, err)
	}
	return path, func() { _ = unix.Close(fd) }
}

func TestInstallRerunStopsTheAgentForTheMigration(t *testing.T) {
	h := newTestHost(t)
	ctx := context.Background()
	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	h.systemd.calls = nil
	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("rerun: %v\n%s", err, h.out)
	}
	want := []string{
		"Stop darkpanel-agent.service", "Reload",
		"Enable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service",
		"Restart darkpanel-controller.service", "Restart darkpanel-agent.service",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
	_, release := holdAgentLock(t, h)
	release()
}

func TestInstallRefusesAnAgentItCannotStop(t *testing.T) {
	h := newTestHost(t)
	ctx := context.Background()
	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	path, release := holdAgentLock(t, h)
	defer release()
	db := h.layout.Paths.AgentDB()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(db + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	h.systemd.calls = nil
	err := Install(ctx, h.config(RoleController))
	if exists(t, db) {
		t.Errorf("the refused install opened %s before it held the agent lock", db)
	}
	if want := "install: database: an agent still runs and holds " + path + "; stop it first"; err == nil || err.Error() != want {
		t.Fatalf("Install = %v, want %q", err, want)
	}
	if got := h.systemd.recorded(); slices.Contains(got, "Reload") || slices.Contains(got, "Restart darkpanel-agent.service") {
		t.Errorf("systemd calls %v: the refused install went on past the database step", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestInstallRerunStopsTheAgentForTheMigration|TestInstallRefusesAnAgentItCannotStop|TestUninstallRefusesARunningAgent' ./internal/install/`
Expected: `TestInstallRerunStopsTheAgentForTheMigration` FAILS (the calls lack `Stop darkpanel-agent.service`); `TestInstallRefusesAnAgentItCannotStop` FAILS with `Install = <nil>`; `TestUninstallRefusesARunningAgent` PASSES.

- [ ] **Step 3: Implement**

1. Create `internal/install/agentlock.go` with `lockAgentDir` and `waitStopped` exactly as K3 states, moving the bodies of `(*Uninstall).takeAgentLock`'s open-and-flock code and of `(*Uninstall).waitStopped` there. `takeAgentLock` keeps its `no agent directory` case and stores the descriptor in `u.lockFD`; `(*Uninstall).waitStopped` becomes a call to the function with `u.cfg.Systemd`, `u.cfg.PollInterval` and `u.cfg.StopTimeout`. The uninstall messages do not change.
2. In `(*installer).database`, before `store.Open`: read the state of `update.AgentUnit`; unless it is `not-found`, `inactive` or `failed`, stop it and wait with `waitStopped(ctx, in.cfg.Systemd, update.AgentUnit, in.cfg.PollInterval, StopTimeout)`. Then take `lockAgentDir(p.AgentDir)` and keep it until the step returns, closing the descriptor on every path. The migration and the manifest records run under the lock. The agent step's existing restart starts the agent again, so the database step does not.
3. Update `database`'s doc comment: it stops a running agent and migrates under the agent lock, so no agent runs against a half-migrated database.
4. Every existing install test still passes; a test whose exact systemd call list changes because the rerun now stops the agent is updated to the new list.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/install/ ./internal/agent/daemon/ ./cmd/darkpanel/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/install/agentlock.go internal/install/install.go internal/install/uninstall.go internal/install/install_test.go
git commit -m "fix(install): migrate agent.db under the agent lock"
```

### Task 6: Run the credential writer tests under the race detector

**Files:**
- Modify: `Makefile` (the `.PHONY` line, a new `test-race` target, the `test` target)
- Modify: `.github/workflows/ci.yml` (the Go job's `make` line)
- Modify: `docs/dforge/gates.md` (gate 3's Why line)

**Interfaces:**
- Consumes: none
- Produces: the make target `test-race`

**Items:** 89
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing test**

The check is the make target itself; no test file pins configuration, so there is no test to write.

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 60 make -n test-race`
Expected: FAIL with `No rule to make target 'test-race'`.

- [ ] **Step 3: Implement**

1. Add `test-race` to the `.PHONY` line, and this target after `test-go`:

```make
test-race:
	go test -race -timeout 10m -count=1 ./internal/agent/daemon/
```

2. Change `test: test-go test-web test-adr test-release` to `test: test-go test-race test-web test-adr test-release`.
3. In `.github/workflows/ci.yml`, change the Go job's `- run: make lint-go test-go vuln` to `- run: make lint-go test-go test-race vuln`.
4. In `docs/dforge/gates.md`, append to gate 3's Why line: `; test-race runs the agent daemon's credential-writer tests under the race detector`.

- [ ] **Step 4: Run test to verify it passes**

Run: `mkdir -p /root/.cache/m2g-tmp/6 && TMPDIR=/root/.cache/m2g-tmp/6 GOTOOLCHAIN=local timeout 700 make test-race`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/agent/daemon`.

- [ ] **Step 5: Commit**

```bash
git add Makefile .github/workflows/ci.yml docs/dforge/gates.md
git commit -m "test(agent): run credential tests under -race"
```

### Task 7: Copy the core module's owned paths into its manifest

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`module.Manifest`)
- Test: `internal/controller/coreactions/module_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 31
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/coreactions/module_test.go`:

```go
func TestManifestCopiesTheOwnedPaths(t *testing.T) {
	d := Deps{OwnedPaths: []core.OwnedPath{{Path: "/etc/darkpanel", Kind: core.OwnedDir}}}
	m := Module(d).Manifest()
	m.OwnedPaths[0].Path = "/changed"
	if got := d.OwnedPaths[0].Path; got != "/etc/darkpanel" {
		t.Errorf("changing the manifest's owned paths changed Deps.OwnedPaths to %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run TestManifestCopiesTheOwnedPaths ./internal/controller/coreactions/`
Expected: FAIL with `changing the manifest's owned paths changed Deps.OwnedPaths to "/changed"`.

- [ ] **Step 3: Implement**

1. In `module.Manifest`, set `OwnedPaths` to `slices.Clone(m.d.OwnedPaths)`. A nil slice stays nil, so `TestManifest` is unaffected.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 600 go test -count=1 ./internal/controller/coreactions/ ./internal/controller/server/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go internal/controller/coreactions/module_test.go
git commit -m "fix(coreactions): copy owned paths into the manifest"
```

### Task 8: Reconcile ADR 0012 with the code

**Files:**
- Modify: `docs/adr/0012-m2-contract-and-layout-additions.md`

**Interfaces:**
- Consumes: K1
- Produces: none

**Items:** 33
**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

No test: this task changes ADR text only, and gate 1 (`check-adrs`) checks only the ADR's structure. The replacement text below is exact, so the review compares it with the code it cites.

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 120 bash scripts/check-adrs.sh --complete`
Expected: passes before the change (structure only); record the output.

- [ ] **Step 3: Implement**

Make exactly these edits; change nothing else, and leave `## Status` as `Proposed`. The quoted passages wrap across lines in the ADR: match them across the line breaks and rewrap the edited paragraph to the file's existing width.

1. **Install script verification** (the first Decision bullet): replace `it downloads nothing but the release files.` with `it downloads nothing but the release index `index/releases.json`, when no version is given, and the chosen version's tarball, `checksums.txt` and signature files.` (`install.sh` lines 283-300). Leave the rest of the bullet unchanged.
2. **Signature files and release index**: no change. The sentence that a generator may write the members in any order is what the code does: `newest_stable` in `install.sh` finds a release by its members whatever their order, and `scripts/gen-install-sh/main_test.go` pins it with the `sorted keys` and `extra members` rows. Contract C16 of the foundation plan, which fixed the order, is superseded by that code; plans are not edited.
3. **Schema hashes**: replace `A schema hash is the hex SHA-256 of` with `A schema hash is the lower-case hex SHA-256 of`.
4. **Operation reservation**: after the sentence that ends `while `*` is held, every other lock is refused.`, insert: `A second `Reserve` by the owner that already holds `*` succeeds and changes nothing. `Reserve` is refused with `core.uninstalling` while the uninstall barrier is set, and `BeginUninstall` is refused with `update.in_progress` while any owner holds `*`, so an update and an uninstall never overlap.`

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 120 bash scripts/check-adrs.test.sh && timeout 120 bash scripts/check-adrs.sh --complete`
Expected: `check-adrs tests:` reports `0 failed`, and no `check-adrs:` error line follows.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0012-m2-contract-and-layout-additions.md
git commit -m "docs(adr): reconcile ADR 0012 with the code"
```
