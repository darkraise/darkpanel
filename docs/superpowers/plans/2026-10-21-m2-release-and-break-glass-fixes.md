# M2 Release and Break-glass Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the last open M2 review rows: `install.sh` joins the signed `checksums.txt`, an update runner stops waiting once another process rolled its update back, `darkpanel agent check` keeps its exit statuses for outcomes only, and the `--local` and `--direct` rollback paths stop ignoring Ctrl+C, hang-up and stray flags.

**Architecture:** Five small, independent changes. `release/package.sh` copies `install.sh` into the release directory before it writes `checksums.txt`, so the existing minisign signature covers it. `update.Probes.Wait` takes a `moved` callback the runner answers by re-reading its record, so a runner whose record a recover pass rolled back returns within one health interval instead of polling to the deadline. `agent check` maps every failure that is not a handshake outcome to a new exit status 3. The `--local` rollback fallback runs `darkpanel.prev` in its own process group and holds SIGINT and SIGHUP until it finishes; `--direct --transaction` refuses `--force` and `--json`. A table row pins that the agent-socket dial cannot hang.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `github.com/spf13/cobra`, `os/signal`, `syscall`, bash, GitHub Actions YAML.

**Spec:** docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves; each task's **Implementer:** line names the subagent that implements it

**Plan review:** 2026-10-21 — dr-superpowers:judge-opus — executability 15 / coherence 18 / coverage 18 / assumptions 17 (round 2)

**Parallelism:** waves — 1-3 | 4-5

**Integration check:** `mkdir -p /root/.cache/plan7-tmp/integration && export GOTOOLCHAIN=local TMPDIR=/root/.cache/plan7-tmp/integration && timeout 600 go build ./... && timeout 1500 go test -count=1 ./... && timeout 120 bash release/package.test.sh && timeout 600 go vet ./... && timeout 900 golangci-lint run --allow-serial-runners`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is a small tmpfs that parallel Go builds fill: every `go` command, `golangci-lint` and `make` run with `TMPDIR=/root/.cache/plan7-tmp/<task number>` (create it with `mkdir -p`, empty it when the task ends), also where a step's command omits it.
- Dependencies: no task edits `go.mod` or `go.sum`. After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing.
- Files: a task touches only the files its **Files:** block names.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings: `cmd/darkpanel` errors carry no prefix because `cli.PrintError` prefixes `darkpanel:`; `internal/update` errors start `update:`.
- Unit tests touch no real systemd, D-Bus, `/etc`, `/var/lib`, `/run` or `/usr/local`: systemd is the existing fake, paths come from `install.Rooted(t.TempDir())` or `t.TempDir()`, and Unix sockets live under `socketDir(t)` (internal/cli) so their paths stay short.
- Tests run as root on this host (`id -u` = 0). Root bypasses file permissions, so no test relies on `chmod` to produce a permission error; the root checks go through the `geteuid` seam `cmd/darkpanel` already has.
- The test Task 5 adds signals its own test process (`kill -INT $PPID`). It must not call `t.Parallel()`, so no other test of `cmd/darkpanel` runs while the signals arrive; `cmd/darkpanel`'s tests swap package-level seams and none of them is parallel.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task of the plan declares in that package; each task's **Interfaces:** block lists the names it adds.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean.
- The waves merge into the plan's work branch, `worktree-m2-release-and-break-glass-fixes`; each task of a wave runs in its own worktree from the wave's base on that branch.
- Gates run through dr-superpowers:running-gates after the last wave merges and before the final whole-branch review. Gate 7 (Harness green) applies, because the branch touches `internal/update/` and `internal/install/`: it needs the branch pushed and `harness.yml` dispatched, and both need the owner's go-ahead. Never push `main`, and never push or dispatch a workflow without asking the owner first.

## Contracts

- `install.CheckFailed` (internal/install/install.go): a fourth `CheckResult`, value 3, appended after `CheckUnreachable`. It is the exit status of `darkpanel agent check` for every failure that is not a handshake outcome: a usage error, a refused root check, unresolvable paths. Exit statuses 0, 1 and 2 keep their meanings (accepted, refused, unreachable). Task 4 produces it; nothing else consumes it.
- `update.Probes.Wait(ctx context.Context, clock core.Clock, from, to string, deadline time.Time, moved func() bool) error` (internal/update/health.go): `moved` may be nil. When it is not nil, Wait asks it before each round of probes, and once it returns true Wait returns `errMoved` (internal/update/runner.go). Task 2 produces and consumes it; no other package calls `Wait`.
- Release layout: `checksums.txt` lists `install.sh` beside the tarballs and the SBOM, in `LC_ALL=C` order, so `install.sh` comes last. Task 1 produces it.

