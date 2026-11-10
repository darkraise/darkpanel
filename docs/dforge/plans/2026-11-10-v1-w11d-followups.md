# v1 wave 11d: follow-up fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the wave 11c follow-up rows that need code, tests or documentation, and record a reason on the rest.

**Architecture:** Each task is one small slice with its own regression test. Rulings W11d-R1 to W11d-R5 under Assumptions settle the rows that need no change; those rows were closed in the registers when this plan was written.

**Tech Stack:** Go 1.27, React with Vitest, Markdown documentation tests.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model sonnet --effort medium` — the hardest self-implemented task totals 4 with no risk-3 task

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w11d-tmp/integ && export TMPDIR=/root/.cache/w11d-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Plan review:** 2026-11-10 — dforge:judge-opus — executability 16 / coherence 19 / coverage 19 / assumptions 15 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`, plus rulings W11d-R1 to W11d-R5 under Assumptions. No task edits the spec.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `ip6tables`, `iptables-restore`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w11d-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends.
- No task runs `go clean -cache` or `go clean -testcache`: the build cache is shared with other sessions on this host.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `timeout 1300 go test -timeout 20m -count=1` on the task's packages (every line `ok`; the backup package has taken 357 s alone on this host, and the npm package has exceeded 600 s under the parallel full suite); `golangci-lint run --allow-serial-runners` on the task's packages (`0 issues.`).
- Generated files: no task changes a wire type, so `schemas/**` and `docs/api/**` stay unchanged.
- TDD: write the test, run it and see the expected RED, then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A test that pins behaviour already present passes before the change; the step says so.
- Frozen files (spec 1.12): no task edits `internal/core/**`, `internal/platform/**`, `go.mod`, `go.sum`, `test/harness/pins.go` or `schemas/**`. Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- The plan runs on branch `v1-w11d-followups` in the base repository, with no worktree.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name; a changed refusal keeps its error code.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) does not apply to this branch: its diff touches none of the paths that gate lists. No task creates or pushes a tag or runs `release.yml`.

## Contracts

None

## Assumptions (evidence)

- Every Step 1 test below and every Step 3 change was applied to `main` at 9feafac and run on 2026-11-10, then reverted. Tasks 1, 3, 4 and 5 showed the RED quoted in their Step 2 and went GREEN after the change; Task 2's tests passed before any change.
- W11d-R1 NPM undo and deploy (security review 85, 86, 88). 85 and 86 fall under W11-R13: an undo only puts back what NPM already held, a reserved upstream is still refused, and a name the resolver cannot settle stands where a name with no answer stands. Plain errors count as no answer in `internal/modules/npm/proxyhost_resolve_test.go`, and the `forward host` entry in Known residual risks already covers re-pointed names. 88 falls under W11-R14: the removal touches only the container, volumes and network this deploy created, and the deploy has failed either way.
- W11d-R2 no defect (security review 91, 92, 95, 99, 108, 110, 130). 91: `maskFrom` has two callers, with `first` 0 and 1, and an empty slice returns early. 92: the project's style asks doc comments on exported identifiers only. 95: `fec0::/10` is refused (`internal/modules/derp/node_probe.go`) and `fec0::1` is in the test table. 99: the journaled effect covers only the package, the package is untouched at that step, and a failed removal is in the returned error; Task 2 pins this. 108: with the before-state ignored, `flowRules` counts 2 rules and the test's first assertion fails (`internal/modules/firewall/guard/cells.go`). 110: `ip -6 route get 2606:4700::1 ipproto 47` on this host (kernel 7.0.14, 2026-11-10) answers `Error: Unsupported ip proto.`, while `ipproto 6` passes that parser. 130: that test is the only one of the `limitSource` to `sourceKey` composition.
- W11d-R3 records of an earlier plan (security review 84, 90, 97, 106, 111, 112, 113, 116, 118). These describe how wave 11c was carried out or a one-time spec edit. Wave 11c's gates passed afterwards, so there is nothing to change, as W11-R18 ruled for the same kind of row.
- W11d-R4 unreproduced flakes (v1 completion 467, 468). `TestBuTaskEndsAHungPoll` passed 25 isolated runs and 96 runs across 16 processes at `GOMAXPROCS=1` on 2026-11-10. The `release/sign.sh` build failure has occurred once. Both are deferred with no code because no cause is known; a recurrence stops gate 3, and debugging starts from it.
- W11d-R5 DERP service directory link (security review 81). Install and renewal keep refusing a host link at or above the service directory for v1, as the threat model records. The owner did not answer the offer within its time on 2026-11-10; the recommended default applies, and the row is deferred.
- A failed repository step happens in pin phase `recorded`, where the rollback is one database write and no host step (`rollback` in `internal/modules/tailscale/package_pin.go`), so a failing rollback there needs a failing database write, which no host fault in the test fixtures reaches. Task 2 leaves that one `errors.Join` branch uncovered on purpose and covers the failed-removal branch.

