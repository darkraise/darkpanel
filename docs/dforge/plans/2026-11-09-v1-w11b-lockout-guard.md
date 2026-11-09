# v1 wave 11b: lockout guard fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close security-review rows 27, 28, 46, 47, 48, 51 and 53: the lockout guard checks every address a wildcard bind takes, reads policy routing, covers the IPv6 public leg of an NPM exposure, and fails closed when a check would cost too much.

**Architecture:** `netstat` gains the route-lookup selectors, a retry around interrupted dumps, and an `ip rule` dump. The guard's router reads the policy rules once per family beside the routes: their prefixes cut cells, their protocol and port selectors are carried on lookups, and a selector no lookup can carry turns stand-in skipping off. An unspecified IPv6 public binding is read as dual-stack, and the panel records `[::]:443` for every NPM exposure. No wire type or database schema changes.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), `golang.org/x/sys/unix` rtnetlink constants, the firewall guard (`internal/modules/firewall/guard`) and evaluator (`internal/modules/firewall/eval`), the KVM harness in `test/harness` (GitHub Actions `harness.yml`), Markdown checked by `internal/docs`.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 with risk 3 (policy rules in the guard, and the dual-stack binding)

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w11b-tmp/integ && export TMPDIR=/root/.cache/w11b-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -count=1 ./... && timeout 300 go test -tags harness -count=1 ./cmd/darkpanel/ && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

> **External executors:** agy

## Global Constraints