## Assumptions (evidence)

- A Go dial to a Unix socket whose listen backlog is full fails at once with `connect: resource temporarily unavailable` instead of blocking: a scratch test on 2026-10-21 (listen backlog 0, five dials) returned the first dial in 26 µs and the next four with EAGAIN in under 20 µs each. Go connects non-blocking, and Linux answers a full Unix-socket backlog with EAGAIN. Task 3 pins this as a test instead of adding a timeout.
- `signal.Reset` does not undo `signal.Ignore`: in a scratch test on 2026-10-21, `/proc/<pid>/status` SigIgn stayed `0000000000000003` after `Ignore(SIGINT, SIGHUP)` then `Reset(SIGINT, SIGHUP)`. Task 5 therefore holds the signals with `signal.Notify` and `signal.Stop`, which restores them, and protects the child with `SysProcAttr.Setpgid` instead of inheriting ignored signals.
- A terminal's Ctrl+C and hang-up go to its foreground process group; a child started with `Setpgid: true` leads its own group and receives neither (setpgid(2), credentials(7)). `darkpanel.prev` may be an older release without this plan's code, so protection has to come from the parent, which is the newly installed binary.
- `WriteRecord` replaces a record by rename (internal/update/record.go:115-121), so the runner's unlocked `ReadRecord` in Task 2 reads either the old or the new record, never a partial one.
- The runner does not hold the update lock while it waits for health (internal/update/runner.go:70-78), and `RollBack` takes it (internal/update/rollback.go:67-75), so a recover pass can finish the record during the wait. `TestRunLeavesAMovedRecordAlone` (internal/update/runner_test.go:404) already drives that interleaving.
- `release/package.sh` resolves `$root` as the repository root (release/package.sh:20) and `install.sh` is committed at that root (`git ls-files install.sh`, 2026-10-21), so `release/package.test.sh` can package the real file.
- A plain error from a command's `RunE` or `PreRunE` exits 2 (internal/cli/errors.go:116-139, default branch), which is why `agent check`'s non-outcome failures currently collide with "unreachable". `cli.ExitWith` overrides the code (internal/cli/errors.go:100-105).
- `darkpanel agent check` is not run by `install.sh`, the harness or any script: `system install` calls `daemon.Check` in-process (internal/install/install.go:449), and `rg -n "agent check" install.sh.in test/` finds nothing (2026-10-21). Adding exit status 3 breaks no caller.
- The requests that follow `withAgent`'s dial have no timeout either: `api.NewClient` builds an `http.Client` with no `Timeout` (internal/agent/api/client.go:58), so an agent frozen with room in its backlog accepts the connect and then never answers a `--local` command, which Ctrl+C still ends. That residual is a new open register row, outside this plan, so it is decided on its own rather than folded into a test-only task.
- A child in a background process group that writes to the terminal is stopped by SIGTTOU only when the terminal has `tostop` set, which is off by default (termios(3), `stty -a` shows `-tostop`). Task 5's child writes its progress to the parent's standard error; with `tostop` on, the rollback would stop on that write and the parent would wait with the signals held. This is accepted: `tostop` is a rare manual setting, and Ctrl+Z or a second terminal can still recover.
- Root bypasses file permissions, so with the root check in place a permission error on the credentials cannot reach production `agent check`. The register row suggested the fix belonged in `daemon.Check`; Task 4 makes it in the command instead, and `daemon.Check` is unchanged.

## Task index

1. Sign install.sh through checksums.txt
2. Stop the update runner once its record moved
3. Pin that the agent socket dial cannot hang
4. Give agent check its own failure status
5. Harden the break-glass rollback paths

---

### Task 1: Sign install.sh through checksums.txt

`install.sh` embeds the trusted keys and the README tells users to run it as root, yet `release.yml` copies it into `bin/release` after `checksums.txt` is written, so only the GitHub build attestation covers it. The owner ruled on 2026-10-21 that it joins `checksums.txt`. `package.sh` already lists every file in the output directory; it now copies `install.sh` there first. The README told users the script was outside the signed file; the README, the release README and the versioning policy now describe the new layout.

