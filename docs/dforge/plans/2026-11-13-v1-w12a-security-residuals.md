# v1 wave 12a security residuals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the residuals the 2026-11-12 security re-review (`docs/dforge/notes/v1-security-rereview.md`) left in security register rows 1, 6, 10, 12, 14 and 15, so that every high finding can be re-reviewed closed.

**Architecture:** Each fix is one slice with its own regression test. Tasks 2 and 3 make NPM's network attach, the undo of a detach and the upgrade refuse an address a proxy host already forwards to (row 1). Task 4 adds the concrete client of a request through an NPM exposure to the lockout guard's public leg (row 6). Tasks 5 and 6 keep NPM's answer bytes and Compose's resolved values out of errors (rows 10 and 12). Tasks 7 and 8 remove an NPM instance whose first-admin creation was cut off and keep the operator's "/" location configuration (rows 14 and 15). Task 1 writes the rulings W12a-R1 to W12a-R6 into the spec first, and Task 9 brings the threat model to the re-reviewed state last.

**Tech Stack:** Go 1.27.4, Markdown spec, threat model and user guide.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 7 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w12a-tmp/integ && export TMPDIR=/root/.cache/w12a-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/agent/daemon/ && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

## Global Constraints

- Scope is security register rows 1, 6, 10, 12, 14 and 15 (`docs/dforge/registers/2026-11-06-v1-security-review.md`), as the rulings under Assumptions narrow them. Only Task 1 edits the spec, and only Task 9 edits `docs/security/threat-model.md`.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. The npm, docker and firewall tests run on the fakes of their packages (`internal/modules/npm/testhost_test.go`, `internal/modules/docker/compose`, the guard package's fixtures).
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w12a-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends.
- No task runs `go clean -cache` or `go clean -testcache`: the build cache is shared with other sessions on this host.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1` on the task's packages (every line `ok`); `golangci-lint run --allow-serial-runners` on the task's packages (`0 issues.`). The npm module's package tree takes 5 to 6 minutes on a loaded host; its commands carry `timeout 900`.
- Generated files: no task changes a wire type, an action schema or an error code, so `schemas/**` and `docs/api/**` stay unchanged and `make generate` has nothing to regenerate.
- TDD: write the test, run it and see the expected RED, then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A row that pins behaviour already present passes before the change; the step says so.
- Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- Existing tests whose expected messages or outcomes a task changes are updated in the same task, to exactly the new values the task states; no other expectation changes.
- Test names and test helpers this plan adds carry the prefix `W12a`/`w12a`, except rows added to an existing table and existing tests a task extends.
- The plan runs on branch `v1-w12a-security-residuals` in the base repository, with no worktree.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name or keep the form of their file; a changed refusal keeps its error code.
- Documentation pages (`docs/security/threat-model.md`, `docs/user/**`) never name a plan, wave, task, ruling id (`W12a-R1`) or register row: `internal/docs` fails on `W[0-9]+-R[0-9]+`, `wave N`, `task N` and `rows N`. Every Mitigations bullet of the threat model names an existing repository path in backticks.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) does not apply to the branch: its diff touches none of `test/harness/`, `.github/workflows/harness.yml`, `install.sh.in`, `internal/install/` or `internal/update/`. Push the branch and run gate 6. No task creates or pushes a tag or runs `release.yml`.
- Re-review: rows 1 and 6 are high. Their acceptance asks for a re-review closed by both panel reviewers (Fable and Codex, `docs/dforge/notes/v1-security-review-brief.md`); rows 10, 12, 14 and 15 are closed by the final whole-branch review. No task runs the panel; the finish does, before the rows are set `done`.

## Contracts

**K1. NPM forward guard (Task 2, consumed by Task 3), `internal/modules/npm/network_claims.go`.**
- `func (n nm) forwardGuard(ctx context.Context, env core.Env, network dockerapi.Network, static netip.Addr) error`. `network` is the Engine's inspect of the network NPM will join; its `Containers` entries count as holders, and a caller removes NPM's own entry when NPM releases its addresses first, as the upgrade does. `static` is the IPv4 address NPM will ask for, or the zero `netip.Addr` when the Engine picks one. Every failure is `core.Definite`.
- Error code `npm.reserved` (`errcode.NPMReserved`, HTTP 409) from `npm.network.attach` at plan and at apply, from the undo of `npm.network.detach`, and from `npm.instance.upgrade` at plan and at apply. The detail is one of `<where> forwards to <addr>, an address darkpanel-npm could take on network <network>` and `<where> forwards to <name>, which resolves to <addr>, an address darkpanel-npm could take on network <network>`, where `<where>` is `proxy host <id>` or `location <path> of proxy host <id>`.
- Test helpers in `internal/modules/npm/network_claims_test.go` that Task 3's tests call: `w12aSeed(h *thHost, host string, port int, locations ...npmapi.Location)`, and the existing `nwRefused`.

**K2. NPM client flow (Task 4), `internal/modules/firewall/guard/flows.go`.** Flow label `exposure <ExposureID> client` in the `firewall.lockout` refusal message, for example `the change would cut exposure exp1 client from 10.0.0.50 to 10.0.0.1:443 on eth1: drop (...)`. Task 9's threat-model text describes it.

**K3. NPM client failure texts (Task 5), `internal/modules/npm/npmapi/client.go`.** `npmapi: <METHOD> <path>: <reason>` with reason one of `context canceled`, `context deadline exceeded`, `connection refused`, `no usable answer`; `npmapi: read <path> answer: <reason>` (same reasons); `npmapi: decode <path> answer: not the expected JSON`. `errors.Is` still finds `context.Canceled` and `context.DeadlineExceeded`.

**K4. Compose refusal details (Task 6), `internal/modules/docker/compose/validate.go`.** `service <s>: container_name names a container of module <id>`; `service <s> refers to a container of module <id>`; `<network|volume> <key> names a <network|volume> of module <id>`; `service <s> builds the image its image member names, which is not present locally; Compose is run with --no-build`. A build service without an `image` member keeps `service <s> builds image <project>-<s>, which is not present locally; Compose is run with --no-build`.

**K5. NPM bootstrap (Task 7), `internal/modules/npm/instance_deploy.go`.** `deployHandler.bootstrap` returns `discard` true for every failure before NPM accepted the creation of the first administrator except a 400 answer to it: an administrator NPM reported first, the apply cancelled while it waits or before NPM answered the creation, the 120-second wait timing out, no answer to the creation, and an error status other than 400. Details of the cancelled cases: `the deploy was cancelled while it waited for Nginx Proxy Manager to answer` and `the deploy was cancelled before Nginx Proxy Manager answered the creation of the administrator`, each followed by `discardDeploy`'s `; the deploy removed what it created` or `; the deploy could not remove what it created: ...`. The error chain keeps `context.Canceled`.

**K6. NPM "/" location (Task 8).** `func OperatorConfig(advancedConfig string) string` in `internal/modules/npm/proxy/location.go` returns the lines of a "/" location's advanced configuration that `proxy.Location` does not write. Plan diff line of `npm.proxyhost.update`: `{Op: " ", Text: "location /: keeps the advanced configuration written in NPM"}`.

## Assumptions (evidence)

Rulings (owner, 2026-11-13; Task 1 writes them into the spec as W12a-R1 to W12a-R6):

- **Row 1 (W12a-R1).** One guard fixes both the attach path and the upgrade path; pinning NPM's address is not chosen. The undo of a detach is the same reconnect with an Engine-picked address and is guarded too.
- **Row 1, upgrade (W12a-R1).** An upgrade now needs NPM's API to read the proxy hosts and is refused with `npm.unhealthy` when it cannot; accepted, because letting it proceed would reopen the hole for anyone able to stop NPM. Recorded as residual risks in the threat model (Task 9), not fixed: the rollback or undo of an upgrade recreates the old container without the check (a refusing rollback would leave NPM down); NPM's dynamic address may drift on a container or host restart (unknown until the harness settles it); an address another container held at check time can be freed before NPM connects.
- **Row 6 (W12a-R2).** The client flow is not compared with the before-change rules, as for direct panel clients. Tunnel exposures need no change: the browser reaches only Cloudflare's edge.
- **Row 10 (W12a-R3).** A failed request keeps its method, path and a fixed reason only; `errors.Is` on context errors keeps working.
- **Row 12 (W12a-R4).** Refusals name service, key or member; Compose's default `<project>-<service>` image name may still be quoted. Compose validation's raw Docker daemon errors (`engineError`) are not traced here: security register row 131 (open, medium, unverified) records them, unscheduled.
- **Row 14 (W12a-R5).** W11-R14's 400 branch stays. The instance is removed on every failure before NPM accepts the admin creation (cancellation, no answer, a 5xx, the health wait timing out), because Darkpanel cannot then prove it owns the only administrator. The drafter's narrower rule (cancellation and no answer only) is replaced: Task 7's 5xx row now expects a removal and a wait-timeout row is added. The race itself, a local user creating the first admin through `127.0.0.1:81` during a deploy, stays an accepted residual risk in the threat model.
- **Row 15 (W12a-R6).** Only a "kept" diff line can occur, since the update's input has no field for the operator's lines; no `-`/`+` pair.

Row 1, attach and upgrade (Tasks 2 and 3):

- Attach takes a caller-chosen IPv4 address and connects with it; its check never reads proxy hosts: `internal/modules/npm/network_claims.go:215,258-317,360-366`. Attach needs only `operate`: no `MinScope` at `network_claims.go:26-35`, and `internal/core/scope.go:10-11`.
- Undo of a change needs only that change's own scope, so `operate` can undo a detach: `internal/controller/api/changes.go:97`, `changes_test.go:145`. The undo reconnects with a dynamic address: `network_claims.go:752-756`. The undo window closes when a later change takes the npm lock (`internal/controller/api/routes.go:44`).
- `proxy.LiteralAddr` reads a host as nginx does and returns a plain address: `internal/modules/npm/proxy/upstream.go:63-67`. Name resolution follows `checkForward`: `internal/modules/npm/proxyhost_add.go:300-356` (`dockerAnswers`) and `:358-398`.
- `npm.reserved` exists with HTTP 409: `internal/core/errcode/errcode.go:109,221`.
- `NetworkContainer` carries `IPv4Address` and `IPv6Address` as prefixes and the live Engine fills them: `internal/platform/dockerapi/networks.go:45-51`, `live_test.go:137`. The npm fake did not fill them (`internal/modules/npm/testhost_test.go:628-636`), hence Task 2's step 1a; only `testhost_check_test.go:232` reads the fake's `Containers`, and only by `Name`.
- The composites attach before adding a proxy host: `internal/modules/npm/composite_deploy_expose.go:144-170`. The panel exposure attaches at a fixed address and forwards to the gateway, which the guard exempts: `internal/modules/panel/expose_add.go:122,146`.
- The attach action gains no wire field; `docs/api/npm.md` lists no error codes per action (read 2026-11-13).
- Upgrade: agent-side compensable with no `MinScope`, so `operate` (`internal/modules/npm/instance_upgrade.go:52-63`); no same-image refusal (`:107-123`, `TestUpSameImageStillRecreatesTheInstance`); the new container is created on `darkpanel-npm` with `{}` (`steps` at `:282`, `instanceCreate` at `deps.go:467`); other networks are reconnected with only their static addresses (`:126-133,146-151,346-352`). The calls `forwardCheck` adds are not traced by `upDocker`, so `TestUpPlan`'s rule that a plan calls none of the traced methods still holds (`instance_upgrade_test.go:68-80,445`).
- That the Engine reports an IPAM subnet for every user-defined bridge network is general Docker behaviour, not checked on this host; the guard fails closed when a network reports none. That the Engine would hand out a given free address B is an inference about its allocator; it is certain when B is the only free address, which an `operate` caller can arrange.
- **Unknown:** whether Docker keeps a dynamic address across a container restart or a host restart. One harness check that restarts the NPM container, and then the host, on a network where NPM has no static address would settle it. Task 9 records it as a residual risk.
- Verified 2026-11-13 by the drafter with `go test -overlay` against the unchanged tree: Tasks 2 and 3 fail as their Step 2 states with only their tests, and with scratch implementations following Step 3 `timeout 900 go test -count=1 ./internal/modules/npm/...` passed and `go vet ./internal/modules/npm/` printed nothing.
- `forwardGuard` reads the proxy hosts and resolves their names once per network; an upgrade of an NPM on k networks makes k reads. Acceptable for a few networks. Name lookups run under the npm lock, up to `resolveTimeout` (5 s) each, only when NPM joins a network.
- The guard refuses any free forwarded address of the pool, since it cannot know which one the Engine picks; a proxy host left pointing at a removed container can therefore refuse an attach, and the detail names the proxy host to fix. Proxy host add already refuses NPM's held addresses (`reservedSet`, `proxyhost_add.go:257-298`), so the reverse order is covered.

Row 6, NPM client flow (Task 4):

- A request through a runtime listener carries the header-named client as `GuardContext.Client`, already unmapped and zone-free: `internal/controller/api/server.go:224-227`, `internal/controller/api/runtime.go:88-97`, `internal/controller/dispatch/guard.go:47-57`.
- The internet cells leave out RFC 1918, CGNAT and IPv6 outside `2000::/3`: `internal/modules/firewall/eval/cells.go:17-40`. A concrete-source flow is checked as it is, with no cells and no before-state: `internal/modules/firewall/guard/cells.go:39-41`; `checkFlow` sets `prior` only for `wildSrc`.
- A concrete flow to an unspecified destination is probed on every host address its route back prefers and then the rest (`route.go` `probes`/`wildcardDsts`), which gives the `10.0.0.1:443 on eth1` and `192.0.2.1:443 on eth0` messages. Verified 2026-11-13 by the drafter in a private copy at `8f8aba2`: the guard package passes with the fix; without the `npmClientFlows` call the three positive rows and the `TestBuildFlows` row fail with Task 4's Step 2 messages; without the `fromHere` skip the `a client address of this host` row fails. `timeout 900 go test -count=1 ./internal/modules/firewall/...` passed there.
- `ckIfaces` has no `tailscale0`, so the CGNAT and ULA rows exercise the plain private-source path (`internal/modules/firewall/guard/route.go:306-308`, `fromTailnet`); no row covers a real `tailscale0`.
- Docker's userland proxy makes NPM see the bridge gateway, an address of this host, as the client, which the `fromHere` skip drops: inference from Docker's documented `docker-proxy` behaviour, unverified on this host. The tunnel reasoning (browser to Cloudflare, connector outbound to the edge) is unverified against a live tunnel.
- `internal/controller/dispatch` and `internal/controller/api` were not run by the drafter; Task 4 changes neither, and the Integration check runs them.

Rows 10 and 12, answer text and Compose values (Tasks 5 and 6):

- Go quotes a malformed `Location` header before `CheckRedirect` (`/usr/local/go/src/net/http/client.go:661-664`, go1.27.4), and a malformed status line and a header line without a colon the same way; `encoding/json` quotes a number literal decoded into an int field. Scratch tests 2026-11-13.
- `http.Client.Timeout` errors match `context.DeadlineExceeded`, and an expired context gives it before dialing: scratch test 2026-11-13.
- Callers inspect npmapi errors only through `*APIError`, `IsNotFound`, `IsClientError` and `errors.Is(err, context.Canceled)`: `rg -n 'errors\.(Is|As)' internal/modules/npm --glob '!*_test.go'` on 2026-11-13; `internal/modules/npm/instance_status.go:89`; `TestApTransportAndDecodeFailuresAreNotAPIErrors` pins the cancel case. The module's client never follows redirects (`internal/modules/npm/deps.go:164-167`); the agent logs the cause of a 5xx-class error (`internal/agent/api/handler.go:870-873`).
- Both tasks' Step 1 tests fail on `8f8aba2` as stated, and prototypes of their Step 3 passed `./internal/modules/npm/... ./internal/agent/...` and `./internal/modules/docker/...` (2026-11-13, overlay; npm took 291 s at load 8). Without Task 6's `compose_check_test.go` row change, `TestCCPrepareAppliesTheConfigurationRefusals/external_owned_network` fails with `detail "network npm names a network of module npm" does not contain "npm_net"`.
- Only `validate_test.go` and that one `compose_check_test.go` row pin the changed Compose details: `rg -n 'belongs to module|which is not present locally|builds image'` on 2026-11-13; the other hits (`internal/modules/docker/owned.go`, the cloudflare module) quote names the caller typed. `docs/user/modules/docker.md:193` describes the refusal without quoting it. The project name passed to `Validate` is the registered `row.Name` (`internal/modules/docker/compose_check.go:156`).
- Compose does not interpolate map keys (service, network, volume keys): unverified — Task 6 relies on it for the key-naming details; compose-spec interpolation applies to values.
- Not in these tasks: the `compose.Parse` error stays a cause (`compose_check.go:150-153`); its messages name paths and keys, and a wrapped JSON error could quote a number literal only if a member is decoded into an integer type, which was not checked. Other modules that copy a stderr line into errors stand as the earlier review listed them.

Rows 14 and 15, bootstrap and "/" location (Tasks 7 and 8):

- `discardDeploy` runs on `context.WithoutCancel(ctx)` bounded by `2*deployStop` and keeps the coded cause's inner error: `internal/modules/npm/instance_deploy.go:460-491`. Nothing in the change engine treats a `context.Canceled` in an Apply error specially: `rg -ln 'context\.Canceled' --type go -g '!*_test.go' internal` lists only `internal/controller/server/extservices.go` and `internal/modules/npm/instance_status.go` (2026-11-13). The fake's `StopContainer` arms no clock timer (`internal/modules/npm/testhost_test.go:568-585`).
- Task 7's test and wider rule verified 2026-11-13 with `go test -overlay` on `114a40c`: with only the Step 1 test, exactly the five subtests Step 2 names fail; with a scratch implementation of Step 3, `-run 'TestV1r12|TestDp|TestW11c02|TestV1r1'` passes, including `TestDpBootstrap`'s `the user is refused`, `NPM never comes up` and `the call is cancelled while it waits`, and `TestW11c02OnlyAnExistingAdminIsAConflict`; `timeout 1200 go test -count=1 ./internal/modules/npm/...` then printed `ok` for all four packages (167 s). The cancelling stub was named `v1r14Halt` in that run; the plan names it `w12aHalt`.
- Task 8: with the Step 1 test the red run gives the plan-diff failure and both `after change` failures; a scratch implementation passed `go vet` and `go test -count=1 ./internal/modules/npm/...` (284 s), 2026-11-13.
- An update reaches only a non-reserved host (`refuseReserved`, `internal/modules/npm/proxyhost_edit.go:308`), so a kept "/" line never sits in the panel's or admin's proxy host. `proxyHostBody` puts the module's "/" location first (`proxyhost_add.go:172`); `proxyHostEditDiff` has one caller, the update's `Plan` (`proxyhost_edit.go:376`).
- Task 8's merge drops whitespace-only lines, merges only the first "/" location (further "/" locations are still dropped), and keeps an operator line that only resembles a module line beside the module's line. No line is shown for the module's own lines written again or for kept host-level configuration and other locations (W11-R13).
- Whether NPM 2.16.0 answers a lost first-user race with 400 or 403 is unverified; the pinned image was not inspected. Task 7 does not depend on it, and Task 9's residual risk covers the 400 case.

Process:

- The plan review has not run yet; the `**Plan review:**` header line is added when it does.

## Task index

1. Record the wave 12a rulings in the spec
2. Refuse an NPM attach address a proxy host forwards to
3. Refuse an NPM upgrade while a proxy host forwards to an address NPM could take
4. Guard the NPM client's own address to the public binding
5. Keep no NPM answer text from a failed request
6. Quote no resolved value in Compose refusals
7. Remove the NPM instance when the bootstrap is cut off
8. Keep the operator's "/" location configuration on update
9. Update the threat model to the re-reviewed state

---

### Task 1: Record the wave 12a rulings in the spec

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (the "Plan rulings" list of section 10)

**Interfaces:**
- Consumes: K1 to K6 (the rulings state them)
- Produces: rulings W12a-R1 to W12a-R6

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 0 = 1

- [ ] **Step 1: No new test, because the rulings are spec prose that no script reads; `internal/docs` reads the spec only for the W11 ruling names (`TestW11b11GuardDocumentation`), which stay**

- [ ] **Step 2: Run the existing checks before the change**

Run: `timeout 300 go test -count=1 ./internal/docs/...`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`.

- [ ] **Step 3: Implement**

1. Find the wave 11c rulings with `rg -n 'Plan rulings \(wave 11' docs/dforge/specs/2026-10-21-v1-completion-design.md`. The list ends with the bullet that begins `  - W11-R18 closures:`; the line after it is blank, then `### 11. Error codes, actors and dependencies`.
2. Directly after the W11-R18 bullet, before the blank line, insert exactly this block (one bullet per line, as the wave 11 rulings are written):

```markdown
- **Plan rulings (wave 12a).** Wave 12a (plan `v1-w12a-security-residuals`) closes the residuals the 2026-11-12 re-review left in security register rows 1, 6, 10, 12, 14 and 15:
  - **W12a-R1 NPM address guard (row 1).** Extends W10b-R2. `npm.network.attach`, at plan and again at apply, and the undo of `npm.network.detach` refuse with `npm.reserved` when a proxy host or custom location forwards to an address NPM could take on the network. That address is the static IPv4 address the attach asks for, or any address of the network's pool that no endpoint holds and that is not its gateway, when the Engine picks the IPv4 or IPv6 address. A forward host that is a name counts with the addresses it resolves to, resolved as W10b-R3 says; a name with no answer adds nothing. A network that reports no subnet for an address family counts every unheld address of that family. `npm.instance.upgrade`, at plan and again at apply, before it changes anything, makes the same check for every network NPM is on. It counts the addresses NPM holds there as free, because the old container releases them, and it counts a static IPv4 address only on networks other than `darkpanel-npm`. An upgrade therefore needs NPM's API and is refused with `npm.unhealthy` when it cannot read the proxy hosts; letting it proceed would reopen the path for anyone able to stop NPM. NPM's address is not pinned. The rollback or undo of an upgrade recreates the original container without the check, because a rollback that refused would leave NPM down; that, an address that changes on a container or host restart (whether Docker keeps a dynamic address there is unknown), and an address another container frees between the check and the connect are residual risks, which the threat model records.
  - **W12a-R2 NPM client flow (row 6).** Protected flow 3 of 2.3, for NPM, runs from an internet address, and from `Client.Addr` when the request carries one, to each `Via.Public` port of that address's family (both families for a `[::]` binding) through `nat` and `FORWARD` to the NPM container. The client flow has a concrete source, so it is not compared with the rules before the change, as for a direct panel client, and a client address of this host adds none. Tunnel exposures need no client flow: the browser reaches only Cloudflare's edge, and this host carries only the connector's outbound legs to public addresses.
  - **W12a-R3 NPM transport errors (row 10).** Extends W10b-R8. A request that gets no usable answer, or a 2xx answer that does not decode, fails with the method, the path and a fixed reason (context canceled, context deadline exceeded, connection refused, no usable answer, not the expected JSON); Go's transport and JSON errors, which quote bytes of the answer, are never kept. `errors.Is` still finds a cancelled or expired context.
  - **W12a-R4 Compose refusals (row 12).** Extends W10b-R14. A refusal of a resolved configuration names the service, the object key and the member, never a value Compose resolved, which can hold an interpolated secret; the only name it quotes is Compose's default image name `<project>-<service>`. Raw Docker daemon errors of the validation are not covered; security register row 131 tracks them.
  - **W12a-R5 NPM bootstrap (row 14).** Replaces W11-R14. A 400 answer to the creation of the first administrator rejects the payload, so it fails the deploy without removing the instance, as does a failure after NPM accepted the creation (the status read after it, a missing setup flag, a refused login). Every other failure before NPM accepted the creation removes the instance as W10b-R9 says, because Darkpanel cannot then show that it owns the only administrator: an administrator NPM reported first, a 4xx other than 400, a 5xx, no answer to the creation, NPM not answering within the 120-second wait, and the apply cancelled while it waits or before NPM answered the creation. A local user who creates the first administrator through `127.0.0.1:81` during a deploy stays a residual risk, which the threat model records.
  - **W12a-R6 NPM "/" location (row 15).** Extends W10b-R7. The operator's lines in the advanced configuration of the "/" location are kept after the module's own lines, and the plan shows `location /: keeps the advanced configuration written in NPM`. The module's own lines (the X-Darkpanel-Client header and the panel's read timeout) are always written again. The update's input has no field for the operator's lines, so the plan shows only that kept line, never a removed and added pair.
```

3. Change nothing else in the spec.

- [ ] **Step 4: Run the existing checks after the change**

Run: `timeout 300 go test -count=1 ./internal/docs/... && rg -c 'W12a-R[1-6] ' docs/dforge/specs/2026-10-21-v1-completion-design.md`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`, and the count `6`.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): record the wave 12a rulings"
```

### Task 2: Refuse an NPM attach address a proxy host forwards to

**Files:**
- Modify: `internal/modules/npm/network_claims.go` (`networkAttachHandler.check`, `networkClaims.Inverse`, `networkClaims.networkUndoDisconnect`; new `forwardTarget`, `nm.forwardTargets`, `nm.forwardGuard`, `networkAssignable`)
- Modify: `internal/modules/npm/testhost_test.go` (`thDocker.attached`)
- Test: `internal/modules/npm/network_claims_test.go`

**Interfaces:**
- Consumes: none
- Produces: K1 (`nm.forwardGuard`, the `npm.reserved` details, the test helper `w12aSeed`)

**Items:** 2026-11-06-v1-security-review#1
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background: an `operate` caller adds a proxy host forwarding to a free address A of a user-defined network at port 3000 or 81. The add passes, because A is not NPM's yet. The caller then runs `npm.network.attach` on that network, either with `address: A` or with no address on a subnet where A is the only free address. The public hostname now proxies to NPM's API or admin UI without `npm.admin.expose`. Undoing an `npm.network.detach` reconnects NPM with an address the Engine picks, which is the same path. After this task, attach, at plan and again at apply, and the undo of a detach refuse with `npm.reserved` when a proxy host or custom location forwards to an address NPM could take on the network.

- [ ] **Step 1: Write the failing test**

1a. In `internal/modules/npm/testhost_test.go`, make the fake report each endpoint's addresses on a network inspect, as the Engine does. Real Docker reports them (`internal/platform/dockerapi/live_test.go:137`). Replace the whole `attached` function (comment included) with:

```go
// attached returns the endpoints of containers on network n, keyed by container id, with their addresses as the Engine
// reports them.
func (d *thDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			nc := dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
			if ep.IPAddress.IsValid() {
				nc.IPv4Address = netip.PrefixFrom(ep.IPAddress, ep.IPAddress.BitLen())
			}
			if ep.GlobalIPv6Address.IsValid() {
				nc.IPv6Address = netip.PrefixFrom(ep.GlobalIPv6Address, ep.GlobalIPv6Address.BitLen())
			}
			out[c.ID] = nc
		}
	}
	return out
}
```

1b. In `internal/modules/npm/network_claims_test.go`, add `"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"` to the imports, after the `.../core/schema` line.

1c. Extend the test `internal/modules/npm/network_claims_test.go::TestNwAttachPlan`. Insert these rows directly after the row `{name: "claim whose attachment is gone", ...}` (they pin what must still pass, so an over-broad guard fails them):

```go
		{name: "a forwarded address another container holds", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			h.docker.putContainer(thContainer("db", thEndpoint(nwNet, "10.9.0.7")))
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{join, claim}},
		{name: "a forwarded gateway", setup: w12aForwardsTo("10.9.0.1", 8080), diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address outside the subnet", setup: w12aForwardsTo("10.10.0.7", 3000), diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address outside the IP range", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			h.docker.putNetwork(thNetwork(nwNet, func(n *dockerapi.Network) {
				n.IPAM.Config = []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("10.9.0.0/24"),
					IPRange: netip.MustParsePrefix("10.9.0.128/25"), Gateway: netip.MustParseAddr("10.9.0.1")}}
			}))
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{join, claim}},
		{name: "a static address no proxy host forwards to", setup: w12aForwardsTo("10.9.0.7", 3000),
			mod:  func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") },
			diff: []core.DiffLine{join, {Op: "+", Text: "ipv4 10.9.0.2"}, claim}},
		{name: "a forwarded IPv6 address on a network without IPv6", setup: w12aForwardsTo("fd00:9::7", 3000),
			diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address while NPM is already on the network", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			nwConnect(t, h, user, nwStatic("10.9.0.5"))
			nwClaim(t, h, nwNet, nwOther, "c0", false)
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{claim}},
```

1d. Extend the test `internal/modules/npm/network_claims_test.go::TestNwAttachPlanRefusals`. Insert these rows directly before the row `{name: "NPM container missing", ...}`:

```go
		{name: "an address a proxy host forwards to", setup: w12aForwardsTo("10.9.0.7", 3000),
			mod:  func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.7") },
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "a free address the Engine could assign", setup: w12aForwardsTo("10.9.0.7", 81),
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "a forward host in nginx's address form", setup: w12aForwardsTo("10.9.0.07", 3000),
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "an address a custom location forwards to", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			w12aSeed(h, "web", 8080, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "10.9.0.7", ForwardPort: 3000})
		}, code: errcode.NPMReserved, has: "location /api of proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "an IPv6 address the Engine assigns beside a static IPv4 address", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			h.docker.putNetwork(thNetwork(nwNet, thSubnet("10.9.0.0/24", "10.9.0.1"), thSubnet("fd00:9::/64", "fd00:9::1"),
				func(n *dockerapi.Network) { n.EnableIPv6 = true }))
			w12aSeed(h, "fd00:9::7", 3000)
		}, mod: func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") },
			code: errcode.NPMReserved, has: "proxy host 1 forwards to fd00:9::7, an address darkpanel-npm could take on network appnet"},
		{name: "a forwarded address on a network that reports no subnet", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			w12aSeed(h, "10.9.0.7", 3000)
		}, code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
```

1e. Append to `internal/modules/npm/network_claims_test.go` (`v1r09Resolver` is defined in `internal/modules/npm/proxyhost_resolve_test.go`, same package):

```go
// w12aSubnet gives appnet the subnet 10.9.0.0/24 with the gateway 10.9.0.1, as the Engine reports a bridge network.
func w12aSubnet(_ *testing.T, h *thHost, _ dockerapi.Network) {
	h.docker.putNetwork(thNetwork(nwNet, thSubnet("10.9.0.0/24", "10.9.0.1")))
}

// w12aSeed adds to NPM a proxy host that forwards to host:port, with the custom locations.
func w12aSeed(h *thHost, host string, port int, locations ...npmapi.Location) {
	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"x.example.com"}, ForwardScheme: "http", ForwardHost: host,
		ForwardPort: port, Locations: locations})
}

// w12aForwardsTo gives appnet its subnet and adds a proxy host that forwards to host:port.
func w12aForwardsTo(host string, port int) nwSetupFunc {
	return func(t *testing.T, h *thHost, user dockerapi.Network) {
		w12aSubnet(t, h, user)
		w12aSeed(h, host, port)
	}
}

// TestW12aAttachChecksTheProxyHostsAgainAtApply: a proxy host added after the plan, forwarding to the address the attach
// asks for, refuses the apply before NPM joins the network.
func TestW12aAttachChecksTheProxyHostsAgainAtApply(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
	if _, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, in)); err != nil {
		t.Fatal(err)
	}
	w12aSeed(h, "10.9.0.7", 3000)
	_, err := nwApplyAttach(t, h, "c1", in)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwCalls(h, "ConnectNetwork") != 0 || nwEndpoint(t, h, user.ID) != nil || len(nwClaims(t, h, nwNet)) != 0 || len(h.journal.effects()) != 0 {
		t.Error("a refused apply connected NPM, recorded a claim or journaled an effect")
	}
}

// TestW12aAttachResolvesForwardHostNames: a forward host that is a name counts with each address it resolves to; a
// name that does not resolve adds nothing.
func TestW12aAttachResolvesForwardHostNames(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "app.example.net", 3000)
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	n := h.nm()
	n.d.resolve = v1r09Resolver("203.0.113.9", "10.9.0.7")
	_, err := n.networkAttach().Handler.Plan(ctx, h.env(""), in)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to app.example.net, which resolves to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("lookup app.example.net: no such host")
	}
	if _, err := n.networkAttach().Handler.Plan(ctx, h.env(""), in); err != nil {
		t.Errorf("plan with a name that does not resolve: %v, want it to pass", err)
	}
}

// TestW12aUndoOfADetachRefusesAForwardedAddress: the undo of a detach reconnects NPM with an address the Engine picks,
// so it is refused while a proxy host forwards to a free address of the network.
func TestW12aUndoOfADetachRefusesAForwardedAddress(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
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
	w12aSeed(h, "10.9.0.7", 3000)
	_, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("the refused undo reconnected NPM")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestNwAttachPlan|TestW12a' ./internal/modules/npm/`
Expected: FAIL. The six new `TestNwAttachPlanRefusals` subtests, `TestW12aAttachChecksTheProxyHostsAgainAtApply`, `TestW12aAttachResolvesForwardHostNames` and `TestW12aUndoOfADetachRefusesAForwardedAddress` each fail with `error <nil>, want code npm.reserved`. `TestNwAttachPlan`, its new rows included, and `TestNwAttachPlanStateHash` PASS.

- [ ] **Step 3: Implement**

All code goes in `internal/modules/npm/network_claims.go`.

1. **`type forwardTarget struct`**, with fields `where string`, `host string`, `name bool` and `addr netip.Addr`. It is one address a proxy host or custom location forwards to. `where` is `proxy host <id>` for a host's own forward, or `location <path> of proxy host <id>` for a custom location. `host` is the forward host as NPM holds it. `name` is true when `host` is a name and `addr` is one of its answers.

2. **`func (n nm) forwardTargets(ctx context.Context, env core.Env) ([]forwardTarget, error)`**:
   - Get the client with `n.d.api(ctx, env)` and return its error as it is.
   - Call `api.ProxyHosts(ctx)`. Return a failure as `proxyhostAddAnswer(err)` (`proxyhost_add.go:249`).
   - Sort the hosts by `ID` ascending. For each host, take its own `ForwardHost`, then each `Locations[i].ForwardHost` in order.
   - Skip an empty forward host.
   - When `proxy.LiteralAddr(host)` reports an address, append it. That address is already unmapped and zone-free, so nginx forms like `10.9.0.07` and `0x0a.9.0.7` count.
   - Otherwise resolve the name as `checkForward` does (`proxyhost_add.go:380-398`). Call `n.dockerAnswers(ctx, host)`. If that reports no answer, call `n.d.resolve` under `context.WithTimeout(ctx, resolveTimeout)`.
   - Unlike `checkForward`, a lookup error or an empty answer adds nothing and is not a refusal. A name that does not resolve now can reach NPM only after it is re-pointed, which is the accepted W10b-R3 residual.
   - Return `ctx.Err()` when the context has ended. Return a `dockerAnswers` error as `fmt.Errorf("npm: resolve forward host %s: %w", host, err)`.
   - Unmap the answers, drop their zones, sort them, compact them, and append one target per answer with `name: true`.

3. **`func networkAssignable(network dockerapi.Network, addr netip.Addr, staticV4 bool) bool`** reports whether the Engine could give NPM the address `addr` when NPM joins `network`. Check these in order:
   - false when `addr` is IPv4 and either `staticV4` is set or `network.EnableIPv4` is non-nil and false.
   - false when `addr` is IPv6 and `!network.EnableIPv6`. Docker assigns an IPv6 address dynamically even when the IPv4 address is static, because the attach input takes IPv4 only.
   - false when any `network.Containers` entry has `IPv4Address.Addr() == addr` or `IPv6Address.Addr() == addr`. A held address is not handed out again.
   - Over the `network.IPAM.Config` entries whose `Subnet` is valid and of `addr`'s family: true when the pool contains `addr` and `addr` is not that entry's `Gateway`. The pool is `IPRange` when it is valid, else `Subnet`.
   - When no config entry of `addr`'s family has a valid `Subnet`, return true. This fails closed: the Engine reports a subnet for every bridge network, so missing data is never trusted.

4. **`func (n nm) forwardGuard(ctx context.Context, env core.Env, network dockerapi.Network, static netip.Addr) error`** (K1):
   - `static` is the IPv4 address NPM asks for, or the zero `netip.Addr` when the Engine picks one.
   - Get the targets with `forwardTargets`. Return any failure wrapped in `core.Definite`, since the guard changes nothing.
   - In order, the first target where `static.IsValid() && t.addr == static`, or `networkAssignable(network, t.addr, static.IsValid())`, refuses with `core.Definite(errcode.New(errcode.NPMReserved, detail))`.
   - `detail` for an address target is `fmt.Sprintf("%s forwards to %s, an address %s could take on network %s", t.where, t.addr, containerName, network.Name)`.
   - `detail` for a name target is `fmt.Sprintf("%s forwards to %s, which resolves to %s, an address %s could take on network %s", t.where, t.host, t.addr, containerName, network.Name)`.
   - No refusal returns nil. The detail carries no field path, as `proxy.CheckUpstream`'s refusals carry none (`proxy/upstream.go:215-218`).

5. **`networkAttachHandler.check`**: after the line `ck := networkAttachCheck{network: network, ..., connect: ep == nil, claim: true}`, add: when `ck.connect`, call `h.n.forwardGuard(ctx, env, network, in.Address)` and return its error. The guard runs only when NPM joins, because NPM's existing addresses are already reserved by `reservedSet`. `Plan` and `Apply` both call `check`, so the guard runs at plan and again at apply. Do not add the proxy hosts to the plan's state hash. The apply runs the check again under the same module lock that `npm.proxyhost.add` takes.

6. **`networkClaims.networkUndoDisconnect`**: change its signature to `(ctx context.Context, env core.Env, network string) (core.InverseResult, error)` and update the call in `Inverse` to `c.networkUndoDisconnect(ctx, env, name)`. Before `ConnectNetwork`:
   - Call `c.npmOnNetwork(ctx, network)`. Return an error as `fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))`.
   - When NPM is already on the network, return `core.InverseResult{Resource: resourceOf(nounEndpoint, network)}, nil`, the same result the refused-and-on branch gives today.
   - Otherwise call `c.n.d.docker.InspectNetwork(ctx, network)`. Return an error as `fmt.Errorf("npm: inspect network %s: %w", network, engineCoded(err))`.
   - Call `c.n.forwardGuard(ctx, env, nw, netip.Addr{})` and return its refusal.
   - Keep the existing connect and refused-branch code unchanged after that. Update the function comment to say the reconnect is refused while a proxy host forwards to an address the Engine could pick.

7. Must not change: `validate`, the existing refusals, the plan diff lines and state hash, `Verify`, detach's own apply, and `proxy.CheckUpstream`. Add no wire field, so `make generate` changes nothing in `schemas/**` or `docs/api/**`. Imports to add: `cmp` (or sort with `slices.SortFunc` and an inline compare), `net/netip`, `slices`, `strconv` and `.../internal/modules/npm/npmapi`.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/npm/... && GOTOOLCHAIN=local timeout 300 go vet ./internal/modules/npm/`
Expected: PASS, every package `ok`, and no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/network_claims.go internal/modules/npm/network_claims_test.go internal/modules/npm/testhost_test.go
git commit -m "fix(npm): refuse attach to a forwarded address"
```

### Task 3: Refuse an NPM upgrade while a proxy host forwards to an address NPM could take

**Files:**
- Modify: `internal/modules/npm/instance_upgrade.go` (`instanceUpgradeHandler.Plan`, `instanceUpgradeHandler.Apply`; new `instanceUpgradeHandler.forwardCheck`)
- Test: `internal/modules/npm/instance_upgrade_test.go`

**Interfaces:**
- Consumes: K1 from Task 2 (`nm.forwardGuard`, and the test helpers `w12aSeed` and `nwRefused` in `network_claims_test.go`); Task 2 must be done first.
- Produces: none

**Items:** 2026-11-06-v1-security-review#1
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

Background: `npm.instance.upgrade` needs only `operate` and has no same-image refusal. It removes NPM's container, creates a new one on `darkpanel-npm` with no static address (`instanceCreate`, `deps.go:467`), and reconnects every other network keeping only static addresses (`endpointOf` and `connect`, `instance_upgrade.go:126-133,346-352`). The new container can therefore take a free address that a proxy host already forwards to. The attack sequence:
1. Attach NPM without an address to the caller's own small subnet.
2. Add a proxy host to the one remaining free address B, at port 3000. The add passes, because B is not NPM's yet.
3. Run the upgrade. NPM can land on B.

After this task, the upgrade checks every network NPM is on, at plan and again at apply, before it stops or recreates anything. It now needs NPM's API to answer: an instance whose API is down or whose container is stopped is refused with `npm.unhealthy` (ruling W12a-R1).

**The set checked.** For each network N the current container is on, the upgrade refuses with `npm.reserved` when a proxy host or custom location forwards to an address A that is either of these. A is a literal, or each answer of a name, resolved as in Task 2.
- (a) The static IPv4 address NPM's endpoint on N keeps. This applies only when N is not `darkpanel-npm`, because the upgrade re-requests the static address only on the other networks.
- (b) An address the Engine could hand the new container on N: inside N's pool (`IPRange`, else `Subnet`, of A's family), not N's gateway, and held by no other container's endpoint on N. IPv4 counts only when the endpoint keeps no static IPv4 address; IPv6 counts only when N has IPv6 enabled. If N reports no subnet for A's family, every such unheld A counts.

The addresses NPM holds now are counted as free in (b), because the old container releases them. Proxy host add already refuses them while NPM holds them (`reservedSet`, `proxyhost_add.go:257-298`). A proxy host that forwards to one anyway exists only through NPM's own UI or the attach hole. Because the upgrade checks them too, an upgrade surfaces such a host instead of carrying it over.

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/npm/instance_upgrade_test.go`. It already imports `npmapi`, `errcode`, `core`, `json` and `context`. `w12aSeed` and `nwRefused` are in `network_claims_test.go` (same package). This is a new table test rather than rows of `TestUpRefusals`, because that table's rows are positional and do not check the detail.

```go
// w12aUpSetup is an upgrade setup whose two networks report their subnets, as the Engine does: darkpanel-npm, where
// NPM holds 172.20.0.2 without a static address, and app-net, where it holds the static 172.30.0.9.
func w12aUpSetup(t *testing.T) *upSetup {
	t.Helper()
	s := upNewSetup(t, upFromImage, true)
	s.h.docker.putNetwork(thNetwork(networkName, thNetLabels(map[string]string{core.LabelModule: ID}), thSubnet("172.20.0.0/16", "172.20.0.1")))
	s.h.docker.putNetwork(thNetwork(upAppNet, thSubnet("172.30.0.0/24", "172.30.0.1")))
	return s
}

// TestW12aUpgradeRefusesAForwardedAddress: the recreated container joins every network again, so the upgrade is
// refused at plan and at apply, before anything changes, while a proxy host or location forwards to an address it
// could take there.
func TestW12aUpgradeRefusesAForwardedAddress(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		seed func(s *upSetup)
		want string
	}{
		{"a free address of NPM's own network", func(s *upSetup) { w12aSeed(s.h, "172.20.0.9", 3000) },
			"proxy host 1 forwards to 172.20.0.9, an address darkpanel-npm could take on network darkpanel-npm"},
		{"the address NPM releases on its own network", func(s *upSetup) { w12aSeed(s.h, "172.20.0.2", 81) },
			"proxy host 1 forwards to 172.20.0.2, an address darkpanel-npm could take on network darkpanel-npm"},
		{"NPM's static address on another network", func(s *upSetup) {
			w12aSeed(s.h, "app.example.net", 8080, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "172.30.0.9", ForwardPort: 3000})
		}, "location /api of proxy host 1 forwards to 172.30.0.9, an address darkpanel-npm could take on network app-net"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w12aUpSetup(t)
			tc.seed(s)
			_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
			if coded := nwRefused(t, err, errcode.NPMReserved); coded.Detail != tc.want {
				t.Errorf("plan detail %q, want %q", coded.Detail, tc.want)
			}
			_, err = s.apply()
			if coded := nwRefused(t, err, errcode.NPMReserved); coded.Detail != tc.want {
				t.Errorf("apply detail %q, want %q", coded.Detail, tc.want)
			}
			if len(s.effects()) != 0 || s.phase() != "no row" || s.dock.counts["StopContainer"] != 0 || len(s.dock.created) != 0 {
				t.Error("a refused upgrade journaled an effect, recorded a phase, stopped NPM or created a container")
			}
		})
	}
}

// TestW12aUpgradeAllowsForwardsNPMCannotTake: an address another container holds, a gateway, a free address of a
// network where NPM's address is static, and an address outside NPM's networks leave the upgrade alone.
func TestW12aUpgradeAllowsForwardsNPMCannotTake(t *testing.T) {
	ctx := context.Background()
	s := w12aUpSetup(t)
	s.h.docker.putContainer(thContainer("db", thEndpoint(networkName, "172.20.0.7")))
	w12aSeed(s.h, "172.20.0.7", 5432)
	w12aSeed(s.h, "172.20.0.1", 8080)
	w12aSeed(s.h, "172.30.0.20", 3000)
	w12aSeed(s.h, "10.99.0.7", 3000)
	if _, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := s.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestW12aUpgrade|TestUp' ./internal/modules/npm/`
Expected: FAIL. The three subtests of `TestW12aUpgradeRefusesAForwardedAddress` each fail at the plan with `error <nil>, want code npm.reserved`. `TestW12aUpgradeAllowsForwardsNPMCannotTake` and every `TestUp*` test PASS.

- [ ] **Step 3: Implement**

All code goes in `internal/modules/npm/instance_upgrade.go`.

1. **`func (h instanceUpgradeHandler) forwardCheck(ctx context.Context, env core.Env, c dockerapi.ContainerJSON) error`**: for each network name of `c.NetworkSettings.Networks`, in name order (`slices.Sorted(maps.Keys(...))`), skipping a nil endpoint:
   - Call `h.n.d.docker.InspectNetwork(ctx, name)`. Return an error as `core.Definite(fmt.Errorf("npm: inspect network %s: %w", name, engineCoded(err)))`.
   - Run `delete(nw.Containers, c.ID)`, so that the addresses the old container releases count as free.
   - Set `static`: the zero `netip.Addr` when `name == networkName`, since `instanceCreate` asks for `{}` there; otherwise `ep.IPAMConfig.IPv4Address` when `ep.IPAMConfig` is non-nil, which `endpointOf` keeps for the reconnect.
   - Call `h.n.forwardGuard(ctx, env, nw, static)` and return its error unchanged. It is already `core.Definite`.
   - Return nil after the last network.
2. **`Plan`**: right after `row, c, err := h.precheck(ctx, env)` and its error return, call `h.forwardCheck(ctx, env, c)` and return its error. The diff lines and the state hash do not change.
3. **`Apply`**: right after the same `precheck` call and its error return, and before `h.pull`, call `h.forwardCheck(ctx, env, c)` and return its error. A refusal therefore journals nothing, records no upgrade row and stops nothing.
4. The check sits in `Plan` and `Apply`, not inside `precheck`, so `Inverse` and `Recover` do not run it. Add the import `net/netip`.
5. Must not change: `rollback`, `restore`, `resume`, `Inverse`, `Recover`, `instanceUpgradeConfig`, `endpointOf`, the plan diff and the state hash. Add no wire field, so `make generate` changes nothing.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/modules/npm/... && GOTOOLCHAIN=local timeout 300 go vet ./internal/modules/npm/`
Expected: PASS, every package `ok`, and no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_upgrade.go internal/modules/npm/instance_upgrade_test.go
git commit -m "fix(npm): refuse upgrade onto a forwarded address"
```

### Task 4: Guard the NPM client's own address to the public binding

**Files:**
- Modify: `internal/modules/firewall/guard/flows.go` (`viaFlows`, new `npmClientFlows`)
- Test: `internal/modules/firewall/guard/exposure_test.go`
- Test: `internal/modules/firewall/guard/flows_test.go` (`TestBuildFlows`)

**Interfaces:**
- Consumes: none
- Produces: K2 (the flow label `exposure <ExposureID> client`)

**Items:** 2026-11-06-v1-security-review#6
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background. A request through an NPM exposure already carries the browser's address in `GuardContext.Client`: `clientPeer` (`internal/controller/api/server.go:224`) returns `core.Peer{Addr: p.Client}` for a proxied request, and `Dispatcher.guardContext` (`internal/controller/dispatch/guard.go:47`) copies it with `Via`. `panelFlows` rightly skips that client for the panel listeners (`g.Via != nil`), but nothing else uses it. The NPM public leg (`npmPublicFlows`) is an any-address flow checked over the internet cells, which leave out RFC 1918, CGNAT and IPv6 outside `2000::/3` (`internal/modules/firewall/eval/cells.go:17-40`). So a change that drops a private or tailnet-range client's HTTPS to NPM passes the guard. This task adds one concrete flow per public binding from that client. No wire type or action schema changes.

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/firewall/guard/exposure_test.go`. Every helper used already exists in the package: `w11b09Plan`, `w11b09Nat` in this file; `v1r21HostRoutes`, `v1r21Route`, `v1r21Fib`, `v1r21CheckHost`, `v1r21State`, `v1r21Table` in `internet_cells_test.go`; `gHost` in `facts_test.go`; `ckIfaces` in `guard_test.go`. The file already imports `core`, `netip`, `strings` and `testing`.

```go

// w12aClientPlan is w11b09Plan(bind) of a request whose NPM exposure
// named client in its client header.
func w12aClientPlan(bind, client string) *core.GuardContext {
	g := w11b09Plan(bind)
	g.Client = &core.Peer{Addr: netip.MustParseAddr(client)}
	return g
}

func TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused(t *testing.T) {
	routes := append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))
	for _, tc := range []struct {
		name, bind, client string
		v4, v6             []string // the drop that cuts the client, in each family's filter table
		want               string   // "" when the change must pass
	}{
		{"an RFC 1918 client", "0.0.0.0:443", "10.0.0.50",
			[]string{"-A FORWARD -s 10.0.0.50/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil,
			"exposure exp1 client from 10.0.0.50 to 10.0.0.1:443 on eth1: drop"},
		{"a CGNAT client on the IPv4 leg of a dual-stack binding", "[::]:443", "100.101.102.103",
			[]string{"-A FORWARD -s 100.101.102.103/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil,
			"exposure exp1 client from 100.101.102.103 to 192.0.2.1:443 on eth0: drop"},
		{"a ULA client on the IPv6 leg of a dual-stack binding", "[::]:443", "fd7a:115c:a1e0::5",
			nil, []string{"-A INPUT -s fd7a:115c:a1e0::5/128 -p tcp -m tcp --dport 443 -j DROP"},
			"exposure exp1 client from fd7a:115c:a1e0::5 to [2001:db8::1]:443 on eth0: drop"},
		{"an IPv6 client of an IPv4-only binding", "0.0.0.0:443", "fd7a:115c:a1e0::5",
			nil, []string{"-A INPUT -s fd7a:115c:a1e0::5/128 -p tcp -m tcp --dport 443 -j DROP"}, ""},
		// Docker's userland proxy names the bridge gateway, an address of this host.
		{"a client address of this host", "0.0.0.0:443", "172.30.0.1",
			[]string{"-A FORWARD -s 172.30.0.1/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(v4, v6 []string) core.Guard {
				t.Helper()
				h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: routes}}
				return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(v4...), v1r21Table(v6...)), w12aClientPlan(tc.bind, tc.client))
			}
			g := check(tc.v4, tc.v6)
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK: no client flow applies", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			}
			if g := check(nil, nil); !g.OK {
				t.Errorf("guard = %+v, want OK without the drop", g)
			}
		})
	}
}
```

- [ ] **Step 1b: Extend the test `internal/modules/firewall/guard/flows_test.go::TestBuildFlows`**

In the row named `"a client that came through an exposure is not a direct panel flow"`, after its last `want` entry

```go
				{label: "exposure exp1 public", pkt: flPkt("input", "::", 0, "::", 443, "tcp"), wild: wildSrc},
```

(the one directly above that row's closing `},` and the `"tunnel exposure"` row) add:

```go
				{label: "exposure exp1 client", pkt: flPkt("input", "203.0.113.7", 0, "192.0.2.1", 443, "tcp")},
				{label: "exposure exp1 client", pkt: flPkt("input", "203.0.113.7", 0, "0.0.0.0", 443, "tcp")},
```

This pins the binding rules at the flow layer: the IPv4 client `client4` (203.0.113.7) gets a flow to the local binding `192.0.2.1:443` as recorded and to the non-local `203.0.113.80:443` (upstream 1:1 NAT) as `0.0.0.0:443`, and none to the two IPv6 bindings. The row keeps its name: still no `panel` flow.

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused|TestBuildFlows' ./internal/modules/firewall/guard/`
Expected: FAIL. Subtests `an_RFC_1918_client`, `a_CGNAT_client_on_the_IPv4_leg_of_a_dual-stack_binding` and `a_ULA_client_on_the_IPv6_leg_of_a_dual-stack_binding` fail with `guard = {ID:firewall.lockout OK:true Message:every protected flow passes (N checked) ...}, want a refusal containing "exposure exp1 client from 10.0.0.50 to 10.0.0.1:443 on eth1: drop"` (and the CGNAT and ULA messages from the table). `TestBuildFlows/a_client_that_came_through_an_exposure_is_not_a_direct_panel_flow` fails with `buildFlows =` followed by a list missing the two `exposure exp1 client` flows. The rows `an_IPv6_client_of_an_IPv4-only_binding` and `a_client_address_of_this_host` pass (they pin what the fix must not add).

- [ ] **Step 3: Implement**

1. In `internal/modules/firewall/guard/flows.go`, add `func npmClientFlows(s *flowSet, label string, client *core.Peer, public []netip.AddrPort, local map[netip.Addr]bool)`, placed directly after `npmPublicFlows`. Doc comment: it adds the flow from the concrete client of a request that came through an NPM exposure to each public binding that takes the client's family.
2. Return without adding anything when `client == nil`, `!client.Addr.IsValid()`, or `fromHere(client.Addr, local)` (the same three skips `panelFlows` applies to a direct client). Let `src := client.Addr.Unmap()`.
3. For each `pub` in `public`, choose the destination `dst`, starting from `pub.Addr().Unmap()`, by the first case that holds, in this order:
   - `pub.Addr() == netip.IPv6Unspecified()` (the dual-stack `[::]` binding, compared exactly as `npmPublicFlows` does): `dst = anyAddr(src)`, so either family of client is checked.
   - `dst.Is4() != src.Is4()`: skip this binding (it does not take the client's family).
   - `dst.IsUnspecified() || local[dst]`: keep `dst` as it is.
   - otherwise (a public address that is not this host's, reached through upstream 1:1 NAT): `dst = anyAddr(dst)`, exactly as `npmPublicFlows` maps it.
   Then add `flow{label: label + " client", pkt: newPacket("input", src, unknownPort, dst, pub.Port(), "tcp")}` with `s.add`. Set no `wild`, `standIn` or `fwdReplies`: the flow has a concrete source, and both fields only matter for any-address stand-in flows (`route.go` `noReply`, `inputProbes`). The packet is the same chain, port and protocol as the public leg: `input` path, TCP, the binding's port, connection state NEW; the evaluator follows Docker's DNAT into `FORWARD` as it does for the public leg.
4. In `viaFlows`, in `case "npm":`, directly after the `for _, pub := range v.Public { npmPublicFlows(s, label, v, pub, local) }` loop, call `npmClientFlows(s, label, g.Client, v.Public, local)`. Because the call sits inside `viaFlows`, an exposure in `retired` adds no client flow either. With no public bindings the existing `"the exposure has no public bindings"` unknown flow still refuses, and the client adds nothing.
5. Extend `viaFlows`' doc comment with one sentence: for NPM, the concrete client of the request is also checked against each public binding of its family, since the internet cells leave out private and special-purpose sources.
6. Do not change `panelFlows`, `npmPublicFlows`, the tunnel case, `checkFlow` or any wire type. The client flow is not compared with the before-state (`checkFlow` does that only for `wildSrc` flows), the same as a direct panel client (ruling W12a-R2).
7. Tunnel exposures need no change: the browser's packets end at Cloudflare's edge, and this host only carries the connector's outbound legs to that edge, which are public addresses the cells cover. A deny of the client's address on this host cannot cut its path.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 900 go test -count=1 ./internal/modules/firewall/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/flows.go internal/modules/firewall/guard/exposure_test.go internal/modules/firewall/guard/flows_test.go
git commit -m "fix(firewall): guard the NPM client's own address"
```

### Task 5: Keep no NPM answer text from a failed request

**Files:**
- Modify: `internal/modules/npm/npmapi/client.go` (`(*Client).do`, new unexported `unanswered`)
- Create: `internal/modules/npm/npmapi/transport_test.go`
- Test: `internal/modules/npm/answer_text_test.go`

**Interfaces:**
- Consumes: none
- Produces: K3

**Items:** 2026-11-06-v1-security-review#10
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background: Go's `net/http` client quotes answer bytes in its errors before `CheckRedirect` runs (`failed to parse Location header "http://[<secret>"`, `malformed HTTP status code "<secret>"`, `malformed MIME header: missing colon: "<secret>"`), and `encoding/json` quotes a number literal of the answer (`cannot unmarshal number <digits> into Go struct field ProxyHost.id of type int`). `do` wraps both verbatim, `apiVia` (`internal/modules/npm/deps.go`) keeps the error as the cause of `npm.unhealthy`, and `writeProblem` (`internal/agent/api/handler.go`) logs it. Spec W10b-R8 says NPM's text never reaches an error, its cause or a log line. The fix classifies, it does not scrub: a fixed vocabulary cannot leak, where removing quoted substrings would miss formats Go adds later. A compromised NPM can write any digits, so a decimal encoding of the password would reach the log through the JSON error; that is why the decode cause is dropped, not shortened.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/npm/npmapi/transport_test.go`:

```go
package npmapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// w12aRaw starts an NPM that answers every request with raw, the bytes of a whole HTTP answer, so the answer can be
// one net/http itself would never write, and returns its URL.
func w12aRaw(t *testing.T, raw string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString(raw)
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// w12aClient is a client of base that sees a redirect as an answer and never follows it, as the npm module's is.
func w12aClient(base string) *Client {
	return New(base, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}

// TestW12aFailuresKeepNoAnswerText: a request that ends without a usable answer, or with a 2xx answer that does not
// decode, fails with an error that names the method, the path and a fixed reason, never text NPM sent.
func TestW12aFailuresKeepNoAnswerText(t *testing.T) {
	login := func(ctx context.Context, base string) error {
		_, err := w12aClient(base).Token(ctx, "admin@example.com", "placeholder-password")
		return err
	}
	read := func(ctx context.Context, base string) error {
		_, err := w12aClient(base).ProxyHost(ctx, 1)
		return err
	}
	closed := apStart(t, 200, `{}`)
	closedURL := closed.srv.URL
	closed.srv.Close()
	passed, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		base   string
		call   func(context.Context, string) error
		secret string // text of the answer the error must not carry; empty when the answer carries none
		want   string
		is     error // a sentinel the error must still match, or nil
	}{
		{"a redirect to a malformed Location", t.Context(),
			w12aRaw(t, "HTTP/1.1 302 Found\r\nLocation: http://[placeholder-secret\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a malformed status line", t.Context(), w12aRaw(t, "HTTP/1.1 placeholder-secret\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a header line without a colon", t.Context(), w12aRaw(t, "HTTP/1.1 200 OK\r\nplaceholder-secret\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a refused connection", t.Context(), closedURL, login, "", "npmapi: POST /api/tokens: connection refused", nil},
		{"a deadline that has passed", passed, closedURL, login, "",
			"npmapi: POST /api/tokens: context deadline exceeded", context.DeadlineExceeded},
		{"a number NPM chose in a 2xx answer", t.Context(),
			w12aRaw(t, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n{\"id\":31415926535897932384626433}"),
			read, "31415926535897932384626433", "npmapi: decode /api/nginx/proxy-hosts/1 answer: not the expected JSON", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(tc.ctx, tc.base)
			if err == nil {
				t.Fatal("the call succeeded, want an error")
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error text %q, want %q", got, tc.want)
			}
			var ae *APIError
			if errors.As(err, &ae) {
				t.Errorf("error %v is an *APIError, want a failure that proves nothing about NPM", err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("error %v does not match %v", err, tc.is)
			}
			for e := err; e != nil && tc.secret != ""; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), tc.secret) {
					t.Errorf("error %q carries the answer text %q", e.Error(), tc.secret)
				}
			}
		})
	}
}
```

Append to `internal/modules/npm/answer_text_test.go` (its imports already cover everything used):

```go
// TestW12aLoginRedirectCarriesNoNPMText: an NPM that answers the login with a redirect whose Location quotes the
// password it was sent fails the login as npm.unhealthy, and no error of the chain carries the password.
func TestW12aLoginRedirectCarriesNoNPMText(t *testing.T) {
	h := newTHHost(t)
	inst := h.deployed()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var login struct {
			Secret string `json:"secret"`
		}
		_ = json.NewDecoder(r.Body).Decode(&login)
		w.Header().Set("Location", "http://["+login.Secret)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	n := h.nm()
	n.d.api = apiVia(func() *npmapi.Client { return npmapi.New(srv.URL, hc) })
	_, err := n.d.api(context.Background(), h.env(""))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
	}
	if !strings.Contains(err.Error(), "npmapi: POST /api/tokens: no usable answer") {
		t.Errorf("error %q does not name the failed login", err.Error())
	}
	v1r11Chain(t, err, inst.Password)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestW12a' ./internal/modules/npm/npmapi/ ./internal/modules/npm/`
Expected: FAIL. Every row of `TestW12aFailuresKeepNoAnswerText` fails on its text, for example `error text "npmapi: Post \"http://127.0.0.1:<port>/api/tokens\": failed to parse Location header \"http://[placeholder-secret\": parse \"http://[placeholder-secret\": missing ']' in host", want "npmapi: POST /api/tokens: no usable answer"`, and the redirect, status line, header and number rows also report `carries the answer text`. `TestW12aLoginRedirectCarriesNoNPMText` fails with `does not name the failed login` and `carries "th-admin-password-0123456789abcdefghijk"`.

- [ ] **Step 3: Implement**

1. In `internal/modules/npm/npmapi/client.go`, add an unexported `func unanswered(err error) error` that returns, in this order of checks: `context.Canceled` when `errors.Is(err, context.Canceled)`; `context.DeadlineExceeded` when `errors.Is(err, context.DeadlineExceeded)` (this also covers the `http.Client.Timeout` of the module's client, whose error matches it); an error with the text `connection refused` when `errors.Is(err, syscall.ECONNREFUSED)`; otherwise an error with the text `no usable answer`. It returns the sentinel or a fixed error and never wraps or formats `err`. Add `"syscall"` to the imports. A dial timeout without a deadline reads `no usable answer`; no separate `timed out` reason is added.
2. In `(*Client).do`, the failure of `c.hc.Do(req)` (anchor: `res, err := c.hc.Do(req)`) returns `fmt.Errorf("npmapi: %s %s: %w", method, path, unanswered(err))`: the `method` argument as passed (`POST`), the `path` argument without the query. The `%w` keeps `errors.Is(err, context.Canceled)` true, which `TestApTransportAndDecodeFailuresAreNotAPIErrors` and `cancelled` in `internal/modules/npm/instance_status.go` rely on.
3. The read failure (anchor: `return fmt.Errorf("npmapi: read %s answer: %w", path, err)`) becomes `fmt.Errorf("npmapi: read %s answer: %w", path, unanswered(err))`.
4. The decode failure (anchor: `if err := json.Unmarshal(b, out); err != nil {`) returns `fmt.Errorf("npmapi: decode %s answer: not the expected JSON", path)` with no cause.
5. Unchanged: the encode error and the `http.NewRequestWithContext` error (both hold only Darkpanel's own request), `apiError` and `APIError`, the size-limit error, and `apiVia`'s wrapper text `the Nginx Proxy Manager API is not reachable` in `internal/modules/npm/deps.go`.
6. Change the doc comment of `do` to: `do sends one request. A 2xx answer is decoded into out when out is not nil and otherwise dropped; any other status becomes an *APIError, which never keeps the answer's text. A request without a usable answer, or a 2xx answer that does not decode, fails with the method, the path and a fixed reason, because Go's transport and JSON errors quote bytes of the answer.`

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 900 go test -count=1 ./internal/modules/npm/... ./internal/agent/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/npmapi/client.go internal/modules/npm/npmapi/transport_test.go internal/modules/npm/answer_text_test.go
git commit -m "fix(npm): keep no answer text from failed requests"
```

### Task 6: Quote no resolved value in Compose refusals

**Files:**
- Modify: `internal/modules/docker/compose/validate.go` (`Validate`)
- Test: `internal/modules/docker/compose/validate_test.go`
- Test: `internal/modules/docker/compose_check_test.go`

**Interfaces:**
- Consumes: none
- Produces: K4

**Items:** 2026-11-06-v1-security-review#12
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background: `docker compose config` succeeds with interpolated values, `compose.Parse` keeps them, and `Validate` quotes them in refusal details: the image of a build service, a `container_name`, a container reference, a network or volume `name`. `validateConfig` (`internal/modules/docker/compose_check.go`) returns the refusal as a Definite `docker.compose_unavailable` or `docker.owned` whose detail a read-scope caller sees in the change record. Spec W10b-R14 keeps Compose's stderr out; this closes the sibling path through its standard output. The default image name `<project>-<service>` stays in the build refusal, since neither part is a Compose value, so the common case still tells the operator which image to build.

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/docker/compose/validate_test.go` (its imports already cover everything used):

```go
// TestW12aRefusalsQuoteNoResolvedValue: Compose interpolates values from the
// project's environment, so a refusal of a resolved configuration names the
// service, the object key and the member, never a value, which can hold a
// secret.
func TestW12aRefusalsQuoteNoResolvedValue(t *testing.T) {
	const secret = "placeholder-secret"
	rows := []struct {
		name   string
		in     string
		owners map[string]string
		code   errcode.Code
		detail string
	}{
		{"the image of a build service",
			`{"name":"shop","services":{"web":{"build":{"context":"."},"image":"app:placeholder-secret"}}}`,
			nil, errcode.DockerComposeUnavailable,
			"service web builds the image its image member names, which is not present locally; Compose is run with --no-build"},
		{"an owned container_name",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27","container_name":"app-placeholder-secret"}}}`,
			map[string]string{"container app-placeholder-secret": "npm"}, errcode.DockerOwned,
			"service web: container_name names a container of module npm"},
		{"an owned container through volumes_from",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27","volumes_from":["container:app-placeholder-secret"]}}}`,
			map[string]string{"container app-placeholder-secret": "npm"}, errcode.DockerOwned,
			"service web refers to a container of module npm"},
		{"an owned network name",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27"}},"networks":{"backend":{"name":"net-placeholder-secret"}}}`,
			map[string]string{"network net-placeholder-secret": "panel"}, errcode.DockerOwned,
			"network backend names a network of module panel"},
		{"an owned volume name",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27"}},"volumes":{"data":{"name":"vol-placeholder-secret"}}}`,
			map[string]string{"volume vol-placeholder-secret": "npm"}, errcode.DockerOwned,
			"volume data names a volume of module npm"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			c, err := Parse([]byte(r.in))
			if err != nil {
				t.Fatal(err)
			}
			err = Validate("shop", c, cvFacts(nil, r.owners))
			cvRefusal(t, err, r.code, r.detail)
			if strings.Contains(err.Error(), secret) {
				t.Errorf("refusal %q quotes the resolved value", err.Error())
			}
		})
	}
}
```

- [ ] **Step 1b: Extend the tests `TestCVValidateRefuses` and `TestCVValidateContainerRefs` in `internal/modules/docker/compose/validate_test.go`**

Change the expected detail of these rows (every other row stays as it is):

| Test, row name | New detail |
|---|---|
| `TestCVValidateRefuses`, `container name of an owned container` | `service web: container_name names a container of module npm` |
| `TestCVValidateRefuses`, `network of an owned network` | `network default names a network of module panel` |
| `TestCVValidateRefuses`, `volume of an owned volume` | `volume data names a volume of module npm` |
| `TestCVValidateRefuses`, `network without a resolved name uses the project prefix` | `network default names a network of module panel` |
| `TestCVValidateRefuses`, `build with a named image that is not present` | `service web builds the image its image member names, which is not present locally; Compose is run with --no-build` |
| `TestCVValidateRefuses`, `ownership is checked before the other refusals` | `service web: container_name names a container of module npm` |
| `TestCVValidateContainerRefs`, `an owned container through network_mode` | `service web refers to a container of module npm` |
| `TestCVValidateContainerRefs`, `an owned container through volumes_from, after an unowned container_name` | `service web refers to a container of module npm` |

In `TestCCPrepareAppliesTheConfigurationRefusals` in `internal/modules/docker/compose_check_test.go`, the row `external owned network` changes its expected detail parts from `[]string{"npm_net", "npm"}` to `[]string{"network npm names a network of module npm"}` (the configuration's network key is `npm`, its resolved name `npm_net`).

The rows `build of an image that is not present` and `build with no facts about images` keep `service web builds image shop-web, which is not present locally; Compose is run with --no-build`: Compose's default name is built from the project and service names only.

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 ./internal/modules/docker/compose/`
Expected: FAIL (the `compose_check_test.go` row fails only in Step 4's wider run until Step 3 lands; it is not run here to keep this step fast). `TestW12aRefusalsQuoteNoResolvedValue` fails on all five rows, for example `Validate refused with docker.compose_unavailable "service web builds image app:placeholder-secret, which is not present locally; Compose is run with --no-build", want docker.compose_unavailable "service web builds the image its image member names, ..."`; the eight rows changed in Step 1b fail the same way (`Validate refused with docker.owned "container npm-app belongs to module npm", want docker.owned "service web: container_name names a container of module npm"`).

- [ ] **Step 3: Implement**

In `Validate` in `internal/modules/docker/compose/validate.go`:

1. The `container_name` refusal (anchor: `"container %s belongs to module %s", s.ContainerName, id`) becomes `refuse(errcode.DockerOwned, "service %s: container_name names a container of module %s", name, id)`.
2. The container-reference refusal (anchor: `"container %s belongs to module %s", ref, id`) becomes `refuse(errcode.DockerOwned, "service %s refers to a container of module %s", name, id)`.
3. The network and volume refusal (anchor: `"%s %s belongs to module %s", g.kind, name, id`) becomes `refuse(errcode.DockerOwned, "%s %s names a %s of module %s", g.kind, key, g.kind, id)`: the object's key in the file, never its resolved name.
4. The build refusal (anchor: `if ref := ImageName(project, name, s); !f.imagePresent(ref) {`): when `s.Image != ""` it refuses with `refuse(errcode.DockerComposeUnavailable, "service %s builds the image its image member names, which is not present locally; Compose is run with --no-build", name)`; when `s.Image == ""` it keeps today's detail, which names `ImageName`'s `<project>-<service>`.
5. Unchanged: what `Facts.Owner` and `Facts.ImagePresent` are asked (still the resolved names and reference; `TestCVValidateAsksForTheEngineNamesOnly` pins this), the refusal order, the project refusal (the project is Darkpanel's registered name, `row.Name`, not a Compose value), the label refusals and the key refusals (keys are not interpolated).
6. In the doc comment of `Validate`, replace `The detail names the service, key or object.` with `The detail names the service, the object key or the member, never a resolved value, which Compose may have interpolated from the project's environment.`

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 900 go test -count=1 ./internal/modules/docker/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/docker/compose/validate.go internal/modules/docker/compose/validate_test.go internal/modules/docker/compose_check_test.go
git commit -m "fix(docker): quote no resolved value in refusals"
```

### Task 7: Remove the NPM instance when the bootstrap is cut off

**Files:**
- Modify: `internal/modules/npm/instance_deploy.go` (`deployHandler.Apply`, `deployHandler.bootstrap`)
- Modify: `docs/user/modules/npm.md` (the Deploy section's two `npm.setup_failed` paragraphs, the `npm.setup_failed` row of the error table)
- Test: `internal/modules/npm/instance_deploy_conflict_test.go`

**Interfaces:**
- Consumes: none
- Produces: K5

**Items:** 2026-11-06-v1-security-review#14
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background: today a deploy removes what it created only when NPM reports an administrator before the creation or answers the creation with a 4xx other than 400. A cancellation or a lost connection during the bootstrap, a 5xx answer and a wait that times out leave the container running, and a local user who reaches `127.0.0.1:81` can then create the first administrator. Ruling W12a-R5: keep W11-R14's 400 branch, and remove the instance on every other failure before NPM accepted the creation, because Darkpanel cannot then show that it owns the only administrator.

- [ ] **Step 1: Extend the test `internal/modules/npm/instance_deploy_conflict_test.go::TestV1r12BootstrapConflictRemovesTheInstance`**

Add `"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"` to the import block, after the `.../npm/fake` import.

Replace the whole function `TestV1r12BootstrapConflictRemovesTheInstance` with the code below. The type `w12aHalt` goes directly above it. The table gains a `cancelled` column, the arrange functions gain a `cancel` parameter, Apply runs under `dpDrive` (`instance_deploy_test.go`) on a context the case can cancel, so the fake clock advances while a case waits. The rows `NPM fails while it creates the administrator` and `the answer to the creation is lost` change from `false, false` to `false, true`, and three rows are added: the wait timing out (`dpHealth` from `instance_deploy_test.go` answering 502 to every status read) and two cancellations. `v1r12Racer`, `v1r12Removals` and `TestV1r12BootstrapConflictReportsAFailedRemoval` stay as they are.

```go
// w12aHalt is the unauthenticated NPM client of a deploy that is cancelled while it waits for NPM to answer.
type w12aHalt struct {
	npmAPI
	cancel context.CancelFunc
}

func (s w12aHalt) Health(ctx context.Context) (npmapi.Health, error) {
	s.cancel()
	return s.npmAPI.Health(ctx)
}

func TestV1r12BootstrapConflictRemovesTheInstance(t *testing.T) {
	cases := []struct {
		name      string
		arrange   func(h *thHost, n *nm, cancel context.CancelFunc)
		foreign   bool // the administrator in NPM is the intruder's
		removed   bool
		cancelled bool // the apply's context ends during the bootstrap
	}{
		{"an administrator exists before the deploy creates one", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
		}, true, true, false},
		{"an administrator appears between the status read and the creation", func(h *thHost, n *nm, _ context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI {
				return v1r12Racer{npmAPI: live, win: func() { h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword) }}
			}
		}, true, true, false},
		{"NPM refuses to create the administrator", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusForbidden})
		}, false, true, false},
		{"NPM fails while it creates the administrator", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusInternalServerError})
		}, false, true, false},
		{"the answer to the creation is lost", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{AfterCommit: true})
		}, false, true, false},
		{"NPM does not answer within the wait", func(_ *thHost, n *nm, _ context.CancelFunc) {
			probe := &dpHealth{npmAPI: n.d.anon(), failing: -1}
			n.d.anon = func() npmAPI { return probe }
		}, false, true, false},
		{"the deploy is cancelled while it creates the administrator", func(_ *thHost, n *nm, cancel context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI { return v1r12Racer{npmAPI: live, win: cancel} }
		}, false, true, true},
		{"the deploy is cancelled while it waits for NPM", func(_ *thHost, n *nm, cancel context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI { return w12aHalt{npmAPI: live, cancel: cancel} }
		}, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c.arrange(h, &n, cancel)
			var err error
			dpDrive(t, h, deployPoll, func() {
				_, err = n.instanceDeploy().Handler.Apply(ctx, h.env("c1"), dpInput(dpEmail))
			})
			var coded *errcode.Error
			if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if core.IsDefinite(err) {
				t.Error("the failure is Definite: effects of the deploy were made")
			}
			if got := errors.Is(err, context.Canceled); got != c.cancelled {
				t.Errorf("error %v carries the cancellation: %t, want %t", err, got, c.cancelled)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestV1r12BootstrapConflictRemovesTheInstance' ./internal/modules/npm/`
Expected: FAIL in five subtests; the other three PASS:
- `NPM_fails_while_it_creates_the_administrator` with `the container is present: true, want false` (the same for the network and both volumes), `detail "Nginx Proxy Manager did not create the administrator (HTTP 500)" says the deploy removed its objects: false, want true` and `Engine changes after the start [], want [StopContainer RemoveContainer RemoveVolume RemoveVolume RemoveNetwork]`;
- `the_answer_to_the_creation_is_lost` with the same lines, its detail `"Nginx Proxy Manager did not create the administrator"`;
- `NPM_does_not_answer_within_the_wait` with the same lines, its detail `"Nginx Proxy Manager did not answer within 2m0s (HTTP 502)"`;
- `the_deploy_is_cancelled_while_it_creates_the_administrator` with `error context canceled, want npm.setup_failed`;
- `the_deploy_is_cancelled_while_it_waits_for_NPM` with `error context canceled, want npm.setup_failed`.

- [ ] **Step 3: Implement**

1. `deployHandler.bootstrap(ctx context.Context, env core.Env, email, password string) (conflict bool, err error)` becomes `(discard bool, err error)`. In `Apply`, the lines `conflict, err := h.bootstrap(ctx, env, email, password)` / `if conflict {` become `discard, err := ...` / `if discard {`. The call to `h.discardDeploy(ctx, containerID, err)` stays as it is.
2. `discard` is true for every failure before NPM accepted the creation request, except a 400 answer to it (ruling W12a-R5). Exactly these cases return true:
   - the status read reports `health.Setup` (unchanged: `Nginx Proxy Manager already has an administrator`);
   - the apply's context ends while the deploy waits for NPM. In the poll loop, `return false, cerr` (after `if cerr := ctx.Err(); cerr != nil {`) becomes `return true, errcode.Wrap(errcode.NPMSetupFailed, cerr, "the deploy was cancelled while it waited for Nginx Proxy Manager to answer")`. The `deploySleep` failure `return false, err` becomes `return true, errcode.Wrap(errcode.NPMSetupFailed, err, <the same detail>)`;
   - the wait times out: `return false, deploySetupFailed(ctx, err, fmt.Sprintf("did not answer within %s", deployReady))` becomes `return true, ...` with the same error;
   - `api.CreateAdmin` fails and `ctx.Err()` is set. Check this first in the `if err := api.CreateAdmin(...); err != nil {` branch and return `true, errcode.Wrap(errcode.NPMSetupFailed, cerr, "the deploy was cancelled before Nginx Proxy Manager answered the creation of the administrator")`;
   - `api.CreateAdmin` fails otherwise, unless the error holds an `*npmapi.APIError` whose `Status` is 400: no answer, a lost connection, an answer that could not be read, a 4xx other than 400, a 5xx. The `discard` expression is `!errors.As(err, &answer) || answer.Status != 400`, and the error stays `deploySetupFailed(ctx, err, "did not create the administrator")`.
3. These cases still return `false`, with their current errors: a 400 (ruling W11-R14, kept by W12a-R5), and every failure after `CreateAdmin` returned nil (the second `Health`, the missing setup flag, the refused `Token`). Once NPM has accepted the anonymous creation, the administrator it holds is Darkpanel's.
4. The cancellation must stay in the error chain. `discardDeploy` already keeps the `*errcode.Error`'s cause, so `errors.Is(err, context.Canceled)` holds. `TestDpBootstrap/the_call_is_cancelled_while_it_waits` depends on this. Do not change `discardDeploy`, `deploySetupFailed` or `deploySleep`.
5. Rewrite `bootstrap`'s doc comment to state the rule from items 2 and 3: discard is true for every failure before NPM accepted the creation of the administrator except a 400 answer to it, because the deploy cannot then show that the administrator NPM holds is its own; a 400 and the failures after the creation (a missing setup flag, a refused login) are not discarded.
6. `docs/user/modules/npm.md`, Deploy section. Replace the paragraph that starts `If the administrator cannot be created for any reason but an existing` with:

   ```
   If NPM answers the request that creates the administrator with status 400, or
   fails after it created the administrator, the change fails with
   `npm.setup_failed`: the container runs and the administrator may exist. Run
   `darkpanel change undo <id>`. The undo removes the container, both volumes and
   the network, and forgets the instance. Then deploy again.
   ```

   Replace the next paragraph's first two sentences (from `If NPM already has an administrator` up to and including `` `npm.setup_failed`. ``) with:

   ```
   If NPM already has an administrator when the deploy reaches that step, does
   not answer within 120 seconds, answers the request that creates the first
   administrator with an error status other than 400, gives no answer to that
   request, or the change is cancelled before NPM answers it, the deploy cannot
   show that the administrator is its own. The deploy then removes the
   container, both volumes and the network it created, so that no instance keeps
   running under another administrator, and the change fails with
   `npm.setup_failed`.
   ```

   Keep the rest of that paragraph (`The message says whether the removal succeeded. ...` through `... for each effect it lists as unresolved.`) as it is.
7. Same file, error table: replace the `npm.setup_failed` row with:

   ```
   | `npm.setup_failed` | The administrator could not be created: NPM did not answer within 120 seconds, already had an administrator, refused or failed the creation, gave no answer to it, or did not accept the new administrator's login, or the change was cancelled first. When the message says the deploy removed what it created, the deploy could not show that NPM's administrator is its own. | If the message says the deploy removed what it created, do not undo: deploy again, as the Deploy section says. Otherwise run `darkpanel change undo <id>`, which removes the container, both volumes and the network, then deploy again. |
   ```

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 900 go test -count=1 ./internal/modules/npm/... ./internal/docs/...`
Expected: PASS, every line `ok`. `TestW11c02OnlyAnExistingAdminIsAConflict` keeps the 400 row `removes: false`, and `TestDpBootstrap` (including `the user is refused`, `the user answer is lost`, `NPM never comes up` and `the call is cancelled while it waits`) stays green unchanged. The run takes about 6 minutes on a loaded host.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_deploy.go internal/modules/npm/instance_deploy_conflict_test.go docs/user/modules/npm.md
git commit -m "fix(npm): remove the instance on a cut-off bootstrap"
```

### Task 8: Keep the operator's "/" location configuration on update

**Files:**
- Modify: `internal/modules/npm/proxy/location.go` (new `OperatorConfig`)
- Modify: `internal/modules/npm/proxyhost_edit.go` (`proxyHostEditKeep`, `proxyHostEditDiff`, new `proxyHostEditRootConfig`)
- Test: `internal/modules/npm/proxyhost_effects_test.go`

**Interfaces:**
- Consumes: none
- Produces: K6

**Items:** 2026-11-06-v1-security-review#15
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

Background: `npm.proxyhost.update` rewrites the "/" location from its input and drops any configuration the operator wrote there in NPM, with no diff line. The fix merges, it does not keep verbatim: a verbatim keep would never bring the module's own lines up to date and would keep an operator's deletion of the X-Darkpanel-Client line. The merge writes the module's lines again and puts the operator's lines after them. The test seeds a "/" location without the module line and updates twice, so it rejects both the verbatim keep and a merge that repeats the module's line.

- [ ] **Step 1: Extend the test `internal/modules/npm/proxyhost_effects_test.go::TestV1r10UpdateKeepsTheOperatorsAdvancedConfig`**

Add `"slices"` to the import block, after `"reflect"`. Replace the whole function `TestV1r10UpdateKeepsTheOperatorsAdvancedConfig` with the version below. It keeps the original assertions: the host's advanced configuration and the `/api` location are kept, and the "/" location follows the new port. It adds an operator edit of the "/" location made through NPM's API, a plan-diff assertion, and a second update to show the module's own line is not repeated.

```go
func TestV1r10UpdateKeepsTheOperatorsAdvancedConfig(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	// The operator replaced the module's "/" configuration in NPM with rules of its own.
	rootRules := "allow 10.0.0.0/8;\ndeny all;\nproxy_set_header X-Root-Key " + v1r10LocSecret + ";"
	api, err := h.nm().d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	edited := puBodyOf(host)
	edited.Locations = append([]npmapi.Location(nil), host.Locations...)
	edited.Locations[0].AdvancedConfig = rootRules
	if host, err = api.UpdateProxyHost(ctx, host.ID, edited); err != nil {
		t.Fatal(err)
	}
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	def := h.nm().proxyhostUpdate()
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})
	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	kept := core.DiffLine{Op: " ", Text: "location /: keeps the advanced configuration written in NPM"}
	if !slices.Contains(plan.Diff, kept) {
		t.Errorf("plan diff %+v, want the line %+v", plan.Diff, kept)
	}
	want := proxyHostBody(next, "")
	want.AdvancedConfig = host.AdvancedConfig
	want.Locations[0].AdvancedConfig += "\n" + rootRules
	want.Locations = append(want.Locations, host.Locations[1])
	// A second update of the same input must not repeat the module's own line.
	for _, id := range []string{"c1", "c2"} {
		h.addChange(id, "npm.proxyhost.update")
		if _, err := def.Handler.Apply(ctx, h.env(id), raw); err != nil {
			t.Fatal(err)
		}
		hosts := puHosts(t, h)
		if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), want) {
			t.Errorf("after change %s NPM holds %+v, want the module's / location for the new port followed by the operator's / rules, and the operator's advanced configuration and /api location kept: %+v", id, hosts, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `timeout 300 go test -count=1 -run 'TestV1r10UpdateKeepsTheOperatorsAdvancedConfig' ./internal/modules/npm/`
Expected: FAIL with `plan diff [{Op:- Text:forward: http://app:8080} {Op:+ Text:forward: http://app:9090}], want the line {Op:  Text:location /: keeps the advanced configuration written in NPM}`, then `after change c1 NPM holds [...]` and `after change c2 NPM holds [...]`: NPM's "/" location holds only the module's `proxy_set_header X-Darkpanel-Client $realip_remote_addr;`.

- [ ] **Step 3: Implement**

1. `internal/modules/npm/proxy/location.go`: add `func OperatorConfig(advancedConfig string) string` with a doc comment. It splits `advancedConfig` on `"\n"` and keeps, in order, every line that is not exactly `clientHeaderLine` or `panelTimeoutLine` and is not empty or whitespace only. It returns the kept lines joined by `"\n"`, or `""` when none remain. Compare whole lines exactly, without trimming: an indented copy of a module line inside an operator's block belongs to the operator. Add `"strings"` to the imports. `Location` does not change.
2. `internal/modules/npm/proxyhost_edit.go`: add `func proxyHostEditRootConfig(locations []npmapi.Location) string`. It returns `proxy.OperatorConfig` of the `AdvancedConfig` of the first location whose `Path` is `"/"`, or `""` when there is none. Import `github.com/darkraise/darkpanel/internal/modules/npm/proxy`.
3. `proxyHostEditKeep(body npmapi.ProxyHostBody, host npmapi.ProxyHost)`: after `body.Locations = slices.Clone(body.Locations)`, when `op := proxyHostEditRootConfig(host.Locations)` is not `""`, append `"\n" + op` to the `AdvancedConfig` of the first location in `body.Locations` whose `Path` is `"/"` (the module's location from `proxyHostBody`). The host's advanced configuration and the non-"/" locations are kept as before. The "/" location's forward scheme, host and port still come from the input. Update the doc comment: it keeps the host's advanced configuration, the operator's lines of the "/" location's advanced configuration after the module's own lines, and every location whose path is not "/".
4. `proxyHostEditDiff(from, to npmapi.ProxyHostBody)`: after the `line("websockets", ...)` call, when `proxyHostEditRootConfig(to.Locations) != ""`, append `core.DiffLine{Op: " ", Text: "location /: keeps the advanced configuration written in NPM"}`. Never put the configuration's text in the diff: it can hold credentials, and `v1r10NoSecret` checks the plan for them. Only this kept line exists; the input has no field that changes the operator's lines (ruling W12a-R6).
5. Must not change: `proxyHostBody` (`npm.proxyhost.add` uses it), `proxyHostEditState`, `Verify`, `Inverse` and `undoBody` (the undo still restores the sealed body). `Op: " "` is already a documented op (`internal/core/contract.go:192`), so no wire type or schema changes and nothing is regenerated. `TestPuUpdateAppliesTheInput` pins an exact diff for a host whose "/" location holds only the module's line; it gets no new line and must stay green.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 900 go test -count=1 ./internal/modules/npm/...`
Expected: PASS, every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/proxy/location.go internal/modules/npm/proxyhost_edit.go internal/modules/npm/proxyhost_effects_test.go
git commit -m "fix(npm): keep the operator's / location config"
```

### Task 9: Update the threat model to the re-reviewed state

**Files:**
- Modify: `docs/security/threat-model.md` (areas Compose execution, Firewall and Exposure)

**Interfaces:**
- Consumes: K1, K2, K3, K4, K5 (the text describes them)
- Produces: none

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: No new test, because the edits are prose that `internal/docs/threat_model_test.go::TestW10TmMitigationsNameExistingRepositoryPaths`, `::TestW10TmFollowsTheDocumentationStyle` and `internal/docs/threat_model_state_test.go::TestV1r25PageStatesWhatTheCodeNowDoes` already check for named paths, internal references and the pinned phrases, all of which the edits keep**

- [ ] **Step 2: Run the existing checks before the change**

Run: `timeout 300 go test -count=1 ./internal/docs/...`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`.

- [ ] **Step 3: Implement**

Make exactly these edits in `docs/security/threat-model.md`; change nothing else. Add no bullet to `## Known residual risks`: `TestV1r25KnownResidualRisksAreWhatStaysOpen` pins its bullet count. Each quoted passage is one line of the file.

1. **Compose execution, Mitigations**, the bullet that begins `- A failed \`docker compose config\` or run reports its exit status`: append ` A refusal of the resolved configuration names services, object keys and members, never a resolved value (\`internal/modules/docker/compose\`).`
2. **Firewall, Mitigations**, the bullet that begins `- A lockout guard predicts the effect of a change`: replace the sentence `It checks the public leg of a panel exposure, the browser to NPM binding, from the exposure's own record, and a request through an NPM exposure without those bindings fails closed.` with `It checks the public leg of a panel exposure, the browser to NPM binding, from the exposure's own record: from any internet address, and from the address of the client whose request came through the exposure, whatever its range, to each binding of that client's family. A request through an NPM exposure without those bindings fails closed.`
3. **Firewall, Residual risk**, the bullet that begins `- The cells cover the public unicast space only.`: replace its last sentence `A protected flow with a concrete address, such as an SSH session from a private network, is checked whatever its range (\`internal/modules/firewall/eval\`).` with `A protected flow with a concrete address, such as an SSH session from a private network or the client of a request through an NPM exposure to each binding of its address family, is checked whatever its range (\`internal/modules/firewall/eval\`, \`internal/modules/firewall/guard\`).`
4. **Firewall, Residual risk**, the bullet that begins `- A wildcard bind is checked on the host's bridge and tailnet addresses too.`: replace its last sentence, from `A direct panel client or an agent peer is a concrete source` to the end of the bullet, with `A direct panel client, the client of a request through an NPM exposure, or an agent peer is a concrete source, so its flow is not compared with the rules before the change: while a wildcard panel or agent listener, or a wildcard NPM binding, is allowed only on some of the host's addresses, a guarded change that protects such a flow is refused until overridden, even when the change does not touch those rules (\`internal/modules/firewall/guard\`).`
5. **Firewall, Residual risk**, append two bullets after the wildcard-bind bullet:
   - `- The guard protects the NPM path of the client making the request only. Another private or tailnet client of the same exposure is covered only by the any-address flow, which leaves its range out.`
   - `- The harness probes UDP only on the ports the exposure view lists. A scan cannot tell a silent open UDP port from a filtered one, so a UDP listener the view does not list and that answers no probe is not caught (\`test/harness\`).`
6. **Exposure, Mitigations**: after the bullet that ends `each custom location it can check (\`internal/modules/npm/proxy\`, \`internal/modules/npm\`).`, insert the bullet `- Attaching NPM to a network, undoing a detach, and upgrading NPM are refused with \`npm.reserved\` while a proxy host or custom location forwards to an address NPM could take on a network it joins: the static address it asks for, or a free address of the network's pool when the Engine picks one. An upgrade counts the addresses NPM holds as free, and is refused when NPM's API cannot list the proxy hosts. A forward host that is a name is resolved as for a proxy host, and a name with no answer is not counted (\`internal/modules/npm/network_claims.go\`, \`internal/modules/npm/instance_upgrade.go\`).`
7. **Exposure, Mitigations**, the bullet that begins `- The agent's NPM client limits the size of every answer`: replace its second sentence `It keeps only the status and an allowlisted error code of a refusal, so NPM's message text never reaches an error, a log line or a change record (\`internal/modules/npm/npmapi\`).` with `It keeps only the status and an allowlisted error code of a refusal, and only the method, the path and a fixed reason of a request that got no usable answer or a 2xx answer that did not decode, so NPM's text never reaches an error, a log line or a change record (\`internal/modules/npm/npmapi\`).`
8. **Exposure, Mitigations**, replace the bullet that begins `- When the creation of NPM's first administrator finds one` with `- When the creation of NPM's first administrator finds one that Darkpanel did not create, or fails before NPM accepted it for any reason but a 400 answer (a refusal, a server error, no answer, NPM not answering within 120 seconds, or the change cancelled first), the deploy stops and removes the container, the data volumes and the network it created before it fails (\`internal/modules/npm\`).`
9. **Exposure, Residual risk**, append two bullets after the bullet that begins `- A container on a network joined through the NPM attach action`:
   - `- NPM's addresses can still change where nothing checks them. Whether Docker keeps an address that is not static across a restart of NPM's container or of the host is unknown; if it does not, NPM can take a free address a proxy host already forwards to. Rolling back or undoing an upgrade recreates the original container without the check, because a rollback that refused would leave NPM down. An address another container held when it was checked can be freed before NPM connects.`
   - `- Port 81 is published on 127.0.0.1 before Darkpanel creates NPM's first administrator, so a local user who reaches it during a deploy can create that administrator first. If NPM then answers Darkpanel's creation with 400, the deploy fails and leaves the instance running, and until the operator undoes the change the other user holds the administrator.`

Check the result with `rg -n 'W1[0-9]a?-R|wave [0-9]|task [0-9]|rows? [0-9]' docs/security/threat-model.md` (no output).

- [ ] **Step 4: Run the existing checks after the change**

Run: `timeout 300 go test -count=1 ./internal/docs/...`
Expected: PASS, `ok  	github.com/darkraise/darkpanel/internal/docs`.

- [ ] **Step 5: Commit**

```bash
git add docs/security/threat-model.md
git commit -m "docs(security): record the wave 12a fixes"
```
