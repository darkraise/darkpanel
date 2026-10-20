# M2 Local Timeout and README Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `--local` command whose agent accepts the connection but never answers fails after its time limit with a message naming the socket and the limit, and the README tells users who check `install.sh` by hand to compare the signed version and channel with the release they meant to install.

**Architecture:** Two independent changes. In `internal/cli/local.go`, the per-request limit of the agent-socket calls becomes a package variable `localTimeout` (equal to `requestTimeout`, 90 s) so a test can shorten it, and `withAgent` turns a request that ran out of time into `the agent socket <path> did not answer within <limit>`; a table row against a socket that never accepts pins both. `README.md`'s install section gains two sentences.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `github.com/spf13/cobra`, Markdown.

**Spec:** docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** inline — `claude --model sonnet --effort low` — both tasks total 2 or less and none is heavy, so the session implements them itself

**Plan review:** 2026-10-21 — dr-superpowers:judge-opus — executability 18 / coherence 19 / coverage 19 / assumptions 18 (round 1)

**Parallelism:** sequential — two tasks of a few minutes each; a wave would cost more in worktree setup and merging than it saves

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is a small tmpfs that parallel Go builds fill: every `go` command, `golangci-lint` and `make` run with `TMPDIR=/root/.cache/plan8-tmp` (create it with `mkdir -p`, empty it when the plan ends), also where a step's command omits it.
- Dependencies: no task edits `go.mod` or `go.sum`. After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing.
- Files: a task touches only the files its **Files:** block names.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings in `internal/cli` carry no `darkpanel:` prefix, because `cli.PrintError` adds it.
- `internal/cli`'s tests are not parallel (`rg -c t.Parallel internal/cli` finds none), which is what lets a test swap the package variable `localTimeout`; the test that swaps it must not call `t.Parallel()` either.
- Unix sockets in `internal/cli` tests live under `socketDir(t)` so their paths stay short.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean.
- Never push `main`, and never push or dispatch a workflow without asking the owner first.

## Contracts

None.

## Assumptions (evidence)

- Every agent-socket request of a `--local` command already runs under `context.WithTimeout(..., requestTimeout)`, 90 s (internal/cli/local.go:154, 166, 193, 203; internal/cli/pin.go:18), added in `ff99a82` on 2026-10-20. The register row's premise that the requests have no timeout looked only at `api.NewClient` (internal/agent/api/client.go:58), which leaves the limit to its callers; what the row's acceptance still lacks is a clear error and a test.
- Against a Unix listener that never accepts, an `api.Client` request with a 200 ms context returns after 200 ms with `api: POST actions/demo.item.list/v1/read: Post "http://agent/agent/v1/actions/demo.item.list/v1/read": context deadline exceeded`, and `errors.Is(err, context.DeadlineExceeded)` is true: a scratch test in `internal/cli` on 2026-10-21 (two requests, 200.5 ms each). The dial succeeds because Linux queues it in the listener's backlog.
- The other agent-socket clients are already bounded: `installAgent.Check` uses `agentHelloTimeout`, 10 s (cmd/darkpanel/system.go:35, 254), and the update health probe calls `Probes.Agent` under `context.WithTimeout(ctx, HealthInterval)` (internal/update/health.go:119-120).
- An error that a command's `RunE` returns exits `ExitError`, 1 (internal/cli/errors.go:59-73, 134-135), as the existing socket-failure rows of `TestLocalFallback` expect (internal/cli/local_test.go:357-364).
- A timed-out apply leaves the change's outcome unknown to the CLI, as it already does today after 90 s; the new message does not claim otherwise, and the operator can read the agent's changes once it answers again. Changing what a timed-out apply reports is outside this plan.

## Task index

1. Name the socket and the limit when a local request times out
2. Tell users to compare the signed version before installing

---

### Task 1: Name the socket and the limit when a local request times out

An agent that accepts the connection and then hangs makes a `--local` command wait 90 s and print the transport's `context deadline exceeded`, which names neither the socket nor the limit. The fix names both and pins the bound with a test against a socket that never accepts.

**Files:**
- Modify: `internal/cli/local.go:101-130` (a new variable above `dialAgent`, the end of `withAgent`) and `internal/cli/local.go:154, 166, 193, 203` (the four `requestTimeout` uses)
- Test: `internal/cli/local_test.go` (a new helper after `fullSocket`, two rows and a timeout swap in `TestLocalFallback`)

**Interfaces:**
- Consumes: none
- Produces: package variable `localTimeout` (internal/cli, unexported) and test helper `frozenSocket(t *testing.T, dir string) string` (internal/cli tests)