## Task index

1. NPM wrapper text and deploy detail
2. Probe, pin and composite coverage
3. Messages and names
4. NPM expose alerts
5. Threat model probe refusals

---

### Task 1: NPM wrapper text and deploy detail

**Files:**
- Modify: `internal/modules/npm/effects.go`
- Test: `internal/modules/npm/instance_deploy_status_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#87, 2026-11-06-v1-security-review#89

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Extend the test `internal/modules/npm/instance_deploy_status_test.go::TestW11c02WithoutDefiniteMarkKeepsWrapperText`**

In `TestW11c02WithoutDefiniteMarkKeepsWrapperText`, add at the end of the function:

```go
	outer := withoutDefiniteMark(fmt.Errorf("outer: %w", errors.Join(core.Definite(cause), errors.New("and more"))))
	if core.IsDefinite(outer) || !strings.Contains(outer.Error(), "outer: ") || !strings.Contains(outer.Error(), "and more") {
		t.Errorf("a joined mark beneath a wrapper gave %v (definite %v)", outer, core.IsDefinite(outer))
	}
```

Replace the whole of `TestW11c02ExistingAdministratorIsStatedOnce` with:

```go
func TestW11c02ExistingAdministratorIsStatedOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fail    bool
		outcome string
	}{
		{"the removal succeeds", false, "the deploy removed what it created"},
		{"the removal fails", true, "the deploy could not remove what it created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
			if tc.fail {
				h.docker.Fail("RemoveVolume", thAPIError(409, "volume is in use"))
			}
			_, err := dpApply(h, h.nm(), "c1")
			if errcode.From(err).Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if n := strings.Count(err.Error(), "already has an administrator"); n != 1 {
				t.Errorf("the error states its detail %d times, want once: %v", n, err)
			}
			if !strings.Contains(err.Error(), tc.outcome) {
				t.Errorf("error %v, want it to say %q", err, tc.outcome)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w11d-tmp/1 && TMPDIR=/root/.cache/w11d-tmp/1 GOTOOLCHAIN=local timeout 280 go test -timeout 260s -count=1 -run TestW11c02 ./internal/modules/npm/`
Expected: FAIL with `a joined mark beneath a wrapper gave cause` followed by `and more (definite false)`. `TestW11c02ExistingAdministratorIsStatedOnce` passes before the change, because it pins the removal-failed branch's behaviour as it stands.

- [ ] **Step 3: Implement**

1. In `withoutDefiniteMark` in `internal/modules/npm/effects.go`, the branch taken when `errors.Unwrap` returns nil (the error is a join that holds the mark) returns `errors.New(text)`, where `text` is the text captured at the top of the function, in place of `notDefinite(err)`.
2. Keep `notDefinite`: `instance_upgrade.go` still calls it. Change nothing else in the function.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w11d-tmp/1 GOTOOLCHAIN=local timeout 1300 go test -timeout 20m -count=1 ./internal/modules/npm/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/effects.go internal/modules/npm/instance_deploy_status_test.go
git commit -m "fix(npm): keep wrapper text on a joined mark"
```

### Task 2: Probe, pin and composite coverage

**Files:**
- Test: `internal/modules/derp/node_probe_forms_test.go`, `internal/modules/derp/composite_remove_test.go`, `internal/modules/tailscale/package_pin_repo_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#96, 2026-11-06-v1-security-review#100, 2026-11-06-v1-security-review#103, 2026-11-06-v1-security-review#129

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Extend the test `internal/modules/derp/node_probe_forms_test.go::TestW11c04ProbeRefusesInternalAddressForms`**

This step edits all three test files in the **Files:** block and adds two new tests; apply every block below.

In `TestW11c04ProbeRefusesInternalAddressForms`, add these lines after the line `"::ffff:0:7f00:1", "fec0::1",`:

```go
		"::ffff:0.1.2.3", "::ffff:192.0.0.1", "::ffff:198.18.0.1", "::ffff:240.0.0.1",
		"0.255.255.255", "192.0.0.255", "198.19.255.255",
		"::ffff:ffff", "64:ff9b::ffff:ffff", "64:ff9b:1:ffff:ffff:ffff:ffff:ffff",
		"2001:0:ffff:ffff:ffff:ffff:ffff:ffff", "2002:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		"::ffff:0:ffff:ffff", "feff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
```

Replace the line `for _, s := range []string{"93.184.216.34", "203.0.113.9", "2606:4700::1"} {` with:

```go
	for _, s := range []string{
		"93.184.216.34", "203.0.113.9", "2606:4700::1",
		"1.0.0.1", "192.0.1.1", "198.20.0.1", "223.255.255.255", "::ffff:93.184.216.34",
		"64:ff9b:2::1", "2001:1::1", "2003::1",
	} {
```

In `TestW11c08OutputRefusesUndecodableEdits` in `internal/modules/derp/composite_remove_test.go`, replace the final `if` statement with:

```go
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(`{"hostId":"h7"}`), steps); err == nil || !strings.Contains(err.Error(), "the edits step's output") {
		t.Fatalf("Output on an undecodable edits output: %v, want the error to name the edits step", err)
	}
```

In `internal/modules/tailscale/package_pin_repo_test.go`, add `"strings"` to the standard imports and `"github.com/darkraise/darkpanel/internal/core/errcode"` after the `core` import. Add a field to `w11c06Files` after `removed *[]string`:

```go
	// removeErr fails every Remove, after it is recorded.
	removeErr error
```

Replace the body of its `Remove` method with:

```go
	*f.removed = append(*f.removed, path)
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.FileWriter.Remove(ctx, module, path)
```

Append to that file:

```go
func TestW11d02FailedRepoStepReportsAFileItCouldNotRemove(t *testing.T) {
	s := pnNewSetup(t, true)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, failOn: sourcesPath, removed: &removed, removeErr: errors.New("read-only file system")}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if err == nil || !strings.Contains(err.Error(), "read-only file system") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error %v, want it to carry the removal's failure and the write's", err)
	}
	if want := []string{keyringPath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
		t.Errorf("effect state %s, want skipped: the package was not touched", e.State)
	}
}

func TestW11d02LaterFailedStepKeepsTheRepoFiles(t *testing.T) {
	s := pnNewSetup(t, false)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	s.apt.missing[pnPrior] = true
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, removed: &removed}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if coded := pnCoded(t, err, errcode.TailscalePinFailed); !strings.Contains(coded.Detail, "stage-prior") {
		t.Errorf("detail %q, want it to name stage-prior", coded.Detail)
	}
	if len(removed) != 0 {
		t.Errorf("removed %q, want the repository files left for uninstall", removed)
	}
}
```

- [ ] **Step 2: Run the tests against the unchanged production code**

Run: `mkdir -p /root/.cache/w11d-tmp/2 && TMPDIR=/root/.cache/w11d-tmp/2 GOTOOLCHAIN=local timeout 280 go test -timeout 260s -count=1 -run 'TestW11c04|TestW11c08Output|TestW11c06|TestW11d02' ./internal/modules/derp/ ./internal/modules/tailscale/`
Expected: every line `ok`. These tests pin behaviour already present, so no RED exists and no production file changes.

- [ ] **Step 3: Implement**

No production change: this task only adds coverage. If a row fails, the production code disagrees with a ruling, so stop and report it as Global Constraints says.

- [ ] **Step 4: Run the packages**

Run: `TMPDIR=/root/.cache/w11d-tmp/2 GOTOOLCHAIN=local timeout 1300 go test -timeout 20m -count=1 ./internal/modules/derp/ ./internal/modules/tailscale/`
Expected: every line `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/node_probe_forms_test.go internal/modules/derp/composite_remove_test.go internal/modules/tailscale/package_pin_repo_test.go
git commit -m "test(modules): pin probe edges and repo rollback"
```

### Task 3: Messages and names

**Files:**
- Modify: `internal/modules/firewall/predict/translate.go`, `internal/controller/backup/stream.go`
- Test: `internal/modules/firewall/predict/translate_test.go`, `internal/controller/backup/stream_descriptor_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#101, 2026-11-06-v1-security-review#102, 2026-11-06-v1-security-review#109

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 2 - spec 1 - coupling 0 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/firewall/predict/translate_test.go`:

```go
func TestW11d03AMissingFileIsAPredictionFailure(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "fresh")
	cfg.Files = maps.Clone(cfg.Files)
	delete(cfg.Files, "/etc/ufw/before.rules")
	_, err := translate(cfg, false)
	if err == nil || !strings.Contains(err.Error(), "so the rules cannot be predicted") {
		t.Errorf("error %v, want it to say the rules cannot be predicted", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w11d-tmp/3 && TMPDIR=/root/.cache/w11d-tmp/3 GOTOOLCHAIN=local timeout 200 go test -timeout 180s -count=1 -run TestW11d03 ./internal/modules/firewall/predict/`
Expected: FAIL with `error predict: /etc/ufw/before.rules is missing or empty, so UFW cannot start, want it to say the rules cannot be predicted`

- [ ] **Step 3: Implement**

1. In the `file` closure of `restoreText` in `internal/modules/firewall/predict/translate.go`, the missing-file message ends `, so the rules cannot be predicted` in place of `, so UFW cannot start`.
2. In `internal/controller/backup/stream.go`, the refusal for a copy whose link count is not one says `backup: the temporary copy %s does not have exactly one name`, in place of `has more than one name`. No test reads this text: `writeProblem` sends it only to the log.
3. In `internal/controller/backup/stream_descriptor_test.go`, rename `TestV1r22BackupRefusesACopySwappedForAFileThatIsNotRegular` to `TestV1r22BackupRefusesACopySwappedForAnUnsafeFile`, and replace its doc comment with:

```go
// TestV1r22BackupRefusesACopySwappedForAnUnsafeFile replaces the
// temporary copy with a FIFO, a directory or a hard link to another file,
// none of which the open refuses. The backup must refuse to stream it and
// remove it.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w11d-tmp/3 GOTOOLCHAIN=local timeout 1300 go test -timeout 20m -count=1 ./internal/modules/firewall/predict/ ./internal/controller/backup/`
Expected: every line `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/predict/translate.go internal/modules/firewall/predict/translate_test.go
git commit -m "fix(firewall): say a missing file stops prediction"
git add internal/controller/backup/stream.go internal/controller/backup/stream_descriptor_test.go
git commit -m "fix(backup): name the hard-link refusal correctly"
```

### Task 4: NPM expose alerts

**Files:**
- Modify: `web/src/modules/npm/container-actions.tsx`
- Test: `web/src/modules/npm/container-actions.test.tsx`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#104, 2026-11-06-v1-security-review#105

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

In `web/src/modules/npm/container-actions.test.tsx`, add before the test named `shows a failed status refetch when the last status said not deployed`:

```tsx
test("shows one alert when the status refetch fails and the container never loaded", async () => {
  let failing = false
  const { client } = caRender({
    [CA_STATUS]: () => (failing ? caDown("the NPM module is unavailable") : { body: { output: caStatus() } }),
    [CA_INSPECT]: () => caDown("the Docker daemon is not reachable"),
  })
  await screen.findByRole("alert")
  failing = true
  await act(async () => {
    await client.invalidateQueries()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  expect(screen.getAllByRole("alert").map((a) => a.textContent)).toEqual(["the NPM module is unavailable"])
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && timeout 200 npx vitest run src/modules/npm/container-actions.test.tsx`
Expected: FAIL with the received list also holding `"the Docker daemon is not reachable"`

- [ ] **Step 3: Implement**

1. In `ExposeLoader`, the branch that handles a query with no data and nothing pending returns the `ReadFailure` only when `showFailure` is true, and `null` otherwise.
2. In `web/src/modules/npm/container-actions.test.tsx`, change the comment above these tests that says `In both tests below` to say `In the tests below`.
3. In `ExposeAction`, after the `status.data === undefined` branch, return one fragment: the `{status.isError && <ReadFailure error={status.error} />}` line once, then `ExposeLoader` with its existing props when `status.data.deployed` is true, and the existing `Nginx Proxy Manager is not deployed on this host. Deploy it first.` paragraph otherwise. The separate `!status.data.deployed` branch goes away.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && timeout 200 npx vitest run src/modules/npm/ && timeout 200 npx tsc --noEmit && cd .. && GOTOOLCHAIN=local timeout 600 make lint-web`
Expected: every Vitest file passes, and `tsc` and ESLint print no findings.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/npm/container-actions.tsx web/src/modules/npm/container-actions.test.tsx
git commit -m "fix(web): show one alert on the expose form"
```

### Task 5: Threat model probe refusals

**Files:**
- Modify: `docs/security/threat-model.md`
- Test: `internal/docs/threat_model_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#119, 2026-11-06-v1-security-review#120, 2026-11-06-v1-security-review#128

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

- [ ] **Step 1: Extend the test `internal/docs/threat_model_test.go::TestW11c13ThreatModelRecordsTheRulings`**

In `TestW11c13ThreatModelRecordsTheRulings`, replace this block:

```go
	if strings.Contains(text, "are not refused. Only a caller") {
		t.Error("the threat model still says the special-purpose forms are not refused")
	}
```

with:

```go
	d := w10tmParse(text)
	const refusal = "The probe refuses the special-purpose forms"
	if !strings.Contains(strings.Join(d.parts["DERP host/Mitigations"], "\n"), refusal) {
		t.Errorf("the DERP mitigations do not say %q", refusal)
	}
	if strings.Contains(strings.Join(d.parts["DERP host/Residual risk"], "\n"), refusal) {
		t.Errorf("the DERP residual risk still states the mitigation %q", refusal)
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w11d-tmp/5 && TMPDIR=/root/.cache/w11d-tmp/5 GOTOOLCHAIN=local timeout 200 go test -timeout 180s -count=1 -run TestW11c13ThreatModelRecordsTheRulings ./internal/docs/`
Expected: FAIL with `the DERP mitigations do not say "The probe refuses the special-purpose forms"` and `the DERP residual risk still states the mitigation`

- [ ] **Step 3: Implement**

In the `### DERP host` area of `docs/security/threat-model.md`:

1. Add as the last bullet under `#### Mitigations`:

```markdown
- The probe refuses the special-purpose forms that reach internal networks, among them `64:ff9b::/96`, `0.0.0.0/8`, `240.0.0.0/4` and `198.18.0.0/15` (`internal/modules/derp/node_probe.go`).
```

2. Under `#### Residual risk`, replace the bullet that starts `The probe refuses the special-purpose forms` with:

```markdown
- Documentation ranges stay allowed as probe targets and there is no allowlist. Only a caller with the `operate` scope can choose the target.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w11d-tmp/5 GOTOOLCHAIN=local timeout 200 go test -timeout 180s -count=1 ./internal/docs/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add docs/security/threat-model.md internal/docs/threat_model_test.go
git commit -m "docs(security): list probe refusals as mitigations"
```