**Files:**
- Modify: `release/package.sh`
- Test: `release/package.test.sh`
- Modify: `.github/workflows/release.yml` (the `build` job's steps, lines 94-100)
- Modify: `Makefile:101-103` (the `release` target's comment)
- Modify: `README.md:57-63` (Install section only; the Break-glass section belongs to Task 5)
- Modify: `docs/versioning.md:25-38`
- Modify: `release/README.md:43-47`
- Modify: `CONTRIBUTING.md:31`
- Modify: `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md:812-816` (section 9, the `build` job)

**Interfaces:**
- Consumes: none
- Produces: the release layout in Contracts

**Items:** 95

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

In `release/package.test.sh`, replace the `names=` line and add the `install.sh` checks after `checksums_verify`:

```bash
names="darkpanel_1.2.3.cdx.json darkpanel_1.2.3_linux_amd64.tar.gz darkpanel_1.2.3_linux_arm64.tar.gz install.sh "
```

```bash
expect checksums_verify 0 verify_checksums "$work/out1"
expect install_sh_content 0 cmp "$(dirname "$script")/../install.sh" "$work/out1/install.sh"
expect install_sh_mode 0 test "$(stat -c %a "$work/out1/install.sh" 2>/dev/null)" = 644
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 120 bash release/package.test.sh`
Expected: FAIL, with `FAIL checksums_names`, `FAIL install_sh_content`, `FAIL install_sh_mode` and `FAIL rerun_excludes_checksums`, and a non-zero exit.

- [ ] **Step 3: Write the implementation**

In `release/package.sh`, extend the header comment and copy `install.sh` before `checksums.txt` is written. The header becomes:

```bash
#!/usr/bin/env bash
# Packages the release tarballs, copies install.sh and writes checksums.txt
# over every file in the output directory (spec section 9).
#
#   package.sh <version> <bin dir> <out dir>
#
# <bin dir> holds darkpanel-linux-amd64 and darkpanel-linux-arm64 (make
# build). The tarballs are reproducible: entries in a fixed order, owner 0/0,
# fixed modes, the commit time (or SOURCE_DATE_EPOCH) as mtime, and gzip
# without a name or timestamp, so a rebuild of the tag gives the same bytes.
```

and between the `done` that ends the tarball loop and `cd "$outdir"`:

```bash
# install.sh embeds the trusted keys and runs as root, so the signature over
# checksums.txt must cover it like the binaries.
install -m 0644 "$root/install.sh" "$outdir/install.sh"
```

In `.github/workflows/release.yml`, the `build` job checks `install.sh` before `make release` packages it, and no longer copies it. Replace:

```yaml
      - run: make release VERSION="$VERSION"
      - name: Check install.sh is committed and current
        run: |
          git ls-files --error-unmatch install.sh
          make install-sh
          git diff --exit-code -- install.sh
          cp install.sh bin/release/
```

with:

```yaml
      - name: Check install.sh is committed and current
        run: |
          git ls-files --error-unmatch install.sh
          make install-sh
          git diff --exit-code -- install.sh
      - run: make release VERSION="$VERSION"
```

The `release` target's comment in the `Makefile` (line 101) becomes:

```make
# make release VERSION=x.y.z writes the tarballs, the SBOM, install.sh and
# checksums.txt to bin/release. One SBOM serves both architectures: the module
# graph is the same for every GOARCH of this pure-Go build.
```

The documentation describes the new layout and how to verify the script before running it as root.

In `README.md`, replace the paragraph and block from "`install.sh` itself is not in the signed `checksums.txt`" through the `gh attestation verify` block with:

````markdown
`install.sh` is listed in the release's signed `checksums.txt`, like the
tarballs. To check it before running it as root, verify the signature with
[minisign](https://jedisct1.github.io/minisign/), using a release public key
and its key id from `internal/update/trustedkeys/production.go`, then the
checksum:

```sh
base=https://github.com/Darkraise/darkpanel/releases/latest/download
curl -fsSLO "$base/checksums.txt"
curl -fsSLO "$base/checksums.txt.<key id>.minisig"
minisign -V -P <public key> -m checksums.txt -x checksums.txt.<key id>.minisig
sha256sum --check --ignore-missing checksums.txt
```

The trusted comment minisign prints names the version and channel. The
release workflow also attests every file, so `gh attestation verify install.sh
--repo darkraise/darkpanel` is a second check.
````

In `docs/versioning.md`, the list of published files: change the `checksums.txt` bullet to

```markdown
- `checksums.txt`, the SHA-256 of every file above and of the SBOM and
  `install.sh`;
```

and the closing bullet to

```markdown
- `install.sh`, the one-line installer, which embeds the trusted keys.
```

In `release/README.md`, the `build` bullet becomes:

```markdown
- `build` checks the committed `install.sh` is current, runs `make release`
  (tarballs, SBOM, `install.sh` and a `checksums.txt` over all of them),
  writes the release notes with git-cliff (a stable release's notes cover
  every commit since the previous stable release, a beta's since the
  previous tag), and attests every file.
```

In `CONTRIBUTING.md`, the `make release` row becomes:

```markdown
| `make release VERSION=x.y.z` | the release tarballs, SBOM, `install.sh` and `checksums.txt` in `bin/release/` |
```

In the spec, section 9's `build` item lists `install.sh` with the release files. Its current lines 812-816 read:

```markdown
  2. `build`: clean checkout, pinned Go toolchain, `-trimpath`, linux/amd64 and
     linux/arm64 tarballs (binary, `LICENSE`, `README.md`), `checksums.txt`, a
     CycloneDX SBOM (`darkpanel_<version>.cdx.json`), release notes from
     `git-cliff` (`cliff.toml`, conventional commits); GitHub artifact
     attestations for every file.
```

Replace them with:

```markdown
  2. `build`: clean checkout, pinned Go toolchain, `-trimpath`, linux/amd64 and
     linux/arm64 tarballs (binary, `LICENSE`, `README.md`), a CycloneDX SBOM
     (`darkpanel_<version>.cdx.json`), the committed `install.sh`,
     `checksums.txt` over all of them, release notes from `git-cliff`
     (`cliff.toml`, conventional commits); GitHub artifact attestations for
     every file.
```

Keep the surrounding lines, wrapping at the width of the neighbouring text.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 120 bash release/package.test.sh && timeout 120 bash release/sign.test.sh`
Expected: PASS, `package tests: 16 passed, 0 failed`, and the sign tests pass unchanged.

Run: `make -n release VERSION=1.2.3 | tail -1`
Expected: `bash release/package.sh 1.2.3 bin bin/release`, the target unchanged.

Run: `rg -n "install.sh" .github/workflows/release.yml`
Expected: the check step's three `install.sh` lines only, above `make release`, and no `cp install.sh`.

Run: `rg -n "not in the signed|attests its build instead" README.md; rg -n "install.sh" docs/versioning.md release/README.md`
Expected: the first search finds nothing; the second shows the new text in both files.

- [ ] **Step 5: Commit**

```bash
git add release/package.sh release/package.test.sh .github/workflows/release.yml Makefile README.md docs/versioning.md release/README.md CONTRIBUTING.md docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md
git commit -m "fix(release): sign install.sh through checksums.txt"
```

### Task 2: Stop the update runner once its record moved

After a crash loop, `darkpanel-rollback.service` rolls the update back while the runner unit `darkpanel-update-<id>` keeps polling the health probes until its 80 s deadline, because only the deadline timer is stopped. The runner now asks before each round of probes whether its record is still `swapped`, and returns as soon as it is not, so its unit goes inactive within one health interval of the rollback record being written.

**Files:**
- Modify: `internal/update/health.go:73-98`
- Modify: `internal/update/runner.go:70-73`
- Test: `internal/update/health_test.go`
- Test: `internal/update/runner_test.go`

**Interfaces:**
- Consumes: none
- Produces: `update.Probes.Wait` with the `moved` parameter (Contracts). Adds the test functions `TestRunStopsWaitingOnceRolledBack` and `TestProbesWaitStopsWhenMoved`; no new helpers.

**Items:** 96

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Add to `internal/update/runner_test.go`, after `TestRunLeavesAMovedRecordAlone`:

```go
func TestRunStopsWaitingOnceRolledBack(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.3.1")
	// A recover pass rolls the update back during the first probe; the
	// agent then reports the restored version, which is never healthy. The
	// hook reads the binary after the rollback: the installed argument was
	// read before it and would still name the new version.
	probes := 0
	e.agentHello = func(string) (core.AgentHello, error) {
		probes++
		if probes == 1 {
			if err := e.updater(t, "1.2.0").RollBack(ctx, tx, false); err != nil {
				t.Error(err)
			}
		}
		return core.AgentHello{BinaryVersion: installedVersion(e.p.Binary())}, nil
	}
	err := driveClock(e.clock, HealthInterval, nil, func() error { return e.updater(t, "1.2.0").Run(ctx, tx) })
	if err != nil {
		t.Fatalf("Run = %v, want nil: another process finished the record", err)
	}
	wantRolledBack(t, e, tx, "1.2.0", "1.3.1")
	// Without the check the runner probes until the deadline, about forty times.
	if probes != 1 {
		t.Errorf("the runner probed %d times, want 1: it must stop once the record left swapped", probes)
	}
}
```

In `internal/update/health_test.go`, the two existing `Wait` calls pass `nil` for `moved` so the file compiles against the new signature: in `runHealthWait`,

```go
	go func() { done <- p.Wait(context.Background(), clock, "1.2.0", "1.3.0", deadline, nil) }()
```

and in `TestProbesWaitStops`,

```go
	if err := (Probes{}).Wait(context.Background(), clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), nil); err == nil {
```

```go
	go func() { done <- p.Wait(ctx, clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), nil) }()
```

Then add a `Wait`-level test after `TestProbesWaitStops`:

```go
func TestProbesWaitStopsWhenMoved(t *testing.T) {
	clock := coretest.NewFakeClock(time.Unix(0, 0))
	agent := &healthAnswers[core.AgentHello]{answers: []healthAnswer[core.AgentHello]{healthHello("1.2.0", nil)}}
	p := Probes{Agent: agent.probe}
	asked := 0
	moved := func() bool {
		asked++
		return asked > 1
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait(context.Background(), clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), moved) }()
	for clock.Pending() == 0 {
		time.Sleep(time.Millisecond)
	}
	clock.Advance(HealthInterval)
	if err := <-done; !errors.Is(err, errMoved) {
		t.Errorf("Wait once the record moved = %v, want errMoved", err)
	}
	if asked != 2 {
		t.Errorf("moved was asked %d times, want 2: before each round of probes", asked)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -timeout 60s -run 'TestRunStopsWaitingOnceRolledBack|TestProbesWait' ./internal/update/`
Expected: FAIL to compile, `too many arguments in call to p.Wait` (the tests already pass `moved`).

- [ ] **Step 3: Write the implementation**

In `internal/update/health.go`, replace `Wait`'s comment and the start of its loop:

```go
// Wait applies the health rule (spec section 8) every HealthInterval of
// clock until both probes pass, which returns nil. A refused connection, a
// timeout or a report of version from is "not yet". A controller reporting
// version to with a status other than ok fails at once; still not passing at
// deadline fails too. moved, when not nil, is asked before each round of
// probes: once another process has finished the record, nobody acts on the
// answer, so Wait returns errMoved instead of polling to the deadline.
func (p Probes) Wait(ctx context.Context, clock core.Clock, from, to string, deadline time.Time, moved func() bool) error {
	if p.Agent == nil {
		return errors.New("update: health check: no agent probe")
	}
	for {
		if moved != nil && moved() {
			return errMoved
		}
		passed, err := p.check(ctx, to)
```

The rest of the loop is unchanged.

In `internal/update/runner.go`, `Run` passes a callback that re-reads the record. Replace:

```go
	health := u.restartServices(ctx)
	if health == nil {
		health = u.probes(ctx).Wait(ctx, u.cfg.Clock, r.FromVersion, r.ToVersion, deadline)
	}
```

with:

```go
	health := u.restartServices(ctx)
	if health == nil {
		// A recover pass may roll the update back while the runner waits;
		// records are replaced by rename, so reading without the lock is safe.
		moved := func() bool {
			cur, err := ReadRecord(u.cfg.Paths, tx)
			return err == nil && cur.State != core.UpdateSwapped
		}
		health = u.probes(ctx).Wait(ctx, u.cfg.Clock, r.FromVersion, r.ToVersion, deadline, moved)
	}
```

The code after it already handles the moved record: it takes the lock, re-reads the record, finds it is not `swapped`, logs `update: the transaction was finished by another process` and returns nil, so the runner unit exits 0 and no `OnFailure=` fires.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -race -timeout 300s ./internal/update/`
Expected: PASS, including `TestRunStopsWaitingOnceRolledBack`, `TestProbesWaitStopsWhenMoved`, `TestRunLeavesAMovedRecordAlone` and `TestRunHealthFailureRollsBack`.

Then prove the runner test depends on the callback: temporarily make the `moved` callback in `runner.go` `return false` as its first statement (passing `nil` instead would leave `moved` unused, which does not compile), and run `GOTOOLCHAIN=local timeout 300 go test -count=1 -timeout 60s -run TestRunStopsWaitingOnceRolledBack ./internal/update/`.
Expected: FAIL with `the runner probed 41 times, want 1` (the first probe plus one per 2 s sleep until the 80 s deadline; a count near 41 is the same result). Restore the callback and run the package again: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/update/health.go internal/update/runner.go internal/update/health_test.go internal/update/runner_test.go
git commit -m "fix(update): stop the runner once its record moved"
```

### Task 3: Pin that the agent socket dial cannot hang

The final review noted that `withAgent`'s first dial of the agent socket has no timeout. A Unix-socket dial in Go never waits for the listener to accept: a full backlog fails at once (see Assumptions). This task records that as a row of the existing fallback table and adds no timeout. The requests after the dial have no timeout either; that residual is a separate register row (see Assumptions), not this task.

**Files:**
- Test: `internal/cli/local_test.go`

**Interfaces:**
- Consumes: none
- Produces: adds the test helper `fullSocket` to package `cli` (test files only).

**Items:** 97

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

- [ ] **Step 1: Write the test**

Add after `staleSocket` in `internal/cli/local_test.go`:

```go
// fullSocket is a socket in dir whose listener never accepts and whose
// backlog is already full, so a dial cannot even be queued.
func fullSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "full.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	// A backlog of 0 still queues one connection; this one fills it.
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return path
}
```

In `TestLocalFallback`, add `full := fullSocket(t, dir)` after `stale := staleSocket(t, dir)`, and add this row after the "another dial error runs no fallback" row:

```go
		{name: "a full backlog fails at once and runs no fallback", sock: full, args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			code: ExitError, errOut: "darkpanel: the agent socket " + full + " does not answer: dial unix " + full + ": connect: resource temporarily unavailable\n"},
```

Add `"syscall"` to the imports if the file does not import it yet.

- [ ] **Step 2: Run the test**

Run: `GOTOOLCHAIN=local timeout 120 go test -count=1 -timeout 60s -run TestLocalFallback ./internal/cli/`
Expected: PASS. This test pins existing behaviour, so it does not fail first. A dial that blocked would hang this row until `-timeout 60s` ends the run with a panic, which is how the row catches a regression. If that happens now, the assumption is wrong: stop and report BLOCKED rather than adding a timeout.

- [ ] **Step 3: Run the package**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/cli/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/local_test.go
git commit -m "test(cli): pin that a full agent socket fails fast"
```

### Task 4: Give agent check its own failure status

`darkpanel agent check` answers 0 accepted, 1 refused and 2 unreachable, but a usage error also exits 2, and a run without root reaches the credentials, fails to read them and prints the "enroll again" advice with exit 1. The command now refuses to run without root (unless `--dev`) and exits 3 for every failure that is not a handshake outcome, printing no outcome line.

**Files:**
- Modify: `internal/install/install.go:44-53`
- Modify: `cmd/darkpanel/agent.go` (`newAgentCheckCmd`, lines 346-371)
- Test: `cmd/darkpanel/agent_test.go` (`TestAgentCheckExit`)
- Modify: `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md:439-440`

**Interfaces:**
- Consumes: none
- Produces: `install.CheckFailed` (Contracts). Adds the unexported function `checkFailed` to package `main` (cmd/darkpanel).

**Items:** 97

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

In `cmd/darkpanel/agent_test.go`, append to the end of `TestAgentCheckExit`, after the `--help` check:

```go
	refusals := []struct {
		euid int
		args []string
		out  string
	}{
		{1000, []string{"agent", "check"}, "agent check runs as root in production mode; run it with sudo, or add --dev"},
		{1000, []string{"agent", "check", "--dev", "extra"}, `unknown command "extra"`},
		{0, []string{"agent", "check", "--nope"}, "unknown flag: --nope"},
	}
	for _, tt := range refusals {
		s := fakeAgent(t, tt.euid)
		s.checkRes = install.CheckAccepted
		stdout, stderr, code := runStdin(strings.NewReader(""), tt.args...)
		if code != int(install.CheckFailed) || stdout != "" || !strings.Contains(stderr, tt.out) {
			t.Errorf("darkpanel %s: exit %d, stdout %q, stderr %q; want exit %d, no outcome and %q",
				strings.Join(tt.args, " "), code, stdout, stderr, install.CheckFailed, tt.out)
		}
	}
	s := fakeAgent(t, 0)
	s.checkRes = install.CheckAccepted
	resolvePaths = func(bool) (paths.Paths, error) { return paths.Paths{}, errors.New("no state directory") }
	if stdout, stderr, code := runStdin(strings.NewReader(""), "agent", "check"); code != int(install.CheckFailed) ||
		stdout != "" || !strings.Contains(stderr, "no state directory") {
		t.Errorf("agent check with unresolvable paths: exit %d, stdout %q, stderr %q; want exit %d and the error",
			code, stdout, stderr, install.CheckFailed)
	}
	if out, _ := runArgs("agent", "check", "--help"); !strings.Contains(out, "3 the check could not run") {
		t.Errorf("agent check --help = %q, want exit status 3 documented", out)
	}
```

`fakeAgent`'s cleanup restores the `resolvePaths` seam the last block replaces. `paths` and `errors` are already imported by `agent_test.go`; check, and add them if not.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run TestAgentCheckExit ./cmd/darkpanel/`
Expected: FAIL to compile, `undefined: install.CheckFailed`.

- [ ] **Step 3: Write the implementation**

In `internal/install/install.go`, the result list becomes:

```go
// CheckResult is the outcome of one handshake with the saved agent
// credentials, the exit status of darkpanel agent check.
type CheckResult int

// The outcomes of Agent.Check, and CheckFailed for an agent check that could
// not try a handshake.
const (
	CheckAccepted    CheckResult = iota // the controller accepted the credentials
	CheckRefused                        // the controller refused them
	CheckUnreachable                    // no handshake completed
	CheckFailed                         // the check did not run: a usage error, no root, unresolvable paths
)
```

In `cmd/darkpanel/agent.go`, replace `newAgentCheckCmd` with:

```go
func newAgentCheckCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that the controller accepts this agent's credentials",
		Long: "Check that the controller accepts this agent's credentials with one handshake. The controller " +
			"treats the check as this host's session, so it replaces a running agent's session for a moment " +
			"and the agent reconnects.\n\n" +
			"Exit status: 0 accepted, 1 refused (enroll again with a new token), 2 unreachable, " +
			"3 the check could not run.",
		Args: func(cmd *cobra.Command, args []string) error {
			return checkFailed(cobra.NoArgs(cmd, args))
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return checkFailed(requireRoot("check", &dev)(cmd, args))
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return checkFailed(err)
			}
			res, err := daemonCheck(cmd.Context(), p.AgentTLSDir)
			if _, werr := fmt.Fprintln(cmd.OutOrStdout(), checkWords(res)); werr != nil && err == nil {
				err = werr
			}
			return cli.ExitWith(int(res), err)
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return checkFailed(err) })
	addDevFlag(cmd, &dev)
	return cmd
}

// checkFailed gives a failure of agent check that is not a handshake outcome
// its own exit status, so a script never reads it as refused or unreachable.
func checkFailed(err error) error {
	if err == nil {
		return nil
	}
	return cli.ExitWith(int(install.CheckFailed), err)
}
```

In the spec, `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md` lines 439-440, change "(one mTLS handshake with the saved credentials; exit 0 accepted, 1 refused, 2 unreachable)" to "(one mTLS handshake with the saved credentials; exit 0 accepted, 1 refused, 2 unreachable, 3 the check could not run)", keeping the line wrapping of the surrounding list item.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 ./cmd/darkpanel/ ./internal/install/ ./internal/agent/daemon/`
Expected: PASS. The `unknown command "extra"` text comes from `cobra.NoArgs`; if cobra words it differently, match its actual message in the test rather than changing the code.

- [ ] **Step 5: Commit**

```bash
git add internal/install/install.go cmd/darkpanel/agent.go cmd/darkpanel/agent_test.go docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md
git commit -m "fix(agent): exit 3 when agent check cannot run"
```

### Task 5: Harden the break-glass rollback paths

Two edge cases of `darkpanel update rollback`:
- With `--local` and no agent, the rollback runs in `darkpanel.prev`. A Ctrl+C or a closed terminal kills it halfway and leaves the services stopped until `update recover` runs. The child now runs in its own process group, and the parent holds SIGINT and SIGHUP until the child finishes, so the rollback always completes and reports.
- `--direct --transaction`, the form the rollback's transient unit uses, accepted `--force` and `--json` and ignored them. It now refuses both as usage errors.

**Files:**
- Modify: `cmd/darkpanel/update.go` (`addDirectRollback`, `rollbackFallback`)
- Test: `cmd/darkpanel/update_test.go`
- Modify: `README.md`, the `### Break-glass` section only (the Install section belongs to Task 1, which runs first and moves the line numbers)

**Interfaces:**
- Consumes: none
- Produces: adds the test function `TestUpdateRollbackFallbackSurvivesSignals` and the test helper `writeSignallingPrevBinary` to package `main` (cmd/darkpanel).

**Items:** 97

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3

- [ ] **Step 1: Write the failing tests**

In `cmd/darkpanel/update_test.go`, add two rows to the `TestUpdateCommands` table after the `--direct --transaction 01HZY6RB` row:

```go
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB", "--force"}, 0, cli.ExitUsage, nil, "--force does not apply with --transaction"},
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB", "--json"}, 0, cli.ExitUsage, nil, "--json does not apply with --transaction"},
```

Add after `TestUpdateRollbackFallback`:

```go
// writeSignallingPrevBinary installs a previous binary that interrupts and
// hangs up on the process that started it, as a terminal's Ctrl+C or a
// closed SSH session would, records whether it leads its own process group,
// and only then prints its rollback transaction.
func writeSignallingPrevBinary(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
read -r stat < /proc/$$/stat
set -- $stat
echo "$5" > "$0.pgrp"
kill -INT $PPID
kill -HUP $PPID
sleep 0.2
printf '{"id":"01HZY6RB","operationId":"01HZY6RB","parentId":null,"kind":"rollback","fromVersion":"0.3.0","toVersion":"0.2.0","targetVersion":"0.2.0","state":"rolled-back","error":null,"startedAt":"2026-10-20T00:00:00Z","updatedAt":"2026-10-20T00:00:01Z"}\n'
`
	if err := os.WriteFile(p.PrevBinary(), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the fake binary must be executable
		t.Fatal(err)
	}
}

func TestUpdateRollbackFallbackSurvivesSignals(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "agent.sock")
	_, layout := fakeUpdate(t, 0)
	writeSignallingPrevBinary(t, layout.Paths)
	var stdout, stderr bytes.Buffer
	code := run([]string{"update", "rollback", "--local", "--yes", "--json"},
		cli.Options{AgentSocket: missing, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != cli.ExitOK || !strings.Contains(stdout.String(), `"state":"rolled-back"`) {
		t.Fatalf("exit %d, stdout %q, stderr %q; want the rollback's transaction", code, stdout.String(), stderr.String())
	}
	pgrp, err := os.ReadFile(layout.Paths.PrevBinary() + ".pgrp")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(pgrp)); got == strconv.Itoa(syscall.Getpgrp()) {
		t.Errorf("the previous binary ran in this process group (%s); a terminal's signals would reach it", got)
	}
}
```

Add `"strconv"` to the imports. Do not call `t.Parallel()` in this test (Global Constraints).

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestUpdateCommands|TestUpdateRollbackFallbackSurvivesSignals' ./cmd/darkpanel/`
Expected: FAIL. The two new `TestUpdateCommands` rows exit 0 instead of 2, and the signal test kills the test binary (`signal: interrupt`), because nothing holds SIGINT yet.

- [ ] **Step 3: Write the implementation**

In `cmd/darkpanel/update.go`, `addDirectRollback`'s replacement `RunE` refuses the two flags with `--transaction`. Insert after the loop over `"local", "yes", "confirmation", "input"` and before `rootRefusal`:

```go
		if tx != "" {
			if cmd.Flags().Changed("force") {
				return cli.ExitWith(cli.ExitUsage, errors.New("--force does not apply with --transaction: the guards ran when the rollback started"))
			}
			if cmd.Flags().Changed("json") {
				return cli.ExitWith(cli.ExitUsage, errors.New("--json does not apply with --transaction, which prints no result"))
			}
		}
```

In `rollbackFallback`, replace the lines from `var out bytes.Buffer` through `if err := run.Run(); err != nil {` with:

```go
		var out bytes.Buffer
		run := exec.CommandContext(ctx, prev, args...) //nolint:gosec // G204: the installed previous binary, never a path from a request
		run.Stdout, run.Stderr = &out, stderr
		// A rollback cut short leaves the services stopped. The previous
		// binary may predate this code, so it is protected from here: in its
		// own process group a terminal's Ctrl+C and hang-up never reach it,
		// and this process holds both until it has finished and reported.
		run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		held := make(chan os.Signal, 1)
		signal.Notify(held, os.Interrupt, syscall.SIGHUP)
		defer signal.Stop(held)
		if err := run.Run(); err != nil {
```

Add `"os/signal"` and `"syscall"` to the imports if `update.go` does not import them yet.

In `README.md`, under the `### Break-glass` heading, the paragraph's text from "With the agent stopped too," to its end becomes:

```markdown
With the agent stopped too, `sudo darkpanel update rollback --local --yes`
puts the previous binary back; neither Ctrl+C nor a closed terminal stops it
once it runs. If the rollback is still cut short, for example by a crash or a
power loss, the services can stay stopped until the next boot; `sudo
darkpanel update recover` finishes a rollback that had started replacing the
binary and restarts the services, or ends one that had not, so it can be run
again.
```

Keep the paragraph's first sentence ("On a host whose controller cannot be reached, …") unchanged, and rewrap at the width of the surrounding text.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 ./cmd/darkpanel/`
Expected: PASS, including `TestUpdateRollbackFallback`, whose rows are unchanged.

Run: `GOTOOLCHAIN=local timeout 600 go test -count=5 -run TestUpdateRollbackFallbackSurvivesSignals ./cmd/darkpanel/`
Expected: PASS five times; the held signals never reach the test binary's default handler.

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/update.go cmd/darkpanel/update_test.go README.md
git commit -m "fix(update): protect the break-glass rollback"
```