**Items:** 100

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing test**

In `internal/cli/local_test.go`, add this helper directly after the `fullSocket` function:

```go
// frozenSocket is a socket in dir whose listener never accepts: a dial is
// queued in its backlog and succeeds, but no request is ever answered, as
// with an agent that hangs.
func frozenSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "frozen.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return path
}
```

In `TestLocalFallback`, replace the line `full := fullSocket(t, dir)` with:

```go
	full := fullSocket(t, dir)
	frozen := frozenSocket(t, dir)
	saved := localTimeout
	localTimeout = 200 * time.Millisecond
	t.Cleanup(func() { localTimeout = saved })
```

and append these two rows to its `tests` table, after the row named `"a full backlog fails at once and runs no fallback"`:

```go
		{name: "a frozen agent's read ends at the limit", sock: frozen, args: []string{"demo", "item", "list"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + frozen + " did not answer within 200ms\n"},
		{name: "a frozen agent's plan ends at the limit and runs no fallback", sock: frozen,
			args: []string{"demo", "item", "remove", "--id", "i1", "--yes"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + frozen + " did not answer within 200ms\n"},
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan8-tmp && GOTOOLCHAIN=local TMPDIR=/root/.cache/plan8-tmp timeout 300 go test -count=1 -timeout 60s -run '^TestLocalFallback$' ./internal/cli/`
Expected: FAIL to build with `undefined: localTimeout`.

- [ ] **Step 3: Write the implementation**

In `internal/cli/local.go`, add directly above `func (a *app) dialAgent`:

```go
// localTimeout bounds one request to the agent socket. It is a variable so
// tests can shorten it.
var localTimeout = requestTimeout
```

Replace each of the four `context.WithTimeout(..., requestTimeout)` calls in `local.go` (in `localRead`, `localResource`, and the `plan` and `apply` functions of `localCalls`) with the same call using `localTimeout`:

```go
		ctx, cancel := context.WithTimeout(cmd.Context(), localTimeout)
```

```go
			ctx, cancel := context.WithTimeout(ctx, localTimeout)
```

Replace the last line of `withAgent`, `return run(api.NewClient(api.ClientConfig{Dial: a.dialAgent}))`, with:

```go
	err = run(api.NewClient(api.ClientConfig{Dial: a.dialAgent}))
	// A hung agent still accepts the connection, so its request ends at
	// localTimeout with the transport's message, which names neither the
	// socket nor the limit.
	if errors.Is(err, context.DeadlineExceeded) && cmd.Context().Err() == nil {
		return fmt.Errorf("the agent socket %s did not answer within %s", a.opts.AgentSocket, localTimeout)
	}
	return err
```

`errors`, `context` and `fmt` are already imported; add no import.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan8-tmp timeout 300 go test -count=1 -timeout 120s ./internal/cli/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/cli`

Run: `rg -n 'requestTimeout' internal/cli/local.go`
Expected: exactly one line, the `var localTimeout = requestTimeout` declaration.

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan8-tmp timeout 600 go vet ./internal/cli/ && TMPDIR=/root/.cache/plan8-tmp timeout 900 golangci-lint run --allow-serial-runners ./internal/cli/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output except golangci-lint's `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/local.go internal/cli/local_test.go
git commit -m "fix(cli): name the socket when a local request hangs"
```

### Task 2: Tell users to compare the signed version before installing

An older release's `checksums.txt` is signed too and verifies together with that release's older `install.sh`, so a user who checks `install.sh` by hand has to compare the version and channel minisign prints with the release they meant to install. The README's install section names the trusted comment but does not say to compare it.

**Files:**
- Modify: `README.md:71-73`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 101

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

- [ ] **Step 1: Write the failing check**

Run: `rg -n 'meant to install' README.md`
Expected: no output, exit status 1.

- [ ] **Step 2: Write the change**

In `README.md`, replace these lines of the install section:

```markdown
The trusted comment minisign prints names the version and channel. The
release workflow also attests every file, so `gh attestation verify install.sh
--repo darkraise/darkpanel` is a second check.
```

with:

```markdown
The trusted comment minisign prints names the version and channel. Check that
they are the release you meant to install: an older release's `checksums.txt`
is signed too, and verifies together with that release's older `install.sh`.
The release workflow also attests every file, so `gh attestation verify
install.sh --repo darkraise/darkpanel` is a second check.
```

- [ ] **Step 3: Run the check to verify it passes**

Run: `rg -n 'meant to install' README.md && git diff --stat README.md`
Expected: one match in the install section, and `README.md | 8 +++++---`.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs(readme): compare the signed version before install"
```