- Scope is spec revision 8 as amended on `main`, plus the rulings W11-R7 to W11-R12 that Task 11 copies into the spec. It covers register rows `2026-11-06-v1-security-review#27`, `#28`, `#46`, `#47`, `#48`, `#51` and `#53`. Wave 11c's rows are not in this plan.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan:
  - talks to the Docker daemon, D-Bus or systemd;
  - runs `systemctl`, `docker`, `ufw`, `iptables`, `ip6tables`, `iptables-restore`, `nft` or `nmap`;
  - binds an address other than `127.0.0.1` (port 0);
  - reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process.

  Read-only rtnetlink dumps and lookups in `internal/platform/netstat` live tests are allowed: the existing `TestRoutesAndInterfacesLive` already makes them. Host effects are tested through the fake hosts of the guard package. Real `ufw` and `iptables` run only on harness VMs.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w11b-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first. When the task ends, remove it and run `go clean -testcache`. `/tmp` is an 8 GiB tmpfs. When `df -h /` shows 90 % or more before a task's first build, run `go clean -cache`.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`. After every task, `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1` on the task's packages (every line `ok`); `go test -race -count=1 ./internal/modules/firewall/...`; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`.
- Generated files: no task changes a wire type, so `schemas/**` and `docs/api/**` stay unchanged and `TestAdAPIDocsCurrent` stays green.
- TDD: write the test, run it and see the expected RED (a build failure naming the first undefined identifier, or a failed assertion), then implement and run GREEN. Quote RED and GREEN verbatim in the ledger. A row that pins behaviour already present passes before the change; the step says so.
- Existing behaviour that must not change: a flow without a wild end and a route-back lookup keep their failure texts; `checkFlow` still returns the results of the first blocked cell; `cutters` still drops rules that cannot match when every target is modelled; a host without policy rules sends exactly the route queries it sends today.
- Frozen files (spec 1.12): no task edits `internal/core/**`, `internal/platform/*.go` (the `internal/platform/netstat/` directory is not frozen), `internal/controller/**`, `go.mod`, `go.sum`, `test/harness/pins.go` or `schemas/**`. Production code changes only where a task's **Files:** block names it. A task that finds it needs an unlisted production file stops and reports it. A pre-existing test in the task's own package whose assertion the task's change makes stale is updated to the new behaviour without stopping, staged by the task's `git add`, and named in the ledger with the reason.
- A test this plan supplies that fails after the implementation matches the task's text is reported, not rewritten to pass: state the expected and the actual message in the ledger and stop the task.
- File ownership: only Task 11 edits the spec, `docs/security/threat-model.md` and `internal/docs/`. No task edits `.github/workflows/**`, `Makefile`, `docs/runbook.md` or `CHANGELOG.md`. The plan runs on branch `v1-w11b-lockout-guard` in the base repository (no worktree).
- Test isolation: files under `t.TempDir()`; no TCP listener; no package-level mutable state. Harness tests carry `Unit` in the name of every VM-free test.
- Go identifiers: every new test function carries its task's prefix from Contracts "Test prefixes", and every new unexported test helper the same prefix in lower case.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, never naming a plan, task, register row or review; gofmt clean; error strings start with the package name; a changed refusal keeps its error code.
- Documentation style (Task 11): English, plain Markdown, sentence-case headings, present tense. A page describes what exists on `main` and never names a plan, wave, task or register row (the spec's "Plan rulings" bullets are exempt). Every relative link resolves.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind.
- Gates: the plan ends with the gates in `docs/dforge/gates.md`. Gate 7 (harness) applies: Task 10 edits `test/harness/` and Tasks 4 to 9 change the firewall module's host behaviour. Push the branch and dispatch `harness.yml` on it per the owner's standing authorization, after Task 11. No task creates or pushes a tag or runs `release.yml`.

## Contracts

### Test prefixes

| Task | Package | Go test prefix |
|---|---|---|
| 1, 2 | `internal/platform/netstat` | `TestW11b01`, `TestW11b02` |
| 3 | `internal/modules/firewall/eval` | `TestW11b03` |
| 4 to 9 | `internal/modules/firewall/guard` (Task 9 also `internal/modules/panel`) | `TestW11b04` to `TestW11b09` |
| 10 | `test/harness` | `TestW11b10Unit` |
| 11 | `internal/docs` | `TestW11b11` |

### netstat

Produced by Tasks 1 and 2, consumed by Task 8.

- `RouteQuery` gains `Proto uint8`, `SrcPort uint16`, `DstPort uint16`. `routeRequest` encodes `RTA_IP_PROTO` (1 byte), `RTA_SPORT` and `RTA_DPORT` (big-endian 16 bits), each only when non-zero.
- `retryDump(dump func() ([][]byte, error)) ([][]byte, error)`: at most 3 attempts, retrying only on `errDumpInterrupted`.
- `netstat.Rule` (comparable), in `internal/platform/netstat/rules.go`:

  ```go
  type Rule struct {
      Family   Family
      Priority uint32
      Table    uint32
      Action   uint8        // FR_ACT_*
      Src, Dst netip.Prefix // zero when the rule has no prefix
      Mark     bool         // FRA_FWMARK or FRA_FWMASK present
      Proto    uint8        // FRA_IP_PROTO, 0 when absent
      SrcPort  bool         // FRA_SPORT_RANGE present
      DstPort  bool         // FRA_DPORT_RANGE present
      UID      bool         // FRA_UID_RANGE present
  }
  ```
- `netstat.Rules(family Family) ([]Rule, error)`: `RTM_GETRULE` dump through `retryDump`, request body `rtMsg(af, 0, 0, 0)`. `parseRule(m []byte) (Rule, error)`.

### Guard internals

- `func (r *router) wildcardDsts(pref netip.Addr) []netip.Addr` (Task 4).
- `flow.fwdReplies []netip.Addr` replaces `flow.fwdReply`; nil when the flow has no forwarded reply (Task 5, consumed by Tasks 8 and 9).
- `routeCutters(routes []netstat.Route, v6 bool) routeCut`. `routeCut` holds `rs *netfilter.Ruleset`, `noPath []netip.Prefix`, `blackholes []netip.Prefix`, `paths []netip.Prefix` (unicast and local routes outside the local table), `why string` (Task 6), and `rules []netstat.Rule` (Task 8).
- `overCellLimit(cells, rules int) (limit int, over bool)` (Task 6): over when `cells > 65536`, limit 65536; else over when `cells*(rules+1) > 10_000_000`, limit 10000000; otherwise `0, false`.
- `func (r *router) forState(st fwstate.Loaded) *router`: a copy with `st` replaced, sharing the caches (Task 7).
- `checkFlow` returns `(results []FlowResult, skipped int)`; `verdict(results []FlowResult, skipped int) core.Guard` (Task 7).
- `ruleDumper` interface `Rules(family netstat.Family) ([]netstat.Rule, error)` in `guard/routes.go`, optional on a `Host`; `LiveHost.Rules`; test field `gHost.rules []netstat.Rule` with a `Rules` method that filters by family (Task 8).
- `func (r *router) selectors(q netstat.RouteQuery, proto string, srcPort, dstPort uint16) netstat.RouteQuery` (Task 8).

### Refusal texts

- Cell limit (Task 6): `the rules and routes split the internet into %d ranges for this flow; checking them against %d rules is more than the guard evaluates (limit %d)`.
- Unreadable policy rules (Task 8): `the policy rules cannot be read: %v`.
- Pass with skipped probes (Task 7): `every protected flow passes (%d checked, %d already blocked before the change)`. Without skipped probes the text stays `every protected flow passes (%d checked)`.

## Assumptions (evidence)

- `x/sys` already holds every constant used: `RTA_IP_PROTO`, `RTA_SPORT`, `RTA_DPORT`, `RTM_GETRULE`, `FRA_DST`, `FRA_SRC`, `FRA_PRIORITY`, `FRA_FWMARK`, `FRA_TABLE`, `FRA_FWMASK`, `FRA_UID_RANGE`, `FRA_IP_PROTO`, `FRA_SPORT_RANGE`, `FRA_DPORT_RANGE`, `FR_ACT_TO_TBL` (`rg` over `zerrors_linux.go` and `ztypes_linux.go` of the module-cache version, 2026-11-09).
- The row 27 change makes exactly four existing tests stale (scratch-copy trial, 2026-11-09, recorded in `docs/dforge/notes/v1-w11b-guard-factsheet.md`). Task 4 lists them.
- `netfilter.Parse` yields `Target.Name` `REDIRECT` and a `raw` `MARK` rule, and a route error in `reroute` gives `Unknown` (same trial; `internal/modules/firewall/eval/hooks.go` `reroute`).
- The kernel emits no `FRA_UID_RANGE` for a rule without a uid selector and no `FRA_PRIORITY` for priority 0. `unverified — Task 2 verifies it` through `TestW11b02RulesLive` on the main rule.
- A flow with a before-state skips what that state already blocks (`internal/modules/firewall/plan.go`: `Before: &p.cur`). The revert path passes none (`internal/modules/firewall/wire.go` `lockoutVia`), but `revert.go` reaches it only after a revert with blocked effects, conflicts or a write error, so a `[::]:443` record on a host whose IPv6 rules drop 443 affects only such troubled reverts.
- Listener rows stored before this plan keep `0.0.0.0:443` (migration `0016_listener_public_binding.sql`) and are checked as IPv4 only until the exposure is recreated. v1.0 has not shipped, so no released controller holds such a row.
- Tasks 1, 2, 5 and 7 carry `agy` executor lines because the lane gate admits them (total at most 8, risk at most 2); the fact sheet named a rung only for Tasks 3, 10 and 11.

## Task index

1. netstat lookup selectors and dump retry
2. netstat policy rule dump
3. eval: one original-destination parser
4. Wildcard binds on every address
5. Every forwarded reply, and the reply tests
6. Cells: limit, local routes, rewrites, longest match
7. Before-state router and skipped probes
8. Policy rules in the guard
9. Dual-stack public binding
10. Harness journal window and single log parse
11. Spec rulings and threat model

---

### Task 1: netstat lookup selectors and dump retry

**Files:**
- Modify: `internal/platform/netstat/routes.go` (`RouteQuery`, `routeRequest`, `Routes`)
- Test: `internal/platform/netstat/routes_test.go`

**Interfaces:**
- Produces: `RouteQuery.Proto`, `.SrcPort`, `.DstPort`; `retryDump` (Contracts "netstat")

**Items:** 2026-11-06-v1-security-review#28, 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Extend the test `internal/platform/netstat/routes_test.go::TestRouteRequest` and add the retry test**

In `TestRouteRequest`, set the query to:

```go
q := RouteQuery{Dst: netip.MustParseAddr("::ffff:192.0.2.1"), Src: netip.MustParseAddr("192.0.2.2"), Mark: 0x40000,
	Proto: unix.IPPROTO_TCP, SrcPort: 443, DstPort: 8443}
```

and add to its `want` map:

```go
unix.RTA_IP_PROTO: {unix.IPPROTO_TCP},
unix.RTA_SPORT:    {0x01, 0xbb},
unix.RTA_DPORT:    {0x20, 0xfb},
```

The test's last assertion, on the length of an IPv6 request with only a destination, already pins that a zero selector adds no attribute. Append to the file:

```go
func TestW11b01RetryDump(t *testing.T) {
	msgs := [][]byte{{1}}
	other := errors.New("netstat: netlink receive: boom")
	for _, tt := range []struct {
		name  string
		seq   []error // the outcome of each dump in turn, nil for a complete one
		calls int
		want  error
	}{
		{"a complete dump", []error{nil}, 1, nil},
		{"interrupted once", []error{errDumpInterrupted, nil}, 2, nil},
		{"interrupted twice", []error{errDumpInterrupted, errDumpInterrupted, nil}, 3, nil},
		{"interrupted on every attempt", []error{errDumpInterrupted, errDumpInterrupted, errDumpInterrupted, nil}, 3, errDumpInterrupted},
		{"another error is not retried", []error{other, nil}, 1, other},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := retryDump(func() ([][]byte, error) {
				e := tt.seq[calls]
				calls++
				if e != nil {
					return nil, e
				}
				return msgs, nil
			})
			if !errors.Is(err, tt.want) || calls != tt.calls {
				t.Fatalf("retryDump = %v after %d calls, want %v after %d", err, calls, tt.want, tt.calls)
			}
			if tt.want == nil && !reflect.DeepEqual(got, msgs) {
				t.Errorf("retryDump = %v, want %v", got, msgs)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRouteRequest|TestW11b01' ./internal/platform/netstat/`
Expected: FAIL, a build error naming `unknown field Proto` or `undefined: retryDump`.

- [ ] **Step 3: Implement**

1. `RouteQuery` gains the three fields of Contracts, each with a short comment naming it optional.
2. `routeRequest`: after the mark attribute, when `q.Proto != 0` append `nlAttr(unix.RTA_IP_PROTO, []byte{q.Proto})`; when a port is non-zero append `RTA_SPORT` or `RTA_DPORT` with `binary.BigEndian.AppendUint16(nil, port)`. The attribute order does not matter. Update the function's doc comment.
3. Add `retryDump` with a named constant for the 3 attempts. It returns the last result, and returns at once for anything but `errors.Is(err, errDumpInterrupted)`.
4. `Routes`: wrap its `netlinkRequest` call in `retryDump`, keeping the `netstat: route dump: %w` wrapping.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/platform/netstat/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/netstat/routes.go internal/platform/netstat/routes_test.go
git commit -m "feat(netstat): add lookup selectors and dump retry"
```

---

### Task 2: netstat policy rule dump

**Files:**
- Create: `internal/platform/netstat/rules.go`
- Test: `internal/platform/netstat/rules_test.go`

**Interfaces:**
- Consumes: `retryDump` (Contracts "netstat")
- Produces: `netstat.Rule`, `netstat.Rules`, `parseRule` (Contracts "netstat")

**Items:** 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-sonnet-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

`internal/platform/netstat/rules_test.go`:

```go
package netstat

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// w11b02Msg is an RTM_NEWRULE payload: struct fib_rule_hdr, then attrs.
func w11b02Msg(family, dstLen, srcLen, table, action uint8, attrs ...[]byte) []byte {
	b := rtMsg(family, dstLen, srcLen, 0)
	b[4], b[7] = table, action
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func TestW11b02ParseRule(t *testing.T) {
	const act = unix.FR_ACT_TO_TBL
	addr := func(s string) []byte { return netip.MustParseAddr(s).AsSlice() }
	span := []byte{1, 0, 2, 0}
	for _, tt := range []struct {
		name string
		m    []byte
		want Rule
	}{
		{"the main rule", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_MAIN, act,
			nlAttr(unix.FRA_PRIORITY, nlUint32(32766)), nlAttr(unix.FRA_TABLE, nlUint32(unix.RT_TABLE_MAIN)),
			nlAttr(unix.FRA_SUPPRESS_PREFIXLEN, nlUint32(0xffffffff)), nlAttr(unix.FRA_PROTOCOL, []byte{2})),
			Rule{Family: IPv4, Priority: 32766, Table: unix.RT_TABLE_MAIN, Action: act}},
		{"priority 0 has no priority attribute", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_LOCAL, act),
			Rule{Family: IPv4, Table: unix.RT_TABLE_LOCAL, Action: act}},
		{"a table above 255 comes from the attribute", w11b02Msg(unix.AF_INET, 0, 0, unix.RT_TABLE_COMPAT, act,
			nlAttr(unix.FRA_TABLE, nlUint32(1000))),
			Rule{Family: IPv4, Table: 1000, Action: act}},
		{"source and destination prefixes", w11b02Msg(unix.AF_INET, 24, 16, 200, act,
			nlAttr(unix.FRA_DST, addr("198.41.192.0")), nlAttr(unix.FRA_SRC, addr("45.33.0.0"))),
			Rule{Family: IPv4, Table: 200, Action: act,
				Dst: netip.MustParsePrefix("198.41.192.0/24"), Src: netip.MustParsePrefix("45.33.0.0/16")}},
		{"an IPv6 prefix", w11b02Msg(unix.AF_INET6, 32, 0, 200, act, nlAttr(unix.FRA_DST, addr("2606:4700::"))),
			Rule{Family: IPv6, Table: 200, Action: act, Dst: netip.MustParsePrefix("2606:4700::/32")}},
		{"a mark", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_FWMARK, nlUint32(0x80000))),
			Rule{Family: IPv4, Table: 200, Action: act, Mark: true}},
		{"a mask without a mark", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_FWMASK, nlUint32(0xff0000))),
			Rule{Family: IPv4, Table: 200, Action: act, Mark: true}},
		{"protocol and ports", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_IP_PROTO, []byte{unix.IPPROTO_TCP}),
			nlAttr(unix.FRA_SPORT_RANGE, span), nlAttr(unix.FRA_DPORT_RANGE, span)),
			Rule{Family: IPv4, Table: 200, Action: act, Proto: unix.IPPROTO_TCP, SrcPort: true, DstPort: true}},
		{"a uid range", w11b02Msg(unix.AF_INET, 0, 0, 200, act, nlAttr(unix.FRA_UID_RANGE, make([]byte, 8))),
			Rule{Family: IPv4, Table: 200, Action: act, UID: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRule(tt.m)
			if err != nil || got != tt.want {
				t.Errorf("parseRule = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestW11b02ParseRuleRefusesMalformedMessages(t *testing.T) {
	for _, tt := range []struct {
		name string
		m    []byte
	}{
		{"shorter than the header", make([]byte, 4)},
		{"an attribute past the end", append(rtMsg(unix.AF_INET, 0, 0, 0), nlAttr(unix.FRA_TABLE, nlUint32(1))[:6]...)},
		{"a prefix longer than the family", w11b02Msg(unix.AF_INET, 40, 0, 200, 1, nlAttr(unix.FRA_DST, []byte{8, 8, 8, 8}))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseRule(tt.m); err == nil || !strings.Contains(err.Error(), "netstat: rule") {
				t.Errorf("parseRule = %v, want an error naming the rule", err)
			}
		})
	}
}

func TestW11b02RulesLive(t *testing.T) {
	rules, err := Rules(IPv4)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(r Rule) bool { return r.Priority == 32766 && r.Table == unix.RT_TABLE_MAIN })
	if i < 0 {
		t.Fatalf("no main rule at priority 32766 in %+v", rules)
	}
	if want := (Rule{Family: IPv4, Priority: 32766, Table: unix.RT_TABLE_MAIN, Action: unix.FR_ACT_TO_TBL}); rules[i] != want {
		t.Errorf("main rule = %+v, want %+v: it selects nothing", rules[i], want)
	}
	if _, err := Rules(IPv6); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run 'TestW11b02' ./internal/platform/netstat/`
Expected: FAIL with `undefined: parseRule`.

- [ ] **Step 3: Implement**

1. Create `rules.go` with `Rule` as Contracts states it, with a doc comment saying what a set field means.
2. `Rules(family Family) ([]Rule, error)`: `af` as in `Routes`; `retryDump` around `netlinkRequest(unix.RTM_GETRULE, unix.NLM_F_DUMP, rtMsg(af, 0, 0, 0))`, because `struct fib_rule_hdr` has the layout of `struct rtmsg`; wrap errors as `netstat: rule dump: %w`; parse every message.
3. `parseRule(m []byte) (Rule, error)`: refuse `len(m) < unix.SizeofRtMsg` with `netstat: rule message of %d bytes`. `Family` is `IPv6` when `m[0] == unix.AF_INET6`, else `IPv4`. `Table` starts as `uint32(m[4])` and `FRA_TABLE` (4 bytes) overrides it; `Action` is `m[7]`. Walk the attributes as `parseRoute` does, with `netstat: rule attribute of length %d in %d bytes` for a bad length. `FRA_DST` and `FRA_SRC` become prefixes of `m[1]` and `m[2]` bits via `addr.Prefix(bits)`, an error being `netstat: rule prefix %s/%d: %w`. `FRA_PRIORITY` (4 bytes) sets `Priority`; `FRA_FWMARK` and `FRA_FWMASK` set `Mark`; `FRA_IP_PROTO` (1 byte) sets `Proto`; `FRA_SPORT_RANGE`, `FRA_DPORT_RANGE` and `FRA_UID_RANGE` set their booleans. Other attributes are ignored.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -count=1 ./internal/platform/netstat/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/netstat/rules.go internal/platform/netstat/rules_test.go
git commit -m "feat(netstat): dump policy routing rules"
```

---

### Task 3: eval: one original-destination parser

**Files:**
- Modify: `internal/modules/firewall/eval/match.go` (`conntrackOption`)
- Modify: `internal/modules/firewall/eval/cells.go` (`InternetCells`, remove `parsePrefixOrAddr`)
- Test: `internal/modules/firewall/eval/cells_test.go`, `internal/modules/firewall/eval/match_test.go`

**Interfaces:**
- Produces: `origDstPrefix(s string) (netip.Prefix, bool)`, used only inside package `eval`

**Items:** 2026-11-06-v1-security-review#47

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Extend the test `internal/modules/firewall/eval/cells_test.go::TestV1r20InternetCells` and add the parser test**

Add these rows to the `tests` table of `TestV1r20InternetCells`. They pin branches that had no test and pass before the change:

```go
{"an IPv4-mapped prefix in an IPv6 call", v1r20Set(v1r20Dst("::ffff:8.8.8.0/120")), true, []string{v1r20Whole6}},
{"a prefix from the bottom of the space", v1r20Set(v1r20Dst("0.0.0.0/9")), false,
	[]string{"1.0.0.0-126.255.255.255", "128.0.0.0-223.255.255.255"}},
{"a conntrack original destination without a value", v1r20Set(netfilter.Rule{Matches: []netfilter.Match{{Module: "conntrack",
	Options: []netfilter.Option{{Name: "ctorigdst"}}}}}), false, []string{v1r20Whole4}},
```

Append to `internal/modules/firewall/eval/match_test.go`:

```go
func TestW11b03OrigDstPrefix(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want netip.Prefix
		ok   bool
	}{
		{"9.9.9.0/24", netip.MustParsePrefix("9.9.9.0/24"), true},
		{"9.9.9.9", netip.MustParsePrefix("9.9.9.9/32"), true},
		{"2606:4700::/32", netip.MustParsePrefix("2606:4700::/32"), true},
		{"2606:4700::1", netip.MustParsePrefix("2606:4700::1/128"), true},
		{"9.9.9.9-9.9.9.20", netip.Prefix{}, false},
		{"", netip.Prefix{}, false},
	} {
		if got, ok := origDstPrefix(tt.in); got != tt.want || ok != tt.ok {
			t.Errorf("origDstPrefix(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestW11b03|TestV1r20InternetCells' ./internal/modules/firewall/eval/`
Expected: FAIL with `undefined: origDstPrefix`.

- [ ] **Step 3: Implement**

1. In `match.go` add `origDstPrefix`: `netip.ParsePrefix`, else `netip.ParseAddr` as a full-length prefix, else `netip.Prefix{}, false`.
2. `conntrackOption`'s `ctorigdst` case calls it, and keeps the refusal text `original destination <quoted value> is not an address` on `false`.
3. `InternetCells` calls `addPrefix` only when `origDstPrefix` answers true. Delete `parsePrefixOrAddr`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/eval/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/eval/match.go internal/modules/firewall/eval/cells.go internal/modules/firewall/eval/cells_test.go internal/modules/firewall/eval/match_test.go
git commit -m "refactor(firewall): share the origdst parser"
```

---

### Task 4: Wildcard binds on every address

**Files:**
- Modify: `internal/modules/firewall/guard/route.go` (`wildcardDsts`, its call in `probes`)
- Test: `internal/modules/firewall/guard/route_test.go`, `internal/modules/firewall/guard/internet_cells_test.go`, `internal/modules/firewall/guard/guard_test.go`

**Interfaces:**
- Produces: `wildcardDsts(pref netip.Addr) []netip.Addr` (Contracts "Guard internals")

**Items:** 2026-11-06-v1-security-review#27, 2026-11-06-v1-security-review#28

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Extend the four stale tests and rewrite the wildcard test**

In `route_test.go::TestProbes`, add beside `panelResolved`:

```go
panelOther := flPkt("input", "203.0.113.7", 51000, "10.0.0.1", 443, "tcp")
```

Replace the `want` of the row "an unspecified destination is the source the host answers from" with:

```go
want: []probe{
	rtProbe("panel from 203.0.113.7 to 192.0.2.1:443 on eth0", panelResolved, "eth0", ""),
	rtProbe("panel from 203.0.113.7 to 192.0.2.1:443 on eth1", panelResolved, "eth1", ""),
	rtProbe("panel from 203.0.113.7 to 10.0.0.1:443 on eth0", panelOther, "eth0", ""),
	rtProbe("panel from 203.0.113.7 to 10.0.0.1:443 on eth1", panelOther, "eth1", ""),
},
```

Replace the row "every lookup of an incoming flow carries the packet's mark" with:

```go
{
	name: "every lookup of an incoming flow carries the packet's mark",
	fl:   flow{label: "panel", pkt: gxMark(panel, 0x10)}, f: rtFacts(1, 0, 0),
	routes: map[netstat.RouteQuery]netstat.Route{
		gxQueryMark(rtQuery("203.0.113.7", "", ""), 0x10):          rtVia("eth0", "192.0.2.1"),
		gxQueryMark(rtQuery("203.0.113.7", "192.0.2.1", ""), 0x10): rtVia("eth0", "192.0.2.1"),
		gxQueryMark(rtQuery("203.0.113.7", "10.0.0.1", ""), 0x10):  rtVia("eth0", "192.0.2.1"),
	},
	want: []probe{
		rtProbe("panel from 203.0.113.7 to 192.0.2.1:443 on eth0", gxMark(panelResolved, 0x10), "eth0", ""),
		rtProbe("panel from 203.0.113.7 to 10.0.0.1:443 on eth0", gxMark(panelOther, 0x10), "eth0", ""),
	},
	lookups: []netstat.RouteQuery{
		gxQueryMark(rtQuery("203.0.113.7", "", ""), 0x10),
		gxQueryMark(rtQuery("203.0.113.7", "192.0.2.1", ""), 0x10),
		gxQueryMark(rtQuery("203.0.113.7", "10.0.0.1", ""), 0x10),
	},
},
```

In `guard_test.go::TestGxCheckControllerListeners`, replace the `want` and the length check with:

```go
want := core.Guard{ID: "firewall.lockout", Message: "the change would cut " +
	"controller listener from 1.0.0.0-223.255.255.255 to 192.0.2.1:7443 on eth0: drop (filter/ufw-user-input#1); " +
	"controller listener from 1.0.0.0-223.255.255.255 to 192.0.2.1:7443 on eth1: drop (filter/ufw-user-input#1); " +
	"controller listener from 1.0.0.0-223.255.255.255 to 10.0.0.1:7443 on eth0: drop (filter/ufw-user-input#1); " +
	"controller listener from 1.0.0.0-223.255.255.255 to 10.0.0.1:7443 on eth1: drop (filter/ufw-user-input#1)"}
if g != want || len(results) != 4 {
```

In `internet_cells_test.go::TestV1r21ThrowCellWithoutARouteBackHasNothingToProtect`, change the second row's rule to `"-A INPUT -s 45.33.0.0/16 -d 10.0.0.1/32 -p tcp -j DROP"`, because a rule on `10.0.0.1` alone now blocks the first cell. Its `want` stays.

Replace `TestV1r21WildcardDestinationsSkipIPv6LinkLocal` with:

```go
func TestV1r21WildcardDestinations(t *testing.T) {
	iface := func(index int, name string, up bool, addrs ...string) netstat.Interface {
		i := netstat.Interface{Index: index, Name: name, Up: up, Running: up}
		for _, a := range addrs {
			i.Addrs = append(i.Addrs, netip.MustParsePrefix(a))
		}
		return i
	}
	lo := iface(1, "lo", true, "127.0.0.1/8", "::1/128")
	lo.Loopback = true
	for _, tt := range []struct {
		name, pref string
		ifaces     []netstat.Interface
		want       []string
	}{
		{"IPv6 skips link-local addresses", "2001:db8::1",
			[]netstat.Interface{lo, iface(2, "eth0", true, "192.0.2.1/24", "fe80::1/64", "2001:db8::1/64", "2001:db8::2/64")},
			[]string{"2001:db8::1", "2001:db8::2"}},
		{"IPv4 skips link-local addresses", "192.0.2.1",
			[]netstat.Interface{lo, iface(2, "eth0", true, "192.0.2.1/24", "169.254.7.7/16")},
			[]string{"192.0.2.1"}},
		{"every up interface counts", "10.0.0.10",
			[]netstat.Interface{lo, iface(2, "eth0", true, "10.0.0.10/24"), iface(3, "eth1", true, "10.1.0.10/24")},
			[]string{"10.0.0.10", "10.1.0.10"}},
		{"the preferred address comes first", "10.1.0.10",
			[]netstat.Interface{lo, iface(2, "eth0", true, "10.0.0.10/24"), iface(3, "eth1", true, "10.1.0.10/24")},
			[]string{"10.1.0.10", "10.0.0.10"}},
		{"a down interface adds nothing", "10.0.0.10",
			[]netstat.Interface{lo, iface(2, "eth0", true, "10.0.0.10/24"), iface(3, "eth1", false, "10.1.0.10/24")},
			[]string{"10.0.0.10"}},
		{"an address on two interfaces counts once", "10.0.0.10",
			[]netstat.Interface{lo, iface(2, "eth0", true, "10.0.0.10/24"), iface(3, "eth1", true, "10.0.0.10/24")},
			[]string{"10.0.0.10"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(&gHost{}, facts{ifaces: tt.ifaces}, fwstate.Loaded{})
			var want []netip.Addr
			for _, a := range tt.want {
				want = append(want, netip.MustParseAddr(a))
			}
			if got := r.wildcardDsts(netip.MustParseAddr(tt.pref)); !slices.Equal(got, want) {
				t.Errorf("wildcardDsts = %v, want %v", got, want)
			}
		})
	}
}
```

Add after `TestV1r21WildcardBindIsCheckedOnEveryAddress`:

```go
func TestW11b04WildcardBindCoversEveryUplink(t *testing.T) {
	check := func(before, st fwstate.Loaded) core.Guard {
		t.Helper()
		h := v1r21TwoAddrs{&gHost{ifaces: []netstat.Interface{
			{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
			{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("10.0.0.10/24")}},
			{Index: 3, Name: "eth1", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("10.1.0.10/24")}},
			{Index: 6, Name: "dp-panel", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/16")}},
		}}}
		plan := &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm",
			ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"),
			Public: []netip.AddrPort{netip.MustParseAddrPort("0.0.0.0:443")}}}
		env, _, _ := ckEnv()
		ckRunning(env)
		g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Before: &before, Eval: eval.Evaluate})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	both := v1r21State(t, v1r21Bindings("10.0.0.10", "10.1.0.10"), v1r21Table())
	const want = "exposure exp1 public from 1.0.0.0-9.255.255.255 to 10.1.0.10:443 on eth0: drop"
	if g := check(both, v1r21State(t, v1r21Bindings("10.0.0.10"), v1r21Table())); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q: the second uplink's allowance is gone", g, want)
	}
	if g := check(both, both); !g.OK {
		t.Errorf("guard = %+v, want OK with both allowances kept", g)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 ./internal/modules/firewall/guard/`
Expected: FAIL, a build error at `r.wildcardDsts(netip.MustParseAddr(tt.pref))`: `not enough arguments in call`.

- [ ] **Step 3: Implement**

1. `wildcardDsts(pref netip.Addr) []netip.Addr`: `pref` first, then in interface order every address of `pref`'s family on every interface with `Up && !Loopback` (the filter `eligible` uses), skipping `IsLinkLocalUnicast()` in both families and any address already in the list. Rewrite the doc comment: the destinations are the addresses an upstream translation or a source-based route may deliver to.
2. In `probes`, call it with `rt.PrefSrc.Unmap()`. Keep the route-back lookup and both its failure paths unchanged, and update the doc comment of `probes` if it names the interface.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/route.go internal/modules/firewall/guard/route_test.go internal/modules/firewall/guard/internet_cells_test.go internal/modules/firewall/guard/guard_test.go
git commit -m "fix(firewall): check wildcard binds on all addresses"
```

---

### Task 5: Every forwarded reply, and the reply tests

**Files:**
- Modify: `internal/modules/firewall/guard/flows.go` (`flow`, `flowSet.add`, `viaFlows`)
- Modify: `internal/modules/firewall/guard/route.go` (`noReply`)
- Test: `internal/modules/firewall/guard/flows_test.go`, `internal/modules/firewall/guard/route_test.go`, `internal/modules/firewall/guard/internet_cells_test.go`

**Interfaces:**
- Produces: `flow.fwdReplies` (Contracts "Guard internals")

**Items:** 2026-11-06-v1-security-review#28

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 2 = 6

- [ ] **Step 1: Write the failing tests**

In `flows_test.go::TestBuildFlows`, change the four rows of "a client that came through an exposure is not a direct panel flow" from `fwdReply: npm.ProxyAddr` to `fwdReplies: []netip.Addr{npm.ProxyAddr}`. Append to `flows_test.go`:

```go
func TestW11b05FlowSetKeepsEveryForwardedReply(t *testing.T) {
	a, b := netip.MustParseAddr("172.30.0.2"), netip.MustParseAddr("172.30.0.3")
	pkt := flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp")
	s := &flowSet{seen: map[flowKey]int{}}
	s.add(flow{label: "first", pkt: pkt, wild: wildSrc, fwdReplies: []netip.Addr{a}})
	s.add(flow{label: "second", pkt: pkt, wild: wildSrc, fwdReplies: []netip.Addr{b}})
	s.add(flow{label: "third", pkt: pkt, wild: wildSrc, fwdReplies: []netip.Addr{a}})
	s.add(flow{label: "fourth", pkt: pkt, wild: wildSrc})
	if len(s.flows) != 1 || !slices.Equal(s.flows[0].fwdReplies, []netip.Addr{a, b}) {
		t.Errorf("flows = %+v, want one flow with the forwarded replies %v and %v", s.flows, a, b)
	}
}
```

Add `"slices"` to that file's imports if it is absent. Append to `route_test.go`:

```go
func TestW11b05NoReplyChecksEveryForwardedReply(t *testing.T) {
	src, from := netip.MustParseAddr("45.33.0.0"), netip.MustParseAddr("192.0.2.1")
	a, b := netip.MustParseAddr("172.30.0.2"), netip.MustParseAddr("172.30.0.3")
	fl := flow{pkt: flPkt("input", "45.33.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc, fwdReplies: []netip.Addr{a, b}}
	for _, tt := range []struct {
		name   string
		routes map[netstat.RouteQuery]netstat.Route
		want   bool
	}{
		{"neither reply has a route", nil, true},
		{"the first reply has a route", map[netstat.RouteQuery]netstat.Route{rtQuery("45.33.0.0", "172.30.0.2", "dp-panel"): rtVia("eth0", "")}, false},
		{"the second reply has a route", map[netstat.RouteQuery]netstat.Route{rtQuery("45.33.0.0", "172.30.0.3", "dp-panel"): rtVia("eth0", "")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(&gHost{routes: tt.routes}, facts{ifaces: gIfaces()}, fwstate.Loaded{})
			if got := r.noReply(fl, src, 0, from); got != tt.want {
				t.Errorf("noReply = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestW11b05NoReplyFrom(t *testing.T) {
	deadline := fmt.Errorf("netstat: route lookup: %w", os.ErrDeadlineExceeded)
	v4Only := []netstat.Interface{{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}}}
	for _, tt := range []struct {
		name      string
		src       string
		ifaces    []netstat.Interface
		routes    map[netstat.RouteQuery]netstat.Route
		routeErrs map[netstat.RouteQuery]error
		want      bool
	}{
		{name: "no address of the host has a route", src: "45.33.0.1", ifaces: gIfaces(), want: true},
		{name: "one address has a route", src: "45.33.0.1", ifaces: gIfaces(),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("45.33.0.1", "10.0.0.1", ""): rtVia("eth1", "")}},
		{name: "a lookup that fails for another reason", src: "45.33.0.1", ifaces: gIfaces(),
			routeErrs: map[netstat.RouteQuery]error{rtQuery("45.33.0.1", "10.0.0.1", ""): deadline}},
		{name: "no address of the source's family", src: "2606:4700::1", ifaces: v4Only},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &gHost{routes: tt.routes, routeErrs: tt.routeErrs}
			r := newRouter(h, facts{ifaces: tt.ifaces}, fwstate.Loaded{})
			fl := flow{pkt: flPkt("input", tt.src, 0, tt.src, 443, "tcp"), wild: wildSrc}
			if got := r.noReplyFrom(fl, netip.MustParseAddr(tt.src), 0); got != tt.want {
				t.Errorf("noReplyFrom = %v, want %v", got, tt.want)
			}
		})
	}
}
```

Replace `internet_cells_test.go::TestV1r21ThrowCellWhoseLookupFailsIsUnknown` with:

```go
func TestV1r21ThrowCellWhoseLookupFailsIsUnknown(t *testing.T) {
	// The tunnel's cell in the range is unknown as well, so only the
	// listener's own reason shows that its cell was not skipped.
	const want = "controller listener from 45.33.0.0-45.33.255.255 to 0.0.0.0:443: unknown (the route back to 45.33.0.0 cannot be resolved"
	for _, fail := range []error{errors.New("netstat: invalid argument"), fmt.Errorf("netstat: route lookup: %w", unix.EINVAL)} {
		g := v1r21ThrowListener(t, v1r21Throw{fail: fail}, v1r21State(t, v1r21Table(), v1r21Table()))
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("lookup failing with %v: guard = %+v, want a refusal containing %q: outside a blackhole only a missing route skips a cell", fail, g, want)
		}
	}
}
```

The EINVAL case and `TestW11b05NoReplyFrom` pin behaviour already present. They fail on the build until `fwdReplies` exists.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestW11b05|TestBuildFlows|TestV1r21ThrowCell' ./internal/modules/firewall/guard/`
Expected: FAIL with `unknown field fwdReplies`.

- [ ] **Step 3: Implement**

1. `flow`: replace `fwdReply netip.Addr` by `fwdReplies []netip.Addr` with the same comment in the plural.
2. `flowSet.add`: on a duplicate, append each of `f.fwdReplies` that the kept flow does not hold. Rewrite the doc comment of `flowSet`: checking every reply only skips fewer cells.
3. `viaFlows`: `fwdReplies: []netip.Addr{v.ProxyAddr}` for now (Task 9 narrows it by family).
4. `noReply`: after the host's own reply, loop over `fl.fwdReplies`, returning false when `!noRoute(fl, err)` for any of them, then return true. Update its doc comment.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/guard/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/flows.go internal/modules/firewall/guard/route.go internal/modules/firewall/guard/flows_test.go internal/modules/firewall/guard/route_test.go internal/modules/firewall/guard/internet_cells_test.go
git commit -m "fix(firewall): keep every forwarded reply of a flow"
```

---

### Task 6: Cells: limit, local routes, rewrites, longest match

**Files:**
- Modify: `internal/modules/firewall/guard/cells.go` (`cellFlows`, `routeCut`, `routeCuts`, `cutRouteTypes`, `routeCutters`, `cutters`)
- Create: `internal/modules/firewall/guard/cells_test.go`
- Test: `internal/modules/firewall/guard/internet_cells_test.go` (`TestV1r21RouteCuttersKeepOnlyTheirFamily`)

**Interfaces:**
- Produces: `routeCutters` returning `routeCut` with `paths`; `overCellLimit`; the cell-limit refusal text (Contracts)

**Items:** 2026-11-06-v1-security-review#28, 2026-11-06-v1-security-review#48, 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

In `internet_cells_test.go::TestV1r21RouteCuttersKeepOnlyTheirFamily`, replace the call and the loop header with:

```go
c := routeCutters(routes, true)
var dsts []netip.Prefix
for _, rule := range c.rs.Table("routes").Chain("routes").Rules {
```

and use `c.noPath` and `c.blackholes` in its assertion. `internal/modules/firewall/guard/cells_test.go`:

```go
package guard

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func TestW11b06OverCellLimit(t *testing.T) {
	for _, tt := range []struct {
		cells, rules, limit int
		over                bool
	}{
		{65536, 0, 0, false},
		{65537, 0, 65536, true},
		{1000, 9999, 0, false},
		{1000, 10000, 10000000, true},
		{65536, 152, 10000000, true},
		{65537, 152, 65536, true},
	} {
		if limit, over := overCellLimit(tt.cells, tt.rules); limit != tt.limit || over != tt.over {
			t.Errorf("overCellLimit(%d, %d) = %d, %v; want %d, %v", tt.cells, tt.rules, limit, over, tt.limit, tt.over)
		}
	}
}

func TestW11b06TooManyCellsAreUnknown(t *testing.T) {
	g, lookups := v1r21Listener(t, v1r21Table(v1r21Bans(2300, "INPUT", " -p tcp -m tcp --dport 443")...))
	const want = "controller listener: unknown (the rules and routes split the internet into 4601 ranges for this flow; " +
		"checking them against 2300 rules is more than the guard evaluates (limit 10000000))"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
	if lookups != 0 {
		t.Errorf("%d route lookups, want none: a flow over the limit is not evaluated", lookups)
	}
}

func TestW11b06LocalRouteInMainCutsCells(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A INPUT -s 172.30.0.3/32 -i dp-panel -p tcp -m tcp --dport 443 -j DROP"), v1r21Table())
	check := func(table []netstat.Route) (msg string, ok bool) {
		t.Helper()
		g := v1r21CheckHost(t, v1r21Fib{&gHost{ifaces: ckIfaces(), table: table}}, st, v1r21Tunnel())
		return g.Message, g.OK
	}
	const want = "exposure exp2 tunnel from 172.30.0.3 to 45.0.0.0-45.255.255.255:443 on dp-panel: drop"
	if msg, ok := check(append(v1r21HostRoutes(), v1r21Route("local", "45.0.0.0/8", "lo", "-", 254))); ok || !strings.Contains(msg, want) {
		t.Errorf("guard: %v %q, want a refusal containing %q: the range is delivered locally, where the rule drops it", ok, msg, want)
	}
	if msg, ok := check(v1r21HostRoutes()); !ok {
		t.Errorf("guard: %q, want OK: without the local route no cell is delivered locally", msg)
	}
}

func TestW11b06CuttersKeepEveryRuleUnderAnUnmodelledRewrite(t *testing.T) {
	const ban = "-A INPUT -s 8.8.8.8/32 -p tcp -m tcp --dport 8443 -j DROP"
	for _, tt := range []struct {
		name, nat string
		kept      bool
	}{
		{"REDIRECT", "-A PREROUTING -p tcp -m tcp --dport 443 -j REDIRECT --to-ports 8443", true},
		{"a DNAT to another port", "-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 192.0.2.1:9443", false},
		{"MASQUERADE", "-A POSTROUTING -j MASQUERADE", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := v1r21State(t, v1r21Jail("22", []string{ban}, tt.nat), v1r21Table())
			rules := cutters(st.V4, false, "tcp", 443).Table("filter").Chain("INPUT").Rules
			got := slices.ContainsFunc(rules, func(r netfilter.Rule) bool {
				return r.Source.Prefix == netip.MustParsePrefix("8.8.8.8/32")
			})
			if got != tt.kept {
				t.Errorf("the ban on 8443 kept = %v, want %v, in %+v", got, tt.kept, rules)
			}
		})
	}
}

func TestW11b06LongestRouteDecidesACell(t *testing.T) {
	type mark struct{ noPath, blackhole bool }
	const wide, narrow = "45.9.0.0/16", "45.9.1.0/24"
	route := func(typ, dst string) netstat.Route { return v1r21Route(typ, dst, "", "-", 254) }
	for _, tt := range []struct {
		name         string
		routes       []netstat.Route
		rest, inside mark // the /16 outside the /24, and the /24
	}{
		{"a unicast route inside a blackhole", []netstat.Route{route("blackhole", wide), route("unicast", narrow)}, mark{true, true}, mark{false, false}},
		{"a unicast route inside an unreachable route", []netstat.Route{route("unreachable", wide), route("unicast", narrow)}, mark{true, false}, mark{false, false}},
		{"a local route inside a blackhole", []netstat.Route{route("blackhole", wide), route("local", narrow)}, mark{true, true}, mark{false, false}},
		{"an unreachable route inside a blackhole", []netstat.Route{route("blackhole", wide), route("unreachable", narrow)}, mark{true, true}, mark{true, false}},
		{"a blackhole inside a unicast route", []netstat.Route{route("unicast", wide), route("blackhole", narrow)}, mark{false, false}, mark{true, true}},
		{"a blackhole inside an unreachable route", []netstat.Route{route("unreachable", wide), route("blackhole", narrow)}, mark{true, false}, mark{true, true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(&gHost{table: tt.routes}, facts{ifaces: ckIfaces()}, fwstate.Loaded{})
			fl := flow{label: "x", pkt: flPkt("input", "0.0.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc}
			got := map[string]mark{}
			for _, cf := range r.cellFlows(fl, nil) {
				got[cf.pkt.Src.String()] = mark{cf.standIn, cf.blackhole}
			}
			for rep, want := range map[string]mark{"45.9.0.0": tt.rest, "45.9.1.0": tt.inside, "45.9.2.0": tt.rest} {
				if got[rep] != want {
					t.Errorf("cell at %s = %+v, want %+v", rep, got[rep], want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestW11b06|TestV1r21RouteCutters' ./internal/modules/firewall/guard/`
Expected: FAIL, a build error naming `undefined: overCellLimit` or the `routeCutters` assignment mismatch.

- [ ] **Step 3: Implement**

1. **Limit.** Add `overCellLimit` with named constants for 65536 and 10_000_000, and a helper that counts the rules of every chain of every table of a ruleset (zero for nil). In `cellFlows`, right after `eval.InternetCells`, when the cell count and the rule count of `rs` are over the limit, set `fl.unknown` to the Contracts text with the cell count, the rule count and the limit, and return `[]flow{fl}`. The doc comment says why the check fails closed: every cell is evaluated against the whole ruleset, so an unbounded check would hold the request.
2. **Local routes.** Add `"local"` to `cutRouteTypes`, keeping the exclusion of `unix.RT_TABLE_LOCAL`.
3. **`routeCutters` returns `routeCut`.** Unicast and local routes outside the local table go to `paths`; the others to `noPath`, and blackholes also to `blackholes`. `routeCuts` assigns its result, keeping `why` semantics.
4. **Longest match.** In `cellFlows`, replace `within` by a longest-prefix comparison of the cell's representative: a cell is no-path when a no-path route contains it and no longer path route does. It is blackhole when a blackhole route contains it and no longer route of any other type does. A helper returning the longest containing prefix length (-1 for none) serves both; the no-blackhole no-path routes are the entries of `noPath` not in `blackholes`. Keep the rest of the loop. Update the doc comments of `cellFlows`, `routeCut` and `routeCutters`.
5. **Rewrites.** In `cutters`, before the table loop, compute whether any rule of any table has a target that is not a chain jump (`rule.Target.Chain`) and not one of `""`, `ACCEPT`, `DROP`, `REJECT`, `RETURN`, `LOG`, `TCPMSS`, `MARK`, `CONNMARK`, `DNAT`, `SNAT`, `MASQUERADE`. When so, `mayMatch` is not consulted and every rule of a reached chain is kept. Extend the doc comment: an unmodelled target may rewrite the packet in a way the port filter cannot see.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/cells.go internal/modules/firewall/guard/cells_test.go internal/modules/firewall/guard/internet_cells_test.go
git commit -m "fix(firewall): bound cells and match longest routes"
```

---

### Task 7: Before-state router and skipped probes

**Files:**
- Modify: `internal/modules/firewall/guard/route.go` (add `forState`)
- Modify: `internal/modules/firewall/guard/cells.go` (`checkFlow`)
- Modify: `internal/modules/firewall/guard/guard.go` (`Check`, `verdict`)
- Test: `internal/modules/firewall/guard/guard_test.go` (`TestVerdict`), `internal/modules/firewall/guard/cells_test.go`

**Interfaces:**
- Consumes: `routeCuts` caching (Task 6)
- Produces: `forState`, `checkFlow`'s skipped count, `verdict(results, skipped)` (Contracts)

**Items:** 2026-11-06-v1-security-review#48

**Implementer:** dforge:impl-opus-high
**Executor:** agy gemini-4-argon / high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 2 = 6

- [ ] **Step 1: Extend the test `internal/modules/firewall/guard/guard_test.go::TestVerdict` and add the before-state tests**

In `TestVerdict`, change `verdict(nil)` to `verdict(nil, 0)`, add `, 0` to the second call, and append before the closing brace:

```go
accepted := []FlowResult{{Flow: "a", Result: fwstate.Result{Verdict: fwstate.Accept}}}
for _, tt := range []struct {
	results []FlowResult
	skipped int
	want    string
}{
	{accepted, 0, "every protected flow passes (1 checked)"},
	{accepted, 2, "every protected flow passes (1 checked, 2 already blocked before the change)"},
	{nil, 3, "every protected flow passes (0 checked, 3 already blocked before the change)"},
} {
	if g := verdict(tt.results, tt.skipped); !g.OK || g.Message != tt.want {
		t.Errorf("verdict(%d results, %d skipped) = %+v, want OK with %q", len(tt.results), tt.skipped, g, tt.want)
	}
}
```

Append to `cells_test.go`, adding the imports `"context"`, `"github.com/darkraise/darkpanel/internal/core"` and `"github.com/darkraise/darkpanel/internal/modules/firewall/eval"`:

```go
func TestW11b07BeforeStateIsRoutedWithItsOwnForwarding(t *testing.T) {
	const v4 = "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 172.30.0.2:443\nCOMMIT\n"
	const drop = "-A FORWARD -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
	after := v1r21State(t, v4+v1r21Table(drop), v1r21Table())
	check := func(sysctl map[string]string) core.Guard {
		t.Helper()
		before := v1r21State(t, v4+v1r21Table(drop), v1r21Table())
		before.Sysctl = sysctl
		env, _, _ := ckEnv()
		ckRunning(env)
		h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: v1r21HostRoutes()}}
		g, _, err := Checker{Host: h}.Check(context.Background(), env, after, Input{Plan: v1r21Public(), Before: &before, Eval: eval.Evaluate})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := check(nil); !g.OK {
		t.Errorf("guard = %+v, want OK: the loaded rules already drop the forwarded flow", g)
	}
	const want = "exposure exp1 public from"
	if g := check(map[string]string{"net/ipv4/ip_forward": "0"}); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q: the before-state does not forward, so it cannot decide the flow", g, want)
	}
}

func TestW11b07ForStateSharesTheCaches(t *testing.T) {
	h := &gxDumpHost{gHost: &gHost{}}
	r := newRouter(h, facts{}, fwstate.Loaded{})
	r.routeCuts(false)
	c := r.forState(fwstate.Loaded{Variant: "other"})
	c.routeCuts(false)
	if len(h.dumps) != 1 || c.st.Variant != "other" || r.st.Variant != "" {
		t.Errorf("%d dumps, copy state %q, original state %q; want 1 dump and only the copy changed", len(h.dumps), c.st.Variant, r.st.Variant)
	}
}

func TestW11b07ProbesAlreadyBlockedAreCounted(t *testing.T) {
	const ban = "-A INPUT -s 8.8.8.0/24 -p tcp -j DROP"
	st := v1r21State(t, v1r21Table(ban), v1r21Table())
	g := v1r21CheckFrom(t, &gHost{ifaces: ckIfaces()}, &st, st, v1r21Public())
	const want = "every protected flow passes (4 checked, 3 already blocked before the change)"
	if !g.OK || g.Message != want {
		t.Errorf("guard = %+v, want OK with %q", g, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestW11b07|TestVerdict' ./internal/modules/firewall/guard/`
Expected: FAIL, a build error at `verdict(nil, 0)`: `too many arguments`.

- [ ] **Step 3: Implement**

1. Add `forState` as Contracts states it: `c := *r; c.st = st; return &c`.
2. `checkFlow`: returns `(results []FlowResult, skipped int)`, counting every probe that the before-state already blocked, across all cells. Evaluate those with `r.forState(*before)` and `*before`, so `forwardingOff` and `rpFilter` read the before-state's sysctl values.
3. `Check` sums the counts and passes them to `verdict`.
4. `verdict(results []FlowResult, skipped int)`: `no protected flow to check` only when `len(results) == 0 && skipped == 0`; the two pass texts of Contracts otherwise. Update the doc comments of `checkFlow` and `verdict`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/route.go internal/modules/firewall/guard/cells.go internal/modules/firewall/guard/guard.go internal/modules/firewall/guard/guard_test.go internal/modules/firewall/guard/cells_test.go
git commit -m "fix(firewall): route the before-state by its sysctls"
```

---

### Task 8: Policy rules in the guard

**Files:**
- Modify: `internal/modules/firewall/guard/routes.go` (`ruleDumper`, `LiveHost.Rules`)
- Modify: `internal/modules/firewall/guard/cells.go` (`routeCut`, `routeCuts`)
- Modify: `internal/modules/firewall/guard/route.go` (`probes`, `noReply`, `noReplyFrom`, `eligible`, `marksReplies`, `route`)
- Create: `internal/modules/firewall/guard/policy_test.go`
- Test: `internal/modules/firewall/guard/facts_test.go` (`gHost`), `internal/modules/firewall/guard/routes_test.go`, `internal/modules/firewall/guard/internet_cells_test.go` (`v1r21Throw`, `v1r21ThrowListener`, `TestV1r21ThrowCellWithMarkedRepliesIsUnknown`)

**Interfaces:**
- Consumes: `netstat.Rule`, `netstat.Rules`, the `RouteQuery` selectors (Tasks 1, 2); `routeCut` (Task 6); `fwdReplies` (Task 5)
- Produces: `ruleDumper`, `LiveHost.Rules`, `gHost.rules`, `routeCut.rules`, `selectors`, the unreadable-rules text (Contracts)

**Items:** 2026-11-06-v1-security-review#28, 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

In `facts_test.go`, add the field `rules []netstat.Rule` to `gHost` and:

```go
func (h *gHost) Rules(family netstat.Family) ([]netstat.Rule, error) {
	var out []netstat.Rule
	for _, r := range h.rules {
		if r.Family == family {
			out = append(out, r)
		}
	}
	return out, nil
}
```

In `routes_test.go`, add `_ ruleDumper = LiveHost{}` to the `var` block.

In `internet_cells_test.go`, add the field `policy []netstat.Rule` to `v1r21Throw` (its doc comment: "policy holds the host's policy rules"), and in `v1r21ThrowListener` add `rules: h.policy` to the `gHost` it builds. Replace `TestV1r21ThrowCellWithMarkedRepliesIsUnknown` with:

```go
func TestV1r21ThrowCellWithMarkedRepliesIsUnknown(t *testing.T) {
	const rule = "-A INPUT -s 45.33.0.0/16 -p tcp --dport 443 -j DROP"
	mangle := func(rules ...string) string {
		return "*mangle\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
			strings.Join(rules, "\n") + "\nCOMMIT\n"
	}
	raw := func(rules ...string) string {
		return "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
	}
	policy := func(r netstat.Rule) []netstat.Rule {
		r.Family, r.Priority, r.Table, r.Action = netstat.IPv4, 100, 200, unix.FR_ACT_TO_TBL
		return []netstat.Rule{r}
	}
	byMark := policy(netstat.Rule{Mark: true})
	tcpMark := mangle("-A OUTPUT -j CONNMARK --restore-mark")
	for _, tt := range []struct {
		name, tables string
		filter       []string
		policy       []netstat.Rule
		unknown      bool
	}{
		{name: "a mangle mark a policy rule selects", tables: tcpMark, policy: byMark, unknown: true},
		{name: "a raw mark a policy rule selects", tables: raw("-A OUTPUT -j MARK --set-xmark 0x40000/0xffffffff"), policy: byMark, unknown: true},
		{name: "a mark no policy rule selects", tables: tcpMark},
		{name: "a policy mark rule and no mark set", policy: byMark},
		// wg-quick marks only UDP.
		{name: "UDP marks only", tables: mangle("-A PREROUTING -p udp -j CONNMARK --restore-mark --nfmask 0xffffffff --ctmask 0xffffffff",
			"-A POSTROUTING -p udp -m mark --mark 0xca6c -j CONNMARK --save-mark --nfmask 0xffffffff --ctmask 0xffffffff"), policy: byMark},
		// Tailscale's ts-forward marks every forwarded packet in filter.
		{name: "a mark in the filter table", filter: []string{"-A FORWARD -j MARK --set-xmark 0x40000/0xff0000"}, policy: byMark},
		{name: "a destination port selector", policy: policy(netstat.Rule{DstPort: true}), unknown: true},
		{name: "a uid selector", policy: policy(netstat.Rule{UID: true}), unknown: true},
		{name: "a protocol selector, which lookups carry", policy: policy(netstat.Rule{Proto: unix.IPPROTO_TCP})},
		{name: "a source port selector, which lookups carry", policy: policy(netstat.Rule{SrcPort: true})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := v1r21State(t, tt.tables+v1r21Table(append([]string{rule}, tt.filter...)...), v1r21Table())
			g := v1r21ThrowListener(t, v1r21Throw{policy: tt.policy}, st)
			if !tt.unknown {
				if !g.OK {
					t.Errorf("guard = %+v, want OK: nothing routes a reply to the thrown range", g)
				}
				return
			}
			for _, want := range []string{"controller listener from 45.33.0.0-45.33.255.255", "cannot be resolved"} {
				if g.OK || !strings.Contains(g.Message, want) {
					t.Errorf("guard = %+v, want a refusal containing %q: the selector may route replies to the range the rule drops", g, want)
				}
			}
		})
	}
}
```

`internal/modules/firewall/guard/policy_test.go`:

```go
package guard

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func w11b08Rule(fam netstat.Family, r netstat.Rule) []netstat.Rule {
	r.Family, r.Priority, r.Table, r.Action = fam, 100, 200, unix.FR_ACT_TO_TBL
	return []netstat.Rule{r}
}

// w11b08Selected is q with the selectors of a tcp packet between the ports.
func w11b08Selected(q netstat.RouteQuery, proto uint8, sport, dport uint16) netstat.RouteQuery {
	q.Proto, q.SrcPort, q.DstPort = proto, sport, dport
	return q
}

func TestW11b08ProbeLookupsCarrySelectorsOnlyUnderAPolicyRule(t *testing.T) {
	link := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	tunnel := flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "198.51.100.1", 7844, "udp"))
	linkQ := rtQuery("198.51.100.9", "192.0.2.1", "")
	tunnelQ := rtQuery("198.51.100.1", "172.30.0.3", "dp-panel")
	for _, tt := range []struct {
		name  string
		fl    flow
		rules []netstat.Rule
		want  netstat.RouteQuery
	}{
		{"no policy rule", flow{label: "l", pkt: link}, nil, linkQ},
		{"a mark rule selects by no protocol or port", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{Mark: true}), linkQ},
		{"a rule of the other family", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv6, netstat.Rule{Proto: 6}), linkQ},
		{"a protocol rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a source port rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{SrcPort: true}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a destination port rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{DstPort: true}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a forwarded flow", flow{label: "t", pkt: tunnel, standIn: true}, w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 17}), w11b08Selected(tunnelQ, 17, 0, 7844)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &gHost{ifaces: gIfaces(), routes: map[netstat.RouteQuery]netstat.Route{tt.want: rtVia("eth0", "192.0.2.1")}, rules: tt.rules}
			r := newRouter(h, rtFacts(0, 0, 0), fwstate.Loaded{})
			r.probes(tt.fl)
			if !reflect.DeepEqual(h.lookups, []netstat.RouteQuery{tt.want}) {
				t.Errorf("lookups = %+v, want %+v", h.lookups, tt.want)
			}
		})
	}
}

func TestW11b08RouteCarriesSelectors(t *testing.T) {
	proto := w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6})
	fwd := flOn("eth0", flPkt("input", "198.51.100.1", 0, "172.17.0.2", 80, "tcp"))
	out := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	for _, tt := range []struct {
		name string
		p    fwstate.Packet
		want netstat.RouteQuery
	}{
		{"forwarded", fwd, w11b08Selected(rtQuery("172.17.0.2", "198.51.100.1", "eth0"), 6, 0, 80)},
		{"outbound", out, w11b08Selected(rtQuery("198.51.100.9", "192.0.2.1", ""), 6, 40100, 7443)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &gHost{routes: map[netstat.RouteQuery]netstat.Route{tt.want: rtVia("eth1", "")}, rules: proto}
			if _, oif, err := newRouter(h, rtFacts(0, 0, 0), fwstate.Loaded{}).route(tt.p); err != nil || oif != "eth1" {
				t.Errorf("route = %q, %v; want eth1", oif, err)
			}
		})
	}
}

func TestW11b08ReplyLookupsCarryTheFlowsPortAsSource(t *testing.T) {
	proto := w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6})
	src, from, proxy := netip.MustParseAddr("45.33.0.0"), netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("172.30.0.2")
	fl := flow{pkt: flPkt("input", "45.33.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc, standIn: true, fwdReplies: []netip.Addr{proxy}}
	h := &gHost{ifaces: gIfaces(), rules: proto}
	if !newRouter(h, facts{ifaces: gIfaces()}, fwstate.Loaded{}).noReply(fl, src, 0, from) {
		t.Fatal("noReply = false, want true: no lookup has a route")
	}
	want := []netstat.RouteQuery{
		w11b08Selected(rtQuery("45.33.0.0", "192.0.2.1", ""), 6, 443, 0),
		w11b08Selected(rtQuery("45.33.0.0", "172.30.0.2", "dp-panel"), 6, 443, 0),
	}
	if !reflect.DeepEqual(h.lookups, want) {
		t.Errorf("lookups = %+v, want %+v", h.lookups, want)
	}

	h = &gHost{ifaces: gIfaces(), rules: proto}
	fl.fwdReplies = nil
	newRouter(h, facts{ifaces: gIfaces()}, fwstate.Loaded{}).noReplyFrom(fl, src, 0)
	if len(h.lookups) == 0 || slices.ContainsFunc(h.lookups, func(q netstat.RouteQuery) bool { return q.Proto != 6 || q.SrcPort != 443 || q.DstPort != 0 }) {
		t.Errorf("lookups = %+v, want every one with protocol 6 and source port 443", h.lookups)
	}

	panel := flPkt("input", "203.0.113.7", 51000, "0.0.0.0", 443, "tcp")
	back := w11b08Selected(rtQuery("203.0.113.7", "", ""), 6, 443, 0)
	strict := []netstat.RouteQuery{back,
		w11b08Selected(rtQuery("203.0.113.7", "192.0.2.1", ""), 6, 443, 0),
		w11b08Selected(rtQuery("203.0.113.7", "10.0.0.1", ""), 6, 443, 0)}
	h = &gHost{ifaces: gIfaces(), rules: proto, routes: map[netstat.RouteQuery]netstat.Route{
		strict[0]: rtVia("eth0", "192.0.2.1"), strict[1]: rtVia("eth0", "192.0.2.1"), strict[2]: rtVia("eth0", "192.0.2.1")}}
	newRouter(h, rtFacts(1, 0, 0), fwstate.Loaded{}).probes(flow{label: "panel", pkt: panel})
	if !reflect.DeepEqual(h.lookups, strict) {
		t.Errorf("route-back lookups = %+v, want %+v", h.lookups, strict)
	}
}

func TestW11b08PolicyPrefixesJoinTheRouteCuts(t *testing.T) {
	ifaces := ckIfaces()
	ifaces[1].Addrs = append(ifaces[1].Addrs, netip.MustParsePrefix("45.33.0.9/24"))
	rule := func(fam netstat.Family, src, dst string) netstat.Rule {
		r := netstat.Rule{Family: fam, Priority: 100, Table: 200, Action: unix.FR_ACT_TO_TBL}
		if src != "" {
			r.Src = netip.MustParsePrefix(src)
		}
		if dst != "" {
			r.Dst = netip.MustParsePrefix(dst)
		}
		return r
	}
	h := &gHost{ifaces: ifaces, rules: []netstat.Rule{
		rule(netstat.IPv4, "8.8.8.0/24", ""),
		rule(netstat.IPv4, "", "9.9.9.9/32"),
		rule(netstat.IPv4, "45.33.0.9/32", ""),
		rule(netstat.IPv4, "45.33.0.0/24", ""),
		rule(netstat.IPv6, "", "2606:4700::/32"),
	}}
	r := newRouter(h, facts{ifaces: ifaces}, fwstate.Loaded{})
	var got []netip.Prefix
	for _, cut := range r.routeCuts(false).rs.Table("routes").Chain("routes").Rules {
		got = append(got, cut.Dest.Prefix)
	}
	want := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24"), netip.MustParsePrefix("9.9.9.9/32"), netip.MustParsePrefix("45.33.0.0/24")}
	if !slices.Equal(got, want) {
		t.Errorf("cuts = %v, want %v: a host's own single address turns the flow into input", got, want)
	}
}

func TestW11b08PolicyRuleCutsTheCellItSends(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -s 172.30.0.3/32 -i dp-panel -o eth1 -j DROP"), v1r21Table())
	def := netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix("0.0.0.0/0"), OutIface: "eth0"}
	edge := w11b08Rule(netstat.IPv4, netstat.Rule{Dst: netip.MustParsePrefix("198.41.192.0/24")})
	g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def}, rules: edge}}, st, v1r21Tunnel())
	for _, want := range []string{"198.41.192.0-198.41.192.255", "out eth1: drop"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard = %+v, want a refusal containing %q: the policy rule sends the range out of eth1", g, want)
		}
	}
	if g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def}}}, st, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: without the policy rule no cell routes out of eth1", g)
	}
}

// w11b08RulesFail is a host whose rule dump fails.
type w11b08RulesFail struct{ *gHost }

func (w11b08RulesFail) Rules(netstat.Family) ([]netstat.Rule, error) {
	return nil, errors.New("netlink: dump interrupted")
}

func TestW11b08HostWhosePolicyRulesCannotBeRead(t *testing.T) {
	g := v1r21CheckHost(t, w11b08RulesFail{&gHost{ifaces: ckIfaces()}}, v1r21State(t, v1r21Table(), v1r21Table()), v1r21Tunnel())
	const want = "exposure exp2 tunnel: unknown (the policy rules cannot be read: netlink: dump interrupted)"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 ./internal/modules/firewall/guard/`
Expected: FAIL, a build error naming `undefined: ruleDumper`, since the `var` block in `routes_test.go` compiles first or alongside.

- [ ] **Step 3: Implement**

1. **`routes.go`.** `ruleDumper` with a comment saying it is optional, as a host that cannot dump rules has none to report; and `func (LiveHost) Rules(family netstat.Family) ([]netstat.Rule, error)`.
2. **`routeCuts`.** Once the routes are read without error and the host implements `ruleDumper`, read the family's rules. An error sets `c.why` to the Contracts text. Otherwise `c.rules` holds them, and each `Src` and `Dst` prefix that `IsValid()` is appended to the `routes` chain of `c.rs` as a `netfilter.Rule{Dest: netfilter.AddrMatch{Prefix: p}}`, except a prefix where `p.IsSingleIP() && r.local[p.Addr().Unmap()]`. A short comment gives the reason: such a cell would turn the flow into input to the host.
3. **`selectors`.** Returns `q` unchanged unless `r.routeCuts(q.Dst.Unmap().Is6()).rules` holds a rule with `Proto != 0 || SrcPort || DstPort`. Otherwise it sets `q.Proto` from `protoNum(proto)` only for `tcp` and `udp`, because the kernel accepts no other protocol in a lookup, and sets `q.SrcPort` and `q.DstPort` from its arguments.
4. **Lookups.** Pass through `selectors`: the output lookup of `probes` and `route` and the forward lookup of `probes` and `route` with `p.Proto, p.SrcPort, p.DstPort`; the reply lookups with `fl.pkt.Proto` (or `p.Proto`), `fl.pkt.DstPort` as source port and `0` as destination port: the route-back lookup in `probes`, the two in `noReply`, the one in `noReplyFrom`, and the one in `eligible`. Do not change `fromTailnet`.
5. **Stand-in off.** `noReply` starts by returning false when any of the family's policy rules has `DstPort` or `UID`, or when any has `Mark` and `marksReplies(fl)` holds. Rewrite the comment: a reply's destination port is the client's, unknown here, and no lookup can state a uid. `marksReplies` scans the `raw` and `mangle` tables, not `filter` or `nat`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/routes.go internal/modules/firewall/guard/cells.go internal/modules/firewall/guard/route.go internal/modules/firewall/guard/policy_test.go internal/modules/firewall/guard/facts_test.go internal/modules/firewall/guard/routes_test.go internal/modules/firewall/guard/internet_cells_test.go
git commit -m "fix(firewall): read policy rules in the guard"
```

---

### Task 9: Dual-stack public binding

**Files:**
- Modify: `internal/modules/firewall/guard/flows.go` (`viaFlows`)
- Modify: `internal/modules/panel/listener.go` (the `public` binding of the open action)
- Test: `internal/modules/firewall/guard/flows_test.go`, `internal/modules/firewall/guard/exposure_test.go`, `internal/modules/panel/listener_test.go` (`lsSpec`)

**Interfaces:**
- Consumes: `flow.fwdReplies` (Task 5), `wildcardDsts` (Task 4)

**Items:** 2026-11-06-v1-security-review#28, 2026-11-06-v1-security-review#46, 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing tests**

In `internal/modules/panel/listener_test.go`, change `lsSpec` to `public := netip.MustParseAddrPort("[::]:443")`.

In `flows_test.go::TestBuildFlows`, change the two IPv6 rows of "a client that came through an exposure is not a direct panel flow" (those with source `"::"`) to carry no `fwdReplies`. Add beside `npm` and `tunnel`:

```go
npmDual := &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
	ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: flAddrPorts("[::]:443")}
npmIPv6Proxy := &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("fd00::2"),
	ListenerAddr: netip.MustParseAddrPort("[fd00::1]:8080"), Public: flAddrPorts("[::]:443")}
noGlobalIPv6 := []netstat.Interface{
	{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}},
	{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("fe80::1/64")}},
}
```

and these rows to the table:

```go
{
	name: "an unspecified IPv6 binding is both families on a host with IPv6",
	in:   Input{Plan: &core.GuardContext{Via: npmDual, Complete: true}},
	f:    facts{ifaces: gIfaces(), controller: 4242},
	want: []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
		{label: "exposure exp1 public", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), wild: wildSrc, fwdReplies: []netip.Addr{npmDual.ProxyAddr}},
		{label: "exposure exp1 public", pkt: flPkt("input", "::", 0, "::", 443, "tcp"), wild: wildSrc, standIn: true},
	},
},
{
	name: "an unspecified IPv6 binding is IPv4 only on a host without routable IPv6",
	in:   Input{Plan: &core.GuardContext{Via: npmDual, Complete: true}},
	f:    facts{ifaces: noGlobalIPv6, controller: 4242},
	want: []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
		{label: "exposure exp1 public", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), wild: wildSrc, fwdReplies: []netip.Addr{npmDual.ProxyAddr}},
	},
},
{
	name: "a forwarded reply belongs to the flow of the proxy's family",
	in:   Input{Plan: &core.GuardContext{Via: npmIPv6Proxy, Complete: true}},
	f:    facts{ifaces: gIfaces(), controller: 4242},
	want: []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "fd00::2", 0, "fd00::1", 8080, "tcp"))},
		{label: "exposure exp1 public", pkt: flPkt("input", "0.0.0.0", 0, "0.0.0.0", 443, "tcp"), wild: wildSrc},
		{label: "exposure exp1 public", pkt: flPkt("input", "::", 0, "::", 443, "tcp"), wild: wildSrc, standIn: true, fwdReplies: []netip.Addr{npmIPv6Proxy.ProxyAddr}},
	},
},
```

Append to `exposure_test.go`, adding the imports `"strings"` and `"github.com/darkraise/darkpanel/internal/platform/netstat"`:

```go
const (
	w11b09Nat = "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 172.30.0.2:443\nCOMMIT\n"
	w11b09Drop = "-A FORWARD -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
)

func w11b09Plan(bind string) *core.GuardContext {
	path := v1r19Path()
	path.Public = []netip.AddrPort{netip.MustParseAddrPort(bind)}
	return &core.GuardContext{Via: path, Complete: true}
}

func TestW11b09BlockedPublicBindingIsRefused(t *testing.T) {
	check := func(rules ...string) core.Guard {
		t.Helper()
		h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: v1r21HostRoutes()}}
		return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(rules...), v1r21Table()), w11b09Plan("0.0.0.0:443"))
	}
	const want = "exposure exp1 public from"
	if g := check(w11b09Drop); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
	if g := check(); !g.OK {
		t.Errorf("guard = %+v, want OK without the drop", g)
	}
}

func TestW11b09IPv6LegOfADualStackBinding(t *testing.T) {
	const v6Drop = "-A INPUT -p tcp -m tcp --dport 443 -j DROP"
	check := func(table []netstat.Route, v6 ...string) core.Guard {
		t.Helper()
		h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: table}}
		return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table(v6...)), w11b09Plan("[::]:443"))
	}
	withRoute := append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))
	const want = "exposure exp1 public from 2000::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff to [2001:db8::1]:443 on eth0: drop"
	if g := check(withRoute, v6Drop); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
	if g := check(withRoute); !g.OK {
		t.Errorf("guard = %+v, want OK without the IPv6 drop", g)
	}
	if g := check(v1r21HostRoutes(), v6Drop); !g.OK {
		t.Errorf("guard = %+v, want OK: a host without an IPv6 route has nothing to protect on that leg", g)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestW11b09|TestBuildFlows' ./internal/modules/firewall/guard/; go test -count=1 ./internal/modules/panel/`
Expected: both FAIL. In the guard package the new `TestBuildFlows` rows report the wrong flows and `TestW11b09IPv6LegOfADualStackBinding` reports a guard without the IPv6 refusal. In the panel package the open action's listener differs from `lsSpec` in its public binding.

- [ ] **Step 3: Implement**

1. In `viaFlows`, move the body of `case "npm"`'s public-binding loop into a helper `npmPublicFlows(s *flowSet, label string, v *core.ProxyPath, pub netip.AddrPort, local map[netip.Addr]bool)`. A local `replies(a netip.Addr) []netip.Addr` returns `[]netip.Addr{v.ProxyAddr}` when `a` and `v.ProxyAddr` are of one family after `Unmap`, and nil otherwise, so a reply lookup never mixes families.
2. For a binding whose address is the unspecified IPv6 address: add the IPv4 flow (`anyAddr` of IPv4 for source and destination, `wildSrc`, `replies` of the IPv4 source); and, only when `local` holds an IPv6 address that is neither loopback nor link-local, the IPv6 flow (`::` for source and destination, `wildSrc`, `standIn: true`, `replies` of the IPv6 source). A comment gives the reason: the record cannot say whether Docker publishes the port on IPv6, so a host without an IPv6 route skips those cells. Any other binding keeps today's logic, with `fwdReplies: replies(<the flow's source>)`.
3. `listener.go`: `public := netip.AddrPortFrom(netip.IPv6Unspecified(), npmHTTPSPort)`. Replace the comment above it: the guard reads the unspecified IPv6 address as dual-stack, because whether Docker also publishes `[::]:443` depends on the daemon's ip6tables setting, which the controller cannot see.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/modules/firewall/... ./internal/modules/panel/ ./internal/controller/listen/ ./internal/controller/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/flows.go internal/modules/firewall/guard/flows_test.go internal/modules/firewall/guard/exposure_test.go internal/modules/panel/listener.go internal/modules/panel/listener_test.go
git commit -m "fix(firewall): guard both legs of a public binding"
```

---

### Task 10: Harness journal window and single log parse

**Files:**
- Modify: `test/harness/m3_docker_traffic_test.go` (`m3dxTrafficLeaks`, `m3dxTrafficProbed`, `m3dxTrafficCount`, `v1r24Windows`, `m3dTrafficRecreate`)
- Test: `test/harness/m3_docker_traffic_unit_test.go`

**Interfaces:**
- Produces: `m3dxJournalSince(agentNs string) (string, error)` and `m3dxTrafficParse(log string) ([]m3dxTrafficAttempt, error)`, used only by this file

**Items:** 2026-11-06-v1-security-review#51

**Implementer:** dforge:impl-sonnet-medium
**Executor:** agy gemini-4-argon / low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Append to `test/harness/m3_docker_traffic_unit_test.go`, adding the imports `"reflect"` and `"time"` if absent:

```go
func TestW11b10UnitJournalSince(t *testing.T) {
	for _, tt := range []struct {
		name, in, want string
		bad            bool
	}{
		{"two seconds before the agent's clock", "1762700000123456789\n", "@1762699998", false},
		{"no trailing newline", "1762700000000000000", "@1762699998", false},
		{"a fraction of a second rounds down", "1762700000999999999", "@1762699998", false},
		{"empty output", "", "", true},
		{"not a number", "date: invalid\n", "", true},
		{"a clock under two seconds", "1000000000", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m3dxJournalSince(tt.in)
			if (err != nil) != tt.bad || got != tt.want {
				t.Errorf("m3dxJournalSince(%q) = %q, %v; want %q, error %v", tt.in, got, err, tt.want, tt.bad)
			}
		})
	}
}

func TestW11b10UnitTrafficParse(t *testing.T) {
	at := time.Unix(0, 1762700000000000000).UTC()
	got, err := m3dxTrafficParse("18209 ok 1762700000000000000\n\n18210 fail\n")
	want := []m3dxTrafficAttempt{{Port: 18209, OK: true, At: at}, {Port: 18210}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("m3dxTrafficParse = %+v, %v; want %+v", got, err, want)
	}
	if _, err := m3dxTrafficParse("18209 ok\nbad\n"); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("a malformed line: %v, want an error naming it", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags harness -count=1 -run 'TestW11b10Unit' ./test/harness/`
Expected: FAIL with `undefined: m3dxJournalSince`.

- [ ] **Step 3: Implement**

1. Add `m3dxJournalSince`: trim, `strconv.ParseInt` base 10; an error or a value under `2e9` returns `harness: agent time %q is not Unix nanoseconds`. Otherwise `"@" + strconv.FormatInt(ns/1e9-2, 10)`. A comment gives the reason: the agent's clock, not the runner's, decides which journal lines the window holds, and two seconds cover the scenario's first log line.
2. Add `m3dxTrafficAttempt{Port uint16; OK bool; At time.Time}` and `m3dxTrafficParse`, which walks the lines as today (blank lines skipped) through `v1r24ParseLine`.
3. Split `v1r24Windows`, `m3dxTrafficCount` and `m3dxTrafficProbed` into their current string-taking forms, which parse and delegate, and inner forms over `[]m3dxTrafficAttempt`. `m3dxTrafficLeaks` parses once and calls only the inner forms. Their error messages do not change.
4. In `m3dTrafficRecreate`, keep the two `date +%s%N` log lines. From the agent's output set `since` through `m3dxJournalSince`, calling `t.Fatal` on its error, and drop the runner-clock `since`. The `journalctl` call is otherwise unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags harness -count=1 -run 'TestM3xTraffic|TestV1r24Unit|TestW11b10Unit' ./test/harness/`
Expected: PASS. `TestM3xTrafficLeaks`, `TestM3xTrafficProbed` and `TestV1r24UnitLeakNamesTheWindows` pin that the refactor changes nothing; they have no `Unit` in the first two names, so this filter is the one that runs them.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m3_docker_traffic_test.go test/harness/m3_docker_traffic_unit_test.go
git commit -m "test(harness): take the journal window from the agent"
```

---

### Task 11: Spec rulings and threat model

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (the W10b-R12 sentence, and a new "Plan rulings (wave 11b)" block after W11-R6)
- Modify: `docs/security/threat-model.md` (Firewall: Mitigations and Residual risk)
- Test: `internal/docs/threat_model_test.go`

**Interfaces:**
- Consumes: the refusal texts and behaviour of Tasks 4 to 9

**Items:** 2026-11-06-v1-security-review#27, 2026-11-06-v1-security-review#53

**Implementer:** dforge:impl-sonnet-low
**Executor:** agy gemini-3.8-flash / medium
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Append to `internal/docs/threat_model_test.go`:

```go
func TestW11b11GuardDocumentation(t *testing.T) {
	residual := strings.Join(w10tmParse(dcRead(t, w10tmPage)).parts["Firewall/Residual risk"], "\n")
	for _, want := range []string{"`100.64.0.0/10`", "`2001:db8::/32`", "2,200 bans", "`uidrange`", "bridge"} {
		if !strings.Contains(residual, want) {
			t.Errorf("Firewall residual risk does not state %q", want)
		}
	}
	for _, gone := range []string{"checks only the IPv4 public leg", "does not read `ip rule`", "open follow-up rows"} {
		if strings.Contains(residual, gone) {
			t.Errorf("Firewall residual risk still states %q", gone)
		}
	}
	spec := dcRead(t, "docs/dforge/specs/2026-10-21-v1-completion-design.md")
	for _, want := range []string{"W11-R7 wildcard binds", "W11-R8 cell limit", "W11-R9 IPv6 public leg", "W11-R10 policy routing", "W11-R11 special-purpose ranges", "W11-R12 guard details"} {
		if !strings.Contains(spec, want) {
			t.Errorf("the spec has no ruling %q", want)
		}
	}
	if strings.Contains(spec, "on the interface its route back leaves through") {
		t.Error("the spec still limits a wildcard bind to the interface of the route back")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run 'TestW11b11' ./internal/docs/`
Expected: FAIL, naming the first missing term.

- [ ] **Step 3: Edit the documents**

1. In the spec's W10b-R12 bullet, replace "is checked on every address of its family on the interface its route back leaves through." with "is checked on every non-link-local address of its family on every up, non-loopback interface (W11-R7)."
2. After the W11-R6 bullet, insert exactly:

```markdown
- **Plan rulings (wave 11b).** Wave 11b (plan `v1-w11b-lockout-guard`) settles these points:
  - **W11-R7 wildcard binds.** A flow to a wildcard bind is checked on the preferred source of the route back, then on every non-link-local address of its family on every up, non-loopback interface (owner, 2026-11-08). Bridge and tailnet addresses join the set, so a hand-written binding allowance that names only the uplinks fails the guard of a revert that had blocked effects, conflicts or a write error; other reverts do not reach the guard.
  - **W11-R8 cell limit.** The guard fails closed on cost: a flow is unknown when its cells number more than 65536, or when the cells times the rules of the family's ruleset plus one exceed 10,000,000. About 2,200 bans that can match the flow's port, or a full BGP table, refuse guarded changes until overridden.
  - **W11-R9 IPv6 public leg.** The panel records `[::]:443` for every NPM exposure, keeping W10b-R12's "bindings come from the exposure's own record". The guard reads an unspecified IPv6 binding as dual-stack: the IPv4 flow always, and the IPv6 flow as a stand-in only when the host has an IPv6 address that is neither loopback nor link-local. A forwarded reply is checked only on a flow of the proxy address's family. Rows stored earlier keep `0.0.0.0:443` until the exposure is recreated.
  - **W11-R10 policy routing.** The guard reads `ip rule`. A rule's source and destination prefixes cut cells, except a single address of the host. Lookups carry the protocol and ports only while a rule of the family selects by them, so a host without such rules sends the queries it always sent. Stand-in skipping turns off while a rule selects by destination port or uid, which no reply lookup can state, or by mark while `raw` or `mangle` holds a MARK or CONNMARK rule that may match. Unreadable rules make every wild flow unknown.
  - **W11-R11 special-purpose ranges.** The cell space keeps leaving out the ranges of `eval.holes4` and `holes6`; the threat model lists them all. No code changes.
  - **W11-R12 guard details.** Local routes outside the local table cut cells. An unmodelled target in any table keeps every rule as a cutter. A cell is no-path or blackhole only when its longest matching route says so. The before-state is routed with its own sysctl values. A pass reports the probes the before-state already blocked.
```

3. In the threat model's Firewall mitigation that begins "A lockout guard predicts", append before its closing path list: "A flow to a wildcard bind is checked on every non-link-local address of its family on every up, non-loopback interface, and an NPM exposure recorded on `[::]:443` is checked on both families. The guard reads the policy rules: their prefixes cut the cells, and their protocol and port selectors are carried on route lookups."
4. In Firewall Residual risk, delete the bullets beginning "The lockout guard checks only the IPv4 public leg", "The reply lookups that decide" and "The open follow-up rows", and replace these three:
   - The bullet beginning "The cells cover the public unicast space only." becomes: "The cells cover the public unicast space only. `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `240.0.0.0/4`, `2001:db8::/32` and all IPv6 outside `2000::/3` are left out, so an any-address flow is not protected from or to an address there. A protected flow with a concrete address, such as an SSH session from a private network, is checked whatever its range (`internal/modules/firewall/eval`)."
   - The bullet beginning "The guard does not read" becomes: "A lookup cannot state a reply's destination port or a user. While a policy rule selects by `dport` or `uidrange`, or by a firewall mark while a `raw` or `mangle` rule may set one, a missing route no longer skips a source cell, so such a host refuses guarded changes until overridden (`internal/modules/firewall/guard`)."
   - The bullet beginning "Inside a cell that the route dump" becomes: "Inside a cell whose longest route is a blackhole, the guard takes any EINVAL from a route lookup as \"no route\", because the kernel answers a lookup there with EINVAL. An EINVAL there for another reason also skips the cell (`internal/modules/firewall/guard`)."
5. Add these two bullets to Firewall Residual risk:
   - "A check that would cost too much fails closed: more than 65,536 cells, or cells times rules above 10,000,000. About 2,200 bans that can match a protected flow's port, or a full BGP table, refuse guarded changes until overridden (`internal/modules/firewall/guard`)."
   - "A wildcard bind is checked on the host's bridge and tailnet addresses too. A hand-written allowance that names only the uplinks fails the guard of a revert that already had blocked effects, conflicts or a write error (`internal/modules/firewall/guard`)."

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/docs/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md docs/security/threat-model.md internal/docs/threat_model_test.go
git commit -m "docs(security): record the wave 11b guard rulings"
```
