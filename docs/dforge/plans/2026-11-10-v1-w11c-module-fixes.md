# v1 wave 11c: module fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the follow-up rows left open after wave 11b: the module fixes ruling W11-R1 names for wave 11c, and the wave 11a and 11b minors no plan has taken.

**Architecture:** Each task is one module's fixes with its own regression tests, ordered so the security-relevant ones are self-implemented and the rest go to the agy lane. Rulings W11-R13 to W11-R18 settle the rows that needed a decision, and Task 13 copies them into the spec and the threat model.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), `golang.org/x/sys/unix` openat2, React 19 + TypeScript with Vitest, the KVM harness in `test/harness` (GitHub runners only).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 with risk 3 (the certificate reader), and tasks carry an Executor line

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w11c-tmp/integ && export TMPDIR=/root/.cache/w11c-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

> **External executors:** agy

**Plan review:** 2026-11-10 — dforge:judge-opus — executability 13 / coherence 16 / coverage 14 / assumptions 11 (round 2)

## Global Constraints

- Scope is spec revision 8 as amended on `main`, plus rulings W11-R13 to W11-R18 under Assumptions, which Task 13 copies into the spec.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `ip6tables`, `iptables-restore`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w11c-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends.
- No task runs `go clean -cache` or `go clean -testcache`: the build cache is shared with other sessions on this host.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1` on the task's packages (every line `ok`); `golangci-lint run --allow-serial-runners` on the task's packages (`0 issues.`).
- Generated files: no task changes a wire type, so `schemas/**` and `docs/api/**` stay unchanged.
- TDD: write the test, run it and see the expected RED, then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A row that pins behaviour already present passes before the change; the step says so.
- Frozen files (spec 1.12): no task edits `internal/core/**` except tests, `internal/platform/*.go` except `files.go` and its new test (Task 5), `go.mod`, `go.sum`, `test/harness/pins.go` or `schemas/**`. Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it. A pre-existing test in the task's own package whose assertion the task's change makes stale is updated to the new behaviour.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- File ownership: only Task 13 edits the spec, `docs/security/threat-model.md`, `internal/docs/` and `Makefile`. The plan runs on branch `v1-w11c-module-fixes` in the base repository. A task carrying an agy **Executor:** line runs in a throwaway detached worktree under the session scratchpad, because the agy wrapper refuses the primary checkout; the session fast-forwards the branch to the reviewed commits and removes the worktree.
- Go identifiers: every new test function carries its task's prefix from Contracts, and every new unexported test helper the same prefix in lower case.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name; a changed refusal keeps its error code.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies: Task 12 edits `test/harness/`. Push the branch and dispatch `harness.yml` on it per the owner's standing authorization, after Task 13. No task creates or pushes a tag or runs `release.yml`.

## Contracts

### Test prefixes

| Task | Package | Go test prefix |
|---|---|---|
| 1, 2 | `internal/modules/npm` | `TestW11c01`, `TestW11c02` |
| 3 | `internal/modules/docker` | `TestW11c03` |
| 4 | `internal/modules/derp` | `TestW11c04` |
| 5 | `internal/platform`, `internal/modules/derp` | `TestW11c05` |
| 6 | `internal/modules/tailscale` | `TestW11c06` |
| 7 | `internal/controller/listen`, `internal/controller/backup` | `TestW11c07` |
| 8 | `internal/controller/auth`, `internal/modules/derp` | `TestW11c08` |
| 10 | `internal/modules/firewall/guard` | `TestW11c10` |
| 11 | `internal/modules/firewall`, `.../dockerfw`, `internal/platform/netstat` | `TestW11c11` |
| 13 | `internal/docs` | `TestW11c13` |

### Certificate reader

Produced by Task 5, consumed only inside Task 5.

- `platform.ReadRegularFileBeneath(dir, name string, limit int64) ([]byte, error)` in `internal/platform/files.go`. It opens `dir` following symbolic links, then opens `name` relative to it with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`.
- `certFileReader(dir string) func(name string) ([]byte, error)` in `internal/modules/derp/deps.go`, which refuses a `name` that does not start with `dir + "/"`. The fs.ErrNotExist row of `TestW11c05CertFileReaderWiring` closes row 42's handler question: `readCert` receives this reader's error unchanged.

## Assumptions (evidence)

- W11-R13 custom locations (rows 26, 36, 37): the undo of `npm.proxyhost.update` or `remove` keeps W10b-R3's check on the host's forward host. For a custom location it still refuses a reserved upstream, but a location whose forward host is empty, holds an nginx variable, or is a name with no answer is restored as it was: Darkpanel never authors a custom location, and an undo only puts back what NPM already held. An update keeps operator locations without checking them, for the same reason. Owner did not object when offered, 2026-11-10.
- W11-R14 CreateAdmin (row 39): only a 4xx other than 400 is a conflict. A 400 rejects the payload, so it fails the deploy without removing the instance.
- W11-R15 certificate reads (rows 42, 54): the DERP certificate reads refuse symbolic links only beneath `server.CertDir`. Links above that directory are the host owner's and cannot be planted by the service account. Before this ruling `internal/modules/derp/deps.go` was the only caller of `platform.ReadRegularFile`.
- W11-R16 probe addresses (row 43): `nodeProbeAllowedAddr` also refuses the special-purpose forms that reach internal networks (`0.0.0.0/8`, `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`, `::/96`, `64:ff9b::/96`, `64:ff9b:1::/48`, `2001::/32`, `2002::/16`). The documentation ranges stay allowed; 55 lines of `node_probe*_test.go` use them. An allowlist stays out of scope, as W10b-R11 holds it.
- W11-R17 repository files (row 52): a failed `ensureRepo` step removes the files that attempt wrote. A later failed pin step leaves them, because uninstall owns them (`internal/modules/tailscale/package_pin.go`).
- Plan review round 2 scored executability 13 and assumptions 11 (borderline). Accepted: the remaining gaps are test bodies described in prose in Tasks 1, 2 and 10 because their fixtures are named but not reproduced; each executor reads the named fixture.
- W11-R18 closures: rows closed with no code carry their reason in the register, set when this plan was written (security review 33, 34, 79; v1-completion 452, 455, 459, 461; deferred 456, 462, 463). Row-part closures: 31's clock pacing, 38's `4e2` parse, 41's journal digests and nil Sealer (every production `core.Env` sets `Sealer`: `internal/controller/server/services.go`, `internal/agent/runtime/runtime.go`, `internal/install/uninstall.go`), 43's reason text, 44's per-pair lock (threat model), 36's DNS rebinding (W10b-R3), 54's label render (no production caller of `NetworkCreated` or `VolumeCreated`) and its A26 wording (a closed plan's assumption).
- `renderWithProviders` returns `client` (`web/src/test/utils.tsx:70`).
- Every `readCertFile` caller passes a path under `server.CertDir`: unverified — Task 5 verifies it.

## Task index

1. npm custom locations in undo
2. npm deploy and effect fixes
3. Docker comments and redaction
4. DERP probe address forms
5. Certificate reads beneath the directory
6. Tailscale repository file rollback
7. Stored bindings and hard-linked backups
8. Auth comments and small tests
9. NPM expose alerts
10. Firewall messages and guard cost
11. Firewall and netstat tests
12. Harness live-apply cleanup
13. Spec, threat model and test timeout

---

### Task 1: npm custom locations in undo

**Files:**
- Modify: `internal/modules/npm/proxyhost_edit.go`, `internal/modules/npm/proxyhost_add.go`
- Create: `internal/modules/npm/proxyhost_locations_test.go`
- Test: `internal/modules/npm/proxyhost_effects_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#26, 2026-11-06-v1-security-review#36, 2026-11-06-v1-security-review#37

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/modules/npm/proxyhost_locations_test.go`:

```go
package npm

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func TestW11c01UndoKeepsLocationsItCannotCheck(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, host string
		port       int
		want       errcode.Code
	}{
		{"an nginx variable", "$upstream", 9000, ""},
		{"an empty host", "", 9000, ""},
		{"a name with no answer", "only-the-container", 9000, ""},
		{"a bracketed IPv6 literal", "[2001:db8::1]", 9000, ""},
		{"a reserved upstream", "127.0.0.1", 81, errcode.NPMReserved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			h.deployed()
			h.addChange("c0", "npm.admin.expose")
			body := proxyHostBody(puInput(), "")
			body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: tc.host, ForwardPort: tc.port})
			host := h.npm.SeedProxyHost(puHostOf(body))
			h.addChange("c1", "npm.proxyhost.update")
			n := h.nm()
			resolve := n.d.resolve
			// Only the host's own forward name resolves, so a location's check gets no answer.
			n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
				if name != "app" {
					return nil, nil
				}
				return resolve(ctx, name)
			}
			def := n.proxyhostUpdate()
			next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
			if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
				t.Fatalf("the update refused a kept location: %v", err)
			}
			effects := h.journal.effects()
			_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
			if tc.want != "" {
				if errcode.From(err).Code != tc.want || !core.IsDefinite(err) {
					t.Fatalf("inverse = %v, want a definite %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("inverse = %v, want the location restored", err)
			}
			hosts := puHosts(t, h)
			if len(hosts) != 1 || hosts[0].Locations[len(hosts[0].Locations)-1].ForwardHost != tc.host {
				t.Errorf("NPM holds %+v, want the location forwarding to %q", hosts, tc.host)
			}
		})
	}
}

func TestW11c01ContextEndingDuringALocationCheckStopsTheUndo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "ends-the-context", ForwardPort: 9000})
	host := h.npm.SeedProxyHost(puHostOf(body))
	h.addChange("c1", "npm.proxyhost.update")
	n := h.nm()
	resolve := n.d.resolve
	armed := false
	n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
		if name == "ends-the-context" {
			if armed {
				cancel()
			}
			return nil, errors.New("no answer in time")
		}
		return resolve(ctx, name)
	}
	def := n.proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	effects := h.journal.effects()
	armed = true
	_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
	if !errors.Is(err, context.Canceled) || core.IsDefinite(err) {
		t.Fatalf("inverse = %v, want the cancellation and no definite mark", err)
	}
}
```

In `proxyhost_effects_test.go::TestV1r10InverseNeverRestoresFromTheDocument`, change the first check so it also pins the cause:

```go
			if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "holds no sealed proxy host") {
				t.Fatalf("inverse without a sealed body = %v, want a definite refusal for the missing sealed body", err)
			}
```

Also add `TestW11c01EngineFailureDuringALocationCheckStopsTheUndo`, shaped like the context test above: the resolver wrapper arms the fake Docker client's `InspectContainer` failure when it sees `app`, so the fault hits the location's check and not the host's. Assert a non-Definite error and that `puHosts` is unchanged. Add a row driving `n.proxyhostRemove()`'s inverse for the `$upstream` location, so both inverses are pinned to `checkRestored`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run 'TestW11c01|TestV1r10InverseNeverRestoresFromTheDocument' ./internal/modules/npm/`
Expected: FAIL in `TestW11c01UndoKeepsLocationsItCannotCheck` for the variable, empty and no-answer rows, with `invalid_input` errors. The bracketed and reserved rows, `TestW11c01ContextEndingDuringALocationCheckStopsTheUndo` and the extended test pass already and pin what must not change.

- [ ] **Step 3: Implement**

1. In `proxyhost_edit.go`, `checkRestored` keeps `checkForward` for the body's own forward host, returning its error unchanged.
2. Each custom location goes through a new `checkRestoredLocation(ctx, env, l npmapi.Location) error`. An empty forward host or one containing `$` returns nil without a check. Otherwise call `checkForward` on it (`proxy.LiteralAddr` already reads a bracketed address), and return nil when the error is Definite with code `errcode.InvalidInput`. Return every other error unchanged, so `npm.reserved`, an Engine failure and an ended context still stop the undo.
3. Add a why comment on `checkRestoredLocation`: a custom location is the operator's and the undo only restores what NPM held.
4. On `proxyHostEditKeep` add a why comment that kept locations are written back unchecked for that same reason.
5. In `proxyhost_add.go`, the `proxyHostState` doc comment changes `what an update sets and an undo restores` to `what an update sets and an undo compares with; the undo restores the sealed body`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/npm/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/proxyhost_edit.go internal/modules/npm/proxyhost_add.go internal/modules/npm/proxyhost_locations_test.go internal/modules/npm/proxyhost_effects_test.go
git commit -m "fix(npm): restore locations the undo cannot check"
```

### Task 2: npm deploy and effect fixes

**Files:**
- Modify: `internal/modules/npm/instance_deploy.go`, `internal/modules/npm/effects.go`, `internal/modules/npm/proxyhost_add.go`, `docs/user/modules/npm.md`
- Create: `internal/modules/npm/instance_deploy_status_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#38, 2026-11-06-v1-security-review#39, 2026-11-06-v1-security-review#50

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/modules/npm/instance_deploy_status_test.go`:

```go
package npm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

type w11c02Refuser struct {
	npmAPI
	status int
}

func (r w11c02Refuser) CreateAdmin(context.Context, string, string) error {
	return &npmapi.APIError{Status: r.status}
}

func TestW11c02OnlyAnExistingAdminIsAConflict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		removes bool
	}{
		{"a rejected payload", 400, false},
		{"permission denied", 403, true},
		{"a conflict", 409, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			anon := n.d.anon
			n.d.anon = func() npmAPI { return w11c02Refuser{npmAPI: anon(), status: tc.status} }
			_, err := dpApply(h, n, "c1")
			if errcode.From(err).Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if removed := len(v1r12Removals(h)) > 0; removed != tc.removes {
				t.Errorf("removed the instance: %v, want %v", removed, tc.removes)
			}
			if n := strings.Count(err.Error(), "did not create the administrator"); n != 1 {
				t.Errorf("the error states its detail %d times, want once: %v", n, err)
			}
		})
	}
}

// A host whose NPM already reports Setup reaches the errcode.New branch of discardDeploy; arrange it as
// TestV1r12BootstrapConflictRemovesTheInstance does and require "already has an administrator" exactly once.

func TestW11c02WithoutDefiniteMarkKeepsWrapperText(t *testing.T) {
	cause := errors.New("cause")
	wrapped := withoutDefiniteMark(fmt.Errorf("outer: %w", core.Definite(cause)))
	if core.IsDefinite(wrapped) || !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "outer: ") {
		t.Errorf("a mark beneath a wrapper gave %v (definite %v)", wrapped, core.IsDefinite(wrapped))
	}
	joined := withoutDefiniteMark(errors.Join(core.Definite(cause), errors.New("and more")))
	if core.IsDefinite(joined) || !strings.Contains(joined.Error(), "and more") {
		t.Errorf("a joined mark gave %v (definite %v)", joined, core.IsDefinite(joined))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c02 ./internal/modules/npm/`
Expected: FAIL. The 400 row reports `removed the instance: true, want false`, and the 403 and 409 rows report the detail stated twice. The wrapper test loses `outer: `. The joined case passes already.

- [ ] **Step 3: Implement**

1. In `instance_deploy.go`, `bootstrap` treats a `CreateAdmin` error as a conflict only when `npmapi.IsClientError(err)`, the status of its `*npmapi.APIError` is not 400, and `ctx.Err() == nil`. Update the doc comment above `bootstrap` so a conflict means an existing administrator and a 400 is named as a failure.
2. In `discardDeploy`, when `cause` is an `*errcode.Error`, wrap what it wraps (`errors.Unwrap(coded)`), or use `errcode.New` with the combined detail when that is nil, so the detail appears once. The combined text and code stay as they are today.
3. In `effects.go`, `withoutDefiniteMark` records `err.Error()` first and strips marks as today. When the stripped error's text differs from the recorded text, it returns a small unexported error type whose `Error()` is the recorded text and whose `Unwrap()` is the stripped error. The fallback through `notDefinite` is unchanged.
4. In `proxyhost_add.go`, the `proxyhostAddAnswer` doc comment ends `err stays the cause, so refused can still read NPM's status.`
5. In `docs/user/modules/npm.md`, replace `If the administrator cannot be created for another reason` with `If the administrator cannot be created for any reason but an existing administrator`, rewrapping the paragraph at about 80 columns.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/npm/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_deploy.go internal/modules/npm/effects.go internal/modules/npm/proxyhost_add.go internal/modules/npm/instance_deploy_status_test.go docs/user/modules/npm.md
git commit -m "fix(npm): treat only a held admin as a conflict"
```

### Task 3: Docker comments and redaction

**Files:**
- Modify: `internal/modules/docker/inspect.go`, `internal/modules/docker/fuzz_test.go`
- Test: `internal/modules/docker/label_secrets_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#40, 2026-11-06-v1-security-review#41

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 0 = 3

- [ ] **Step 1: Extend the test `internal/modules/docker/label_secrets_test.go::TestV1r14LabelInputsAreRedacted`**

After the existing `strings.Contains(got, v1r14Secret)` check, add:

```go
			if !strings.Contains(got, `"labels":"[redacted]"`) {
				t.Errorf("the recorded input %s does not show the labels as [redacted]", got)
			}
```

- [ ] **Step 2: Run test to verify the form**

Run: `go test -count=1 -run TestV1r14LabelInputsAreRedacted ./internal/modules/docker/`
Expected: PASS: it pins behaviour already present. If it fails, the recorded mask differs from `[redacted]`: report the actual text and stop.

- [ ] **Step 3: Implement**

1. In `inspect.go`, `redactCmd` and `redactArgs` share one unexported helper `maskFrom(args []string, first int) []string`, which keeps the elements before `first` and masks the rest. `redactCmd` passes 0 and `redactArgs` passes 1. An empty `args` is returned as it is. Keep both doc comments.
2. In `fuzz_test.go`, the `fz6Scrub` doc comment says `each element of the command, each argument of the entrypoint after the first` in place of `each argument after the first`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/docker/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/docker/inspect.go internal/modules/docker/fuzz_test.go internal/modules/docker/label_secrets_test.go
git commit -m "refactor(docker): share the inspect mask loop"
```

### Task 4: DERP probe address forms

**Files:**
- Modify: `internal/modules/derp/node_probe.go`
- Create: `internal/modules/derp/node_probe_forms_test.go`
- Test: `internal/modules/derp/node_probe_private_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#43

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/modules/derp/node_probe_forms_test.go`:

```go
package derp

import (
	"net/netip"
	"testing"
)

func TestW11c04ProbeRefusesInternalAddressForms(t *testing.T) {
	for _, s := range []string{
		"0.1.2.3", "192.0.0.1", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255",
		"::1.2.3.4", "64:ff9b::7f00:1", "64:ff9b:1::1", "2001:0:1::1", "2002:7f00:1::1",
	} {
		if nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed, want it refused", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "203.0.113.9", "2606:4700::1"} {
		if !nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is refused, want it allowed", s)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c04 ./internal/modules/derp/`
Expected: FAIL naming `0.1.2.3 is allowed` and the other refused forms.

- [ ] **Step 3: Implement**

1. In `node_probe.go`, replace `nodeProbeShared` with a package-level `nodeProbeRefused` slice of `netip.Prefix` holding, in this order: `100.64.0.0/10`, `0.0.0.0/8`, `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`, `::/96`, `64:ff9b::/96`, `64:ff9b:1::/48`, `2001::/32`, `2002::/16`. `0.0.0.0` stays refused as unspecified. `nodeProbeAllowedAddr` refuses an address any of them contains, after `Unmap` as today, and its doc comment lists the new classes.
2. In `probeOnce` and `probeOnceLogged`, rename the named results `tls, stun` to `tlsResult, stunResult` so they stop shadowing the `crypto/tls` import.
3. In `node_probe_private_test.go`, the `tlsServer` helper builds with `httptest.NewUnstartedServer`, sets `srv.Config.ErrorLog = log.New(io.Discard, "", 0)` and calls `srv.StartTLS()`, so a probe's aborted handshake prints nothing to stderr.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/derp/ && go test -v -count=1 -run TestV1r16RealTLSFailuresUseTheVocabulary ./internal/modules/derp/ 2>&1 | rg -c 'TLS handshake error'`
Expected: PASS, every line `ok`, then `rg -c` prints nothing and exits 1 because no line matches.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/derp/node_probe.go internal/modules/derp/node_probe_forms_test.go internal/modules/derp/node_probe_private_test.go
git commit -m "fix(derp): refuse internal probe address forms"
```

### Task 5: Certificate reads beneath the directory

**Files:**
- Modify: `internal/platform/files.go`, `internal/modules/derp/deps.go`
- Create: `internal/platform/files_beneath_test.go`, `internal/modules/derp/cert_reader_test.go`
- Test: `internal/modules/derp/cert_files_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: Contracts "Certificate reader".

**Items:** 2026-11-06-v1-security-review#42, 2026-11-06-v1-security-review#54

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

Create `internal/platform/files_beneath_test.go`:

```go
package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW11c05ReadRegularFileBeneath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "disk", "certs")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.crt"), []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "disk"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "a.crt"), filepath.Join(real, "planted")); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(root, "linked", "certs")

	got, err := ReadRegularFileBeneath(through, "a.crt", 64)
	if err != nil || string(got) != "cert" {
		t.Fatalf("a file through a host link above the directory = %q, %v", got, err)
	}
	if _, err := ReadRegularFileBeneath(through, "planted", 64); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a link inside the directory = %v, want a refusal naming the link", err)
	}
	if _, err := ReadRegularFileBeneath(through, "missing", 64); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file = %v, want fs.ErrNotExist", err)
	}
	if _, err := ReadRegularFileBeneath(through, "../certs/a.crt", 64); err == nil {
		t.Error("a name climbing out of the directory was read")
	}
	if _, err := ReadRegularFileBeneath(through, "a.crt", 2); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a file over the limit = %v, want a size refusal", err)
	}
}
```

Create `internal/modules/derp/cert_reader_test.go`:

```go
package derp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW11c05CertFileReaderWiring(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "disk", "certs")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.crt"), []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "disk"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "a.crt"), filepath.Join(real, "planted")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "linked", "certs")
	read := certFileReader(dir)

	if got, err := read(dir + "/a.crt"); err != nil || string(got) != "cert" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := read(dir + "/planted"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a planted link = %v, want a refusal naming the link", err)
	}
	if _, err := read(dir + "/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file = %v, want fs.ErrNotExist to reach the caller", err)
	}
	if _, err := read(filepath.Join(root, "disk", "certs", "a.crt")); err == nil {
		t.Error("a path outside the directory was read")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c05 ./internal/platform/ ./internal/modules/derp/`
Expected: FAIL to build, naming `undefined: ReadRegularFileBeneath` and `undefined: certFileReader`.

- [ ] **Step 3: Implement**

1. Confirm first: `rg -n 'readCertFile\(' internal/modules/derp --glob '!*_test.go'` shows only paths built from `server.CertDir`. If `server_install.go` or `cert_renew.go` passes any other directory, stop and report it.
2. In `files.go`, move the part of `ReadRegularFile` after the open (the `Fstat`, regular-file check, size limit and read) into an unexported helper. Leave `ReadRegularFile` otherwise unchanged.
3. Add `ReadRegularFileBeneath` per Contracts. Open `dir` with `unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC`, wrapping a failure with `%w` so `fs.ErrNotExist` still reaches the caller, and close it on return. Open `name` from it with the existing `openBeneath(dirfd, name, flags, mode)` helper of `files.go` (it applies `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` and retries `EAGAIN`), with `unix.O_RDONLY | unix.O_NONBLOCK` and mode 0. `ELOOP` and `EXDEV` wrap with the text `has a symbolic link in its path` or `is outside its directory`. A doc comment gives the why: a link above `dir` is the host owner's, and only `dir` is writable by the service.
4. In `deps.go`, add `certFileReader` per Contracts: `strings.CutPrefix(name, dir+"/")`, an error `derp: %s is not under %s` when absent, then `platform.ReadRegularFileBeneath(dir, rel, certFileLimit)`. The `readCertFile` default becomes `certFileReader(server.CertDir)`.
5. In `cert_files_test.go::TestV1r15ProductionCertReadRefusesPlantedFiles`, `read := productionDP().d.readCertFile` reads temporary paths, which the production reader now refuses. Replace it by `read := certFileReader(<the test's temporary directory holding crt>)`, keep the table, and add an assertion that pins the production wiring without touching the disk: `productionDP().d.readCertFile("/nonexistent/a.crt")` returns an error containing `server.CertDir`. Update the comment in `cert_files_test.go` that says the production read is `platform.ReadRegularFile`, and the `readCertFile` field comment in `deps.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/platform/ ./internal/modules/derp/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/files.go internal/platform/files_beneath_test.go internal/modules/derp/deps.go internal/modules/derp/cert_reader_test.go internal/modules/derp/cert_files_test.go
git commit -m "fix(derp): allow host links above the cert dir"
```

### Task 6: Tailscale repository file rollback

**Files:**
- Modify: `internal/modules/tailscale/package_pin.go`
- Create: `internal/modules/tailscale/package_pin_repo_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#52

**Implementer:** dforge:impl-sonnet-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/package_pin_repo_test.go`:

```go
package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

type w11c06Files struct {
	core.FileWriter
	failOn  string
	removed *[]string
}

func (f w11c06Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if path == f.failOn {
		return errors.New("disk full")
	}
	return f.FileWriter.WriteFile(ctx, module, path, data, mode)
}

func (f w11c06Files) Remove(ctx context.Context, module, path string) error {
	*f.removed = append(*f.removed, path)
	return f.FileWriter.Remove(ctx, module, path)
}

func TestW11c06FailedRepoStepRemovesWhatItWrote(t *testing.T) {
	s := pnNewSetup(t, true)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, failOn: sourcesPath, removed: &removed}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if err == nil {
		t.Fatal("the pin succeeded with a failing repository write")
	}
	if want := []string{keyringPath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c06 ./internal/modules/tailscale/`
Expected: FAIL with `removed [], want ["<the keyring path>"]`.

- [ ] **Step 3: Implement**

1. In `package_pin.go`, the `ensureRepo` failure branch keeps `written` from the call. After the rollback attempt, on the rollback context, it calls `noJournal.Files.Remove(rctx, ID, path)` for each written path, joining a removal error into the returned error.
2. Update the existing why comment above `noJournal` to say the files are removed when this step fails and stay owned otherwise.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/tailscale/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_pin.go internal/modules/tailscale/package_pin_repo_test.go
git commit -m "fix(tailscale): remove repo files of a failed step"
```

### Task 7: Stored bindings and hard-linked backups

**Files:**
- Modify: `internal/controller/listen/runtime_store.go`, `internal/controller/backup/stream.go`
- Test: `internal/controller/listen/runtime_test.go`, `internal/controller/backup/stream_descriptor_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#45, 2026-11-06-v1-security-review#49

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing test and extend the backup test**

Append to `internal/controller/listen/runtime_test.go`:

```go
func TestW11c07StoredBindingIsValidated(t *testing.T) {
	for _, bad := range []string{"203.0.113.5:0", "[fe80::1%dp-panel]:443"} {
		f := newRtFixture(t)
		f.start(t)
		spec := rtSpec
		spec.PublicBinding = netip.MustParseAddrPort("203.0.113.5:443")
		id, _ := f.open(t, spec)
		if _, err := f.db.Writer().ExecContext(context.Background(), `UPDATE core_listeners SET public_binding = ? WHERE id = ?`, bad, id); err != nil {
			t.Fatal(err)
		}
		if _, err := getListener(context.Background(), f.db, id); err == nil || !strings.Contains(err.Error(), "bad public binding") {
			t.Errorf("stored binding %s: getListener = %v, want a bad public binding error", bad, err)
		}
	}
}
```

In the backup test, add this row to the table:

```go
		{"a hard link", func(path string) error {
			other := path + ".held"
			if err := os.WriteFile(other, []byte("held"), 0o600); err != nil {
				return err
			}
			return os.Link(other, path)
		}},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run 'TestW11c07|TestV1r22BackupRefusesACopySwapped' ./internal/controller/listen/ ./internal/controller/backup/`
Expected: FAIL: `getListener` returns no error for both stored bindings, and the hard-link row answers status 200.

- [ ] **Step 3: Implement**

1. In `runtime_store.go`, `scanListener` refuses a parsed public binding with port 0 or a zoned address, with the same `has a bad public binding` error it returns for an unparsable one. It does not run the whole `validListenerSpec`. A bad row fails the read as an unparsable row does today; every stored row passed `validListenerSpec` at `Open` (`runtime.go`), which already refuses both forms.
2. Leave `runtime.go` unchanged.
3. In `stream.go`, after the existing `Fstat` regular-file check on the copy, refuse a file whose link count is not 1 with `backup: the temporary copy %s has more than one name`, handled as the regular-file refusal is.
4. In `stream_descriptor_test.go`, replace the `//nolint:errorlint` condition by two branches: `if err != nil { lookErr = fmt.Errorf("look for the copy: %w", err); return }` and `if len(names) != 1 { lookErr = fmt.Errorf("found %v", names); return }`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -timeout 20m -count=1 ./internal/controller/listen/ ./internal/controller/backup/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/runtime_store.go internal/controller/listen/runtime_test.go internal/controller/backup/stream.go internal/controller/backup/stream_descriptor_test.go
git commit -m "fix(controller): validate stored bindings and copies"
```

### Task 8: Auth comments and small tests

**Files:**
- Modify: `internal/controller/auth/ratelimit.go`, `internal/controller/auth/login.go`, `internal/controller/auth/ratelimit_test.go`, `internal/controller/api/extensions_test.go`
- Create: `internal/controller/auth/limit_source_test.go`
- Test: `internal/modules/derp/composite_remove_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#31, 2026-11-06-v1-security-review#35, 2026-11-06-v1-security-review#44

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 0 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/limit_source_test.go`:

```go
package auth

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestW11c08MappedProxiedClientGroupsWithItsIPv4Form(t *testing.T) {
	ctx := core.WithProxied(context.Background(), core.Proxied{Client: netip.MustParseAddr("::ffff:192.0.2.7")})
	if got, want := sourceKey(limitSource(ctx, "172.31.255.1")), sourceKey("192.0.2.7"); got != want {
		t.Errorf("source key %q, want %q", got, want)
	}
}
```

In `internal/modules/derp/composite_remove_test.go`, add after `TestCMOutput`:

```go
func TestW11c08OutputRefusesUndecodableEdits(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().serverRemove()
	steps := map[string]json.RawMessage{
		"uninstall": json.RawMessage(`{"removed":[]}`),
		"edits":     json.RawMessage(`not json`),
		"region":    json.RawMessage(`{"etag":"x"}`),
	}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(`{"hostId":"h7"}`), steps); err == nil {
		t.Fatal("Output accepted an edits output it cannot decode")
	}
}
```

- [ ] **Step 2: Run test to verify the outcome**

Run: `go test -count=1 -run TestW11c08 ./internal/controller/auth/ ./internal/modules/derp/`
Expected: PASS: both pin behaviour already present (`sourceKey` unmaps).

- [ ] **Step 3: Implement**

1. In `ratelimit.go`, the `blocked` doc comment opens `blocked returns how long the pair of username and source, or the source alone, stays locked, the longer of the two, or 0 when the attempt may be evaluated.`
2. In `login.go`, the sentence of the `Login` doc comment that says username or source reads `A lock on the pair of username and source, or on the source alone, refuses the attempt.`
3. In `ratelimit_test.go`, the comments that name the keys `u:alice` and `u:bob` name them `u:alice@10.0.0.1` and `u:bob@10.0.0.1`, as `pairKey` writes them.
4. In `extensions_test.go`, the `xrEvents` loop runs `for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {`, so a test that timed out or failed early does not leave the handler writing forever; `failed` is sent only on a write error, as today.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/controller/auth/ ./internal/controller/api/ ./internal/modules/derp/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth internal/controller/api/extensions_test.go internal/modules/derp/composite_remove_test.go
git commit -m "test(auth): cover mapped sources and fix comments"
```

### Task 9: NPM expose alerts

**Files:**
- Modify: `web/src/modules/npm/container-actions.tsx`
- Test: `web/src/modules/npm/container-actions.test.tsx`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#32

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Append to `web/src/modules/npm/container-actions.test.tsx`, adding `act` to its `@testing-library/react` import if absent:

```tsx
const caDown = (detail: string) => ({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail } })

test("shows one alert when the status and the container both fail to refetch", async () => {
  let failing = false
  const { client } = caRender({
    [CA_STATUS]: () => (failing ? caDown("the NPM module is unavailable") : { body: { output: caStatus() } }),
    [CA_INSPECT]: () => (failing ? caDown("the Docker daemon is not reachable") : { body: { output: caDetail() } }),
  })
  await caForm()
  failing = true
  await act(async () => {
    await client.invalidateQueries()
  })
  expect(screen.getAllByRole("alert").map((a) => a.textContent)).toEqual(["the NPM module is unavailable"])
})

test("shows a failed status refetch when the last status said not deployed", async () => {
  let failing = false
  const { client } = caRender({
    [CA_STATUS]: () =>
      failing
        ? caDown("the NPM module is unavailable")
        : { body: { output: caStatus({ deployed: false, containerId: "", running: false, apiReady: false, public: "no", adminEmail: "" }) } },
  })
  await screen.findByText("Nginx Proxy Manager is not deployed on this host. Deploy it first.")
  failing = true
  await act(async () => {
    await client.invalidateQueries()
  })
  expect(screen.getByRole("alert").textContent).toBe("the NPM module is unavailable")
  expect(screen.getByText("Nginx Proxy Manager is not deployed on this host. Deploy it first.")).toBeTruthy()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && timeout 600 pnpm install --frozen-lockfile && timeout 300 pnpm vitest run src/modules/npm/container-actions.test.tsx`
Expected: FAIL: the first new test finds two alerts, the second finds no alert.

- [ ] **Step 3: Implement**

1. `ExposeAction`, when `!status.data.deployed`, renders the `ReadFailure` of a failed status refetch above the not-deployed line.
2. `ExposeLoader` takes a prop `showFailure: boolean`. It gates only the refetch `ReadFailure` above the form. `ExposeAction` passes `showFailure={!status.isError}`, so at most one alert shows.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && timeout 300 pnpm vitest run src/modules/npm/ && timeout 300 pnpm tsc --noEmit`
Expected: PASS, no type errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/npm/container-actions.tsx web/src/modules/npm/container-actions.test.tsx
git commit -m "fix(web): show one NPM expose read failure"
```

### Task 10: Firewall messages and guard cost

**Files:**
- Modify: `internal/modules/firewall/predict/predict.go`, `internal/modules/firewall/target.go`, `internal/modules/firewall/docker_effects.go`, `internal/modules/firewall/guard/cells.go`, `internal/platform/netstat/routes.go`, `internal/modules/firewall/guard/internet_cells_test.go`, `internal/modules/firewall/e2e_engine_test.go`, `internal/modules/firewall/handler_test.go`
- Create: `internal/modules/firewall/guard/cells_cost_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#55, 2026-11-06-v1-security-review#57, 2026-11-06-v1-security-review#58, 2026-10-21-v1-completion#450, 2026-10-21-v1-completion#453, 2026-10-21-v1-completion#454, 2026-10-21-v1-completion#460, 2026-10-21-v1-completion#466

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/modules/firewall/guard/cells_cost_test.go`:

```go
package guard

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func TestW11c10FlowRulesCountTheBeforeStateOfAWildSource(t *testing.T) {
	parse := func(s string) *netfilter.Ruleset {
		t.Helper()
		rs, err := netfilter.ParseRestore(strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	now := parse("*filter\n:INPUT DROP [0:0]\n-A INPUT -p tcp -j ACCEPT\nCOMMIT\n")
	before := parse("*filter\n:INPUT DROP [0:0]\n-A INPUT -p tcp -j ACCEPT\n-A INPUT -p udp -j ACCEPT\nCOMMIT\n")
	if got := flowRules(flow{wild: wildSrc}, now, before); got != 3 {
		t.Errorf("a wild-source flow counts %d rules, want 3", got)
	}
	if got := flowRules(flow{}, now, before); got != 1 {
		t.Errorf("a flow without a wild source counts %d rules, want 1", got)
	}
	if got := flowRules(flow{wild: wildSrc}, now, nil); got != 1 {
		t.Errorf("a wild-source flow without a before-state counts %d rules, want 1", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c10 ./internal/modules/firewall/guard/`
Expected: FAIL to build, naming `undefined: flowRules`.

- [ ] **Step 3: Implement**

1. In `guard/cells.go`, add `flowRules(fl flow, rs, before *netfilter.Ruleset) int`: `ruleCount(rs)`, plus `ruleCount(before)` when `fl.wild == wildSrc`. `cellFlows` gives it the family's before-state ruleset (nil when `before` is nil) and uses it in place of `ruleCount(rs)` for `rules`. Also add `TestW11c10CellFlowsCountTheBeforeState` to `cells_cost_test.go`, building the router as `cells_test.go` does near its direct `cellFlows` call: a now-state of 2 rules and a before-state of 2300 `-A INPUT -s 10.<a>.<b>.1/32 -j DROP` rules made with a `fmt.Sprintf` loop. Assert the result is one flow with `unknown != ""` for a wild-source flow given that before-state, and that the same flow with a nil before-state is not unknown.
2. `predict/predict.go`: the missing-file message ends `, so the rules cannot be predicted` in place of `, so UFW cannot start`. Update any test that quotes the old text.
3. `target.go`: in `selfTransition`, whose doc comment says it covers a transition no Darkpanel command runs, the wrap `firewall: predict ufw %s: %w` becomes `firewall: predict UFW's own %s transition: %w`. Leave the other `predict ufw` wraps alone.
4. `docker_effects.go`: `loadBlockChains` takes the `fwstate.Loaded` that `loadChains` already read in place of `variant`, using `st.Variant`, and no longer calls `d.readLoaded`.
5. `netstat/routes.go`: the `Proto` field comment says the kernel answers a lookup only for TCP, UDP and, per family, ICMP or ICMPv6, and refuses any other with `EINVAL`.
6. Comments: `guard/internet_cells_test.go` `v1r21TwoAddrs` says it is shared by the wildcard and policy tests, or whatever its users are, as `rg -n v1r21TwoAddrs` shows; `e2e_engine_test.go`'s stale comments say `defaults.set` where they name the old operation; `handler_test.go` `TestMutationLifecycle`'s comment drops `reload of an active UFW` and says `the live apply`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/firewall/... ./internal/platform/netstat/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall internal/platform/netstat/routes.go
git commit -m "fix(firewall): count the before-state in the cost"
```

### Task 11: Firewall and netstat tests

**Files:**
- Create: `internal/modules/firewall/dockerfw/restore_input_refusals_test.go`, `internal/modules/firewall/docker_load_failure_test.go`
- Test: `internal/platform/netstat/rules_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#56, 2026-10-21-v1-completion#451, 2026-10-21-v1-completion#457, 2026-10-21-v1-completion#458, 2026-10-21-v1-completion#464

**Implementer:** dforge:impl-sonnet-low
**Executor:** agy gemini-3.8-flash / medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Write the failing test**

Create `internal/modules/firewall/dockerfw/restore_input_refusals_test.go`:

```go
package dockerfw

import (
	"strings"
	"testing"
)

func TestW11c11ChainsInputWithoutALoadedRuleset(t *testing.T) {
	in := string(ChainsInput([]byte("*filter\n-A darkpanel-docker-bindings -j DROP\nCOMMIT\n"), nil))
	if strings.Contains(in, "-X ") {
		t.Errorf("input deletes a chain with nothing loaded:\n%s", in)
	}
	if !strings.Contains(in, "-A darkpanel-docker-bindings -j DROP") {
		t.Errorf("input lost the block's rule:\n%s", in)
	}
}
```

Create `internal/modules/firewall/docker_load_failure_test.go`:

```go
package firewall

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

type w11c11FailRestore struct {
	*deeHost
	program string
	failed  bool
}

func (r *w11c11FailRestore) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if !r.failed && strings.HasSuffix(cmd.Path, r.program) {
		r.failed = true
		return core.CmdResult{ExitCode: 1, Stderr: []byte("restore: line 2 failed\n")}, nil
	}
	return r.deeHost.Run(ctx, cmd)
}

func TestW11c11FailedRestoreRevertsTheEnable(t *testing.T) {
	for _, program := range []string{"/iptables-nft-restore", "/ip6tables-nft-restore"} {
		t.Run(program, func(t *testing.T) {
			h := deeNewHost(t)
			exec := &w11c11FailRestore{deeHost: h, program: program}
			_, cv, err := deePlanApply(t, deeEngineWith(t, h, exec), "firewall.docker.enable", `{}`)
			if !exec.failed || cv.State != core.StateReverted {
				t.Fatalf("enable = %+v, %v (restore failed: %v); want reverted", cv, err, exec.failed)
			}
			if owned := deeOwnedChains(h); len(owned) != 0 {
				t.Errorf("the revert left the chains %v loaded", owned)
			}
		})
	}
}

func TestW11c11BlockInputsSkipIPv6WhenOff(t *testing.T) {
	h := deeNewHost(t)
	deeConfirm(t, deeEngine(t, h), "firewall.docker.enable", `{}`)
	cfg := h.config()
	cfg.IPv6 = false
	inputs, err := blockChainInputs(cfg, h.loaded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inputs[fwstate.V4]; !ok {
		t.Error("no IPv4 input")
	}
	if _, ok := inputs[fwstate.V6]; ok {
		t.Error("an IPv6 input with IPv6 off")
	}
}

func TestW11c11ByInversesRevertKeepsAForeignDockerUserRule(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	_, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("allow = %+v, %v; want pending", cv, err)
	}
	if res, err := h.Run(ctx, core.Cmd{Path: ufwPath, Args: strings.Fields("allow in proto tcp from any to any port 8098")}); err != nil || res.ExitCode != 0 {
		t.Fatalf("the manual ufw allow = %+v, %v", res, err)
	}
	h.addForeign(t, deeForeign)
	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if got := deeTexts(h, fwstate.V4, "DOCKER-USER"); !slices.Contains(got, deeForeign) {
		t.Errorf("DOCKER-USER = %q, want the foreign rule %q kept", got, deeForeign)
	}
}
```

Append to `internal/platform/netstat/rules_test.go`, with the imports `errors` and `golang.org/x/sys/unix` if absent:

```go
func TestW11c11RulesFromErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := rulesFrom(IPv4, func(uint16, uint16, []byte) ([][]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("a failed dump = %v, want it wrapped", err)
	}
	if _, err := rulesFrom(IPv4, func(uint16, uint16, []byte) ([][]byte, error) { return [][]byte{{1, 2}}, nil }); err == nil {
		t.Error("a malformed message was accepted")
	}
}
```

- [ ] **Step 2: Run test to verify the outcome**

Run: `go test -count=1 -run 'TestW11c11' ./internal/modules/firewall/... ./internal/platform/netstat/`
Expected: PASS: these tests pin behaviour already present. A failure is a finding: report it with its message and stop.

- [ ] **Step 3: Implement**

No production change. `TestW11a04ApplyChainsRefusals` already covers the refusals that row 451 names.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/modules/firewall/... ./internal/platform/netstat/`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/dockerfw/restore_input_refusals_test.go internal/modules/firewall/docker_load_failure_test.go internal/platform/netstat/rules_test.go
git commit -m "test(firewall): cover chain refusals and failed loads"
```

### Task 12: Harness live-apply cleanup

**Files:**
- Modify: `test/harness/m3_firewall_live_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks use.

**Items:** 2026-10-21-v1-completion#447, 2026-10-21-v1-completion#448, 2026-10-21-v1-completion#449

**Implementer:** dforge:impl-sonnet-low
**Executor:** agy gemini-3.8-flash / medium
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

- [ ] **Step 1: No unit test applies**

This task edits a harness scenario that runs only on KVM runners. Step 3's vet and lint catch a build fault, and the harness gate runs the scenario.

- [ ] **Step 2: Implement**

In `m3LiveApply`:
1. The `t.Cleanup` command starts with `sudo touch /run/w11a-stop; sleep 1; `, so a failed step cannot leave the policy poller running.
2. Delete the `m3WaitStatus(ctx, t, f.agent, ...)` line at the start of the function.
3. The mismatch message uses `"live %s and a reload disagree on %s chain %s"` with `cmd` as the first argument, because `cmd` already holds `sudo ufw`.

- [ ] **Step 3: Verify**

Run: `go vet -tags harness ./test/... && go test -tags harness -count=1 -run Unit ./test/harness/... && golangci-lint run --allow-serial-runners --build-tags harness ./test/harness/...`
Expected: no vet output, every line `ok`, `0 issues.`. If the linter names `m3Healthy` or `m3WaitStatus` as unused, remove only what it names.

- [ ] **Step 4: Commit**

```bash
git add test/harness/m3_firewall_live_test.go
git commit -m "test(harness): stop the poller when a step fails"
```

### Task 13: Spec, threat model and test timeout

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md`, `docs/security/threat-model.md`, `docs/user/modules/firewall.md`, `docs/dforge/notes/v1-w3-final-review-followups.md`, `Makefile`
- Test: `internal/docs/threat_model_test.go`, `internal/docs/threat_model_state_test.go`

**Interfaces:**
- Consumes: the behaviour of Tasks 1, 4, 5 and 6.
- Produces: nothing other tasks use.

**Items:** 2026-11-06-v1-security-review#30, 2026-11-06-v1-security-review#78, 2026-10-21-v1-completion#465

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 2 - spec 1 - coupling 0 - risk 0 = 3

- [ ] **Step 1: Extend the test `internal/docs/threat_model_test.go`**

Add a new test function at the end of the file:

```go
func TestW11c13ThreatModelRecordsTheRulings(t *testing.T) {
	text := dcRead(t, w10tmPage)
	for _, want := range []string{
		"restores a custom location it cannot check",
		"symbolic links only beneath",
		"The probe refuses the special-purpose forms",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the threat model does not say %q", want)
		}
	}
	if strings.Contains(text, "no symlink anywhere on the path") {
		t.Error("the threat model still says no symlink anywhere on the path")
	}
	if strings.Contains(text, "are not refused. Only a caller") {
		t.Error("the threat model still says the special-purpose forms are not refused")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestW11c13 ./internal/docs/`
Expected: FAIL naming the three phrases and the stale sentence.

- [ ] **Step 3: Implement**

1. Spec, after the "Plan rulings (wave 11b)" bullets, add a bullet `**Plan rulings (wave 11c).**` holding these six, as sub-bullets, verbatim:
   - W11-R13 custom locations (rows 26, 36, 37): the undo of `npm.proxyhost.update` or `remove` keeps W10b-R3's check on the host's forward host. For a custom location it still refuses a reserved upstream, but a location whose forward host is empty, holds an nginx variable, or is a name with no answer is restored as it was: Darkpanel never authors a custom location, and an undo only puts back what NPM already held. An update keeps operator locations without checking them, for the same reason. Owner did not object when offered, 2026-11-10.
   - W11-R14 CreateAdmin (row 39): only a 4xx other than 400 is a conflict. A 400 rejects the payload, so it fails the deploy without removing the instance.
   - W11-R15 certificate reads (rows 42, 54): the DERP certificate reads refuse symbolic links only beneath `server.CertDir`. Links above that directory are the host owner's and cannot be planted by the service account. `internal/modules/derp/deps.go:125` is the only caller of `platform.ReadRegularFile`.
   - W11-R16 probe addresses (row 43): `nodeProbeAllowedAddr` also refuses the special-purpose forms that reach internal networks (`0.0.0.0/8`, `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`, `::/96`, `64:ff9b::/96`, `64:ff9b:1::/48`, `2001::/32`, `2002::/16`). The documentation ranges stay allowed; 55 lines of `node_probe*_test.go` use them. An allowlist stays out of scope, as W10b-R11 holds it.
   - W11-R17 repository files (row 52): a failed `ensureRepo` step removes the files that attempt wrote. A later failed pin step leaves them, because uninstall owns them (`internal/modules/tailscale/package_pin.go`).
   - W11-R18 closures: rows closed with no code carry their reason in the register, set when this plan was written (security review 33, 34, 79; v1-completion 452, 455, 459, 461; deferred 456, 462, 463). Row-part closures: 31's clock pacing, 38's `4e2` parse, 41's journal digests and nil Sealer (every production `core.Env` sets `Sealer`: `internal/controller/server/services.go`, `internal/agent/runtime/runtime.go`, `internal/install/uninstall.go`), 43's reason text, 44's per-pair lock (threat model), 36's DNS rebinding (W10b-R3), 54's label render (no production caller of `NetworkCreated` or `VolumeCreated`) and its A26 wording (a closed plan's assumption).
   Extend W11-R1's list with: security-review rows 55, 56, 57, 58 and 78, and v1-completion rows 447-451, 453, 454, 457, 458, 460 and 464-466. In the wave 10 row of "Parallel decomposition", remove `` `CHANGELOG.md`, ``. In the `ListenerSpec` struct of section 1, align the `PublicBinding` field with the fields above it.
2. `docs/security/threat-model.md`. The page never names a plan, wave, task or register row, and each bullet under Mitigations carries a backticked existing repository path. Under `### Exposure`, `#### Mitigations` add: `The undo of a proxy host restores a custom location it cannot check, and still refuses one it can show is reserved (`+"`internal/modules/npm/proxyhost_edit.go`"+`).` Under the DERP area's Mitigations add: `Certificate reads refuse symbolic links only beneath the certificate directory, so a link the host owner placed above it keeps working (`+"`internal/modules/derp/deps.go`"+`).` Replace the residual-risk bullet that starts `The probe refuses the address classes named above and no others` by: `The probe refuses the special-purpose forms that reach internal networks, among them `+"`64:ff9b::/96`"+`, `+"`0.0.0.0/8`"+`, `+"`240.0.0.0/4`"+` and `+"`198.18.0.0/15`"+` (`+"`internal/modules/derp/node_probe.go`"+`). Only a caller with the `+"`operate`"+` scope can choose the target.` In `### Exposure`, change the sentence that says the undo "checks every forward host it writes back the same way" to say it checks the host's forward host that way and each custom location it can check. In `### DERP host`, change "no symlink anywhere on the path" to "no symlink beneath the certificate directory", and update the same phrase at `internal/docs/threat_model_state_test.go:97`. Each bullet is plain Markdown on the page, with the backticks around paths kept and no string concatenation. The replacement probe bullet also says documentation ranges stay allowed and there is no allowlist. Keep the Tailscale repository-file sentence.
3. `docs/user/modules/firewall.md`: rewrap the 85-column sentence of "Turning it on and off" at about 80.
4. `docs/dforge/notes/v1-w3-final-review-followups.md`: replace the invalid byte with an em dash, using `python3 -c` to rewrite the single `\xe2\n` sequence as `—\n`. Verify with `python3 -c "open(p,'rb').read().decode()"`. This closes the third item of security-review row 40.
5. `Makefile`, `test-go:` runs `go test -timeout 20m ./...`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./internal/docs/ && timeout 60 make -n test-go`
Expected: PASS, and `make -n` prints `go test -timeout 20m ./...`.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md docs/security/threat-model.md docs/user/modules/firewall.md docs/dforge/notes/v1-w3-final-review-followups.md Makefile internal/docs/threat_model_test.go internal/docs/threat_model_state_test.go
git commit -m "docs(security): record the wave 11c rulings"
```
