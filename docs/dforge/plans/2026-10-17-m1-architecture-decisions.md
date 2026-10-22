# M1 Architecture Decision Records Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Write Darkpanel's nine architecture decision records, their index with shared contracts and traceability, and a structural checker for them.

**Architecture:** A wave-0 foundation task writes `docs/adr/README.md` (index, template, lifecycle, shared contracts) and `scripts/check-adrs.sh` with its test. Nine independent tasks then each write one ADR from the matching section of the spec. Two final single-task waves add the traceability table, then reconcile the ADRs against each other and against that table.

**Tech Stack:** Markdown; bash 5 and awk for the checker (no grep, so the script behaves the same with GNU grep and ugrep).

**Spec:** docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves.

**Plan review:** 2026-10-17 — codex gpt-6-astra / xhigh — executability 9 / coherence 10 / coverage 15 / assumptions 8 (round 1)

**Parallelism:** waves — 1 | 2-10 | 11 | 12

**Integration check:** `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh`

**Worktree setup:** none

## Global Constraints

- The spec `docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md` is the source of truth. An ADR's Decision section states every bullet of the spec's matching `### 000N` section, in the same order, with the same values (ports, paths, lifetimes, names). It adds no decision the spec does not contain and contradicts nothing in the spec or in `docs/adr/README.md`.
- Every ADR uses the template in Contracts exactly: the title line, the `**Requirements:**` line, then the headings `## Status`, `## Context`, `## Decision`, `## Alternatives considered`, `## Consequences` in that order. Status is `Proposed`.
- The shared contracts (names, schemas, action classes, errors, actors, version skew) are defined only in `docs/adr/README.md`. An ADR refers to them ("see the shared contracts in the index") and never restates them with different wording.
- ADRs refer to each other as `ADR 000N` with the titles listed in Contracts.
- English only. Plain, literal sentences; no metaphor. Each ADR is 50 to 160 lines.
- A task edits only the files in its own `**Files:**` block.
- In a waves run every task executes in its own git worktree created by the controller (dr-superpowers:subagent-driven-development) and commits only there; the controller merges a wave's worktrees before starting the next wave. No two tasks share a git index.
- Commits use `docs(adr): <subject>` (subject at most 50 characters, imperative, no period) and carry no AI attribution.
- Tests touch only temporary directories created with `mktemp -d`; no ports, no shared paths.

## Contracts

**ADR files and titles** (fixed; README links each as `[ADR 000N: <title>](000N-<slug>.md)`):

| File | Title |
|---|---|
| `docs/adr/0001-stack-and-layout.md` | Stack, layout and process model |
| `docs/adr/0002-module-contract.md` | Module contract and compatibility |
| `docs/adr/0003-agent-protocol.md` | Controller–agent protocol, identity and enrollment |
| `docs/adr/0004-change-lifecycle.md` | Change lifecycle, locking and recovery |
| `docs/adr/0005-storage-and-secrets.md` | Storage, migrations, secrets and keys |
| `docs/adr/0006-api-and-cli.md` | API, listeners, authentication and CLI |
| `docs/adr/0007-release-and-update.md` | Release, signing, update and rollback |
| `docs/adr/0008-test-harness.md` | Integration test harness |
| `docs/adr/0009-host-execution-safety.md` | Host execution safety |

**ADR template** (every ADR file):

```markdown
# ADR 000N: <title from the table above>

**Requirements:** <comma-separated R-numbers, e.g. R5, R6, R9>

## Status

Proposed

## Context

<why a decision is needed: the problem, the forces, the constraints from the product spec>

## Decision

<every bullet of the spec's matching section, as prose or bullets>

## Alternatives considered

<each alternative and why it was not chosen>

## Consequences

<what follows: benefits, costs, obligations on later epics>
```

**`scripts/check-adrs.sh`** — usage `bash scripts/check-adrs.sh [--complete] [ADR_FILE...]`.
- Reads ADRs from `$ADR_DIR` (default `docs/adr`).
- With file arguments: checks only those files' structure: line 1 is `# ADR NNNN: <title>`; a `**Requirements:**` line exists; the five headings exist, in template order, each with at least one non-blank line; Status is `Proposed`, `Accepted` or `Superseded by ADR NNNN`.
- Without file arguments: checks every `$ADR_DIR/[0-9][0-9][0-9][0-9]-*.md`, and that `$ADR_DIR/README.md` exists and links each of them.
- `--complete` additionally fails when README links an ADR file that does not exist, and, when README has a `## Traceability` table, when a row maps a requirement to an ADR whose `**Requirements:**` line does not list it (an ADR may list more requirements than the table).
- Every error is one line on stderr starting `check-adrs: `; exit 0 when no errors, 1 otherwise.

**`docs/adr/README.md` sections** (Task 1 writes the first five; Task 11 appends the sixth): `## Index`, `## Format`, `## Lifecycle`, `## Shared contracts`, `## Deferred decisions`, `## Traceability`.

## Assumptions (evidence)

- Local tools: `bash --version` printed 5.2.37 and `awk -W version` printed mawk 1.3.4 on 2026-10-17. The checker avoids `grep` (this host's `grep` is ugrep 7.8.4) and regex interval braces. The checker and test in Task 1 were run on this host on 2026-10-17: 15 passed with the checker present, 15 failed with it absent.
- awk on GitHub runners: unverified, and not needed by this plan, whose checks all run locally; the M1 "Repository, tooling, CI and gates" epic runs the checker in CI and verifies it there.
- The values the ADRs record are the owner-approved spec's (approved 2026-10-17). The facts behind them were checked by commands on 2026-10-17: `https://go.dev/dl/?mode=json` listed go1.27.2; `npm view darkraise-ui version` printed 6.9.6; `proxy.golang.org` listed modernc.org/sqlite v1.60.1 and hashicorp/yamux v0.1.2. KVM on GitHub runners is recorded in ADR 0008 as a risk, not assumed. ADR tasks copy values from the decision text in their task; they do not choose new ones.
- Wave 2's integration check passes before every ADR exists, because the checker without `--complete` only requires README to link ADR files that exist — Task 1 test `index_ignores_missing_without_complete`.
- R19 (a role field on user records) has no ADR: it is a stored field decided by the identity epic, not a decision other epics build on. The traceability table lists it explicitly as `none` so the R15–R36 mapping the spec asks for is complete.
- Plan review round 1 (cap 1, no Critical) scored executability 9 and coherence 10 (borderline) and assumptions 8; all 13 Important findings were fixed after the round, and the stricter checker and its tests were re-run from the plan text (15 passed). With assumptions at 8 at the cap, the owner decides whether to accept the plan or ask for one delta review.
- Lane-eligible tasks carry no `**Executor:**` line: the owner chose Claude-only on 2026-10-17, because an Executor line forces its task to run alone and wave 2 would lose its parallelism.
- Wave 2 has nine tasks: each writes one separate document and consumes only Task 1's output, so they are trivially independent; they run three at a time.

## Task index

1. Foundation: ADR index and checker
2. ADR 0001 stack, layout and process model
3. ADR 0002 module contract and compatibility
4. ADR 0003 controller–agent protocol
5. ADR 0004 change lifecycle
6. ADR 0005 storage, migrations, secrets and keys
7. ADR 0006 API, listeners, authentication and CLI
8. ADR 0007 release, signing, update and rollback
9. ADR 0008 integration test harness
10. ADR 0009 host execution safety
11. Traceability table
12. Cross-ADR reconciliation

---

### Task 1: Foundation: ADR index and checker

**Files:**
- Create: `scripts/check-adrs.sh`
- Create: `docs/adr/README.md`
- Test: `scripts/check-adrs.test.sh`

**Interfaces:**
- Consumes: nothing.
- Produces: `scripts/check-adrs.sh` and the README sections `## Index` to `## Deferred decisions` (Contracts).

**Items:** 1

**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `scripts/check-adrs.test.sh`:

```bash
#!/usr/bin/env bash
# Tests for scripts/check-adrs.sh. Each case builds its own ADR directory.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
checker="$here/check-adrs.sh"
pass=0; failures=0

# adr <file> [section to leave empty] [order: normal|swapped] [title]
adr() {
  local f=$1 empty=${2:-} order=${3:-normal} title=${4:-"# ADR 0001: Example"} h
  local heads=("## Status" "## Context" "## Decision" "## Alternatives considered" "## Consequences")
  [ "$order" = swapped ] && heads=("## Status" "## Decision" "## Context" "## Alternatives considered" "## Consequences")
  { printf '%s\n\n**Requirements:** R1, R2\n' "$title"
    for h in "${heads[@]}"; do
      printf '\n%s\n\n' "$h"
      if [ "$h" = "$empty" ]; then continue; fi
      if [ "$h" = "## Status" ]; then echo Proposed; else echo Text.; fi
    done; } > "$f"
}

expect() { # expect <name> <want exit> <stderr substring or -> <command...>
  local name=$1 want=$2 needle=$3; shift 3
  local err rc
  err=$("$@" 2>&1 >/dev/null); rc=$?
  if [ "$rc" -ne "$want" ]; then
    echo "FAIL $name: exit $rc, want $want; stderr: $err"; failures=$((failures+1)); return
  fi
  if [ "$needle" != "-" ] && [[ "$err" != *"$needle"* ]]; then
    echo "FAIL $name: stderr lacks '$needle': $err"; failures=$((failures+1)); return
  fi
  pass=$((pass+1))
}

d=$(mktemp -d); adr "$d/0001-example.md"
printf '# Index\n\n- [ADR 0001: Example](0001-example.md)\n' > "$d/README.md"
expect valid_set 0 - env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d)
expect no_such_file 1 "no such file" env ADR_DIR="$d" bash "$checker" "$d/0009-absent.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '$0!="## Consequences"' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect missing_heading 1 "missing heading '## Consequences'" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '!/^\*\*Requirements:/' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect missing_requirements 1 "missing **Requirements:** line" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '{ if ($0=="Proposed") print "Draft"; else print }' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect bad_status 1 "status must be" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "" normal "# Example"
expect missing_title 1 "missing title line" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "" swapped
expect out_of_order 1 "out of order" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "## Decision"
expect empty_section 1 "empty section '## Decision'" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
printf '# Index\n' > "$d/README.md"
expect index_missing_link 1 "does not link 0001-example.md" env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d); adr "$d/0001-example.md"
expect index_missing_file 1 "README.md: missing" env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d); adr "$d/0001-example.md"
printf -- '- [ADR 0001: Example](0001-example.md)\n- [ADR 0002: Later](0002-later.md)\n' > "$d/README.md"
expect index_ignores_missing_without_complete 0 - env ADR_DIR="$d" bash "$checker"
expect complete_reports_missing 1 "links missing file 0002-later.md" env ADR_DIR="$d" bash "$checker" --complete

d=$(mktemp -d); adr "$d/0001-example.md"
printf -- '- [ADR 0001: Example](0001-example.md)\n\n## Traceability\n\n| Requirement | ADRs |\n|---|---|\n| R1, R2 | 0001 (Decision) |\n' > "$d/README.md"
expect trace_ok 0 - env ADR_DIR="$d" bash "$checker" --complete
printf '| R7 | 0001 (Decision) |\n' >> "$d/README.md"
expect trace_pair_missing 1 "maps R7 to ADR 0001" env ADR_DIR="$d" bash "$checker" --complete
printf '| R2 | 0004 |\n' >> "$d/README.md"
expect trace_missing_adr 1 "names missing ADR 0004" env ADR_DIR="$d" bash "$checker" --complete

echo "check-adrs tests: $pass passed, $failures failed"
[ "$failures" -eq 0 ]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash scripts/check-adrs.test.sh`
Expected: FAIL lines for every case (the checker does not exist yet), ending `check-adrs tests: 0 passed, 15 failed`, exit 1.

- [ ] **Step 3: Write the checker**

Create `scripts/check-adrs.sh`:

```bash
#!/usr/bin/env bash
# Structural check for architecture decision records. Uses awk, not grep:
# grep is ugrep on some hosts and GNU grep on others.
set -euo pipefail

adr_dir=${ADR_DIR:-docs/adr}
complete=0
if [ "${1:-}" = "--complete" ]; then complete=1; shift; fi
errors=0
fail() { echo "check-adrs: $1" >&2; errors=$((errors + 1)); }

check_adr() {
  local f=$1 msg
  if [ ! -f "$f" ]; then fail "$f: no such file"; return; fi
  while IFS= read -r msg; do fail "$f: $msg"; done < <(awk -v q="'" '
    BEGIN { split("## Status|## Context|## Decision|## Alternatives considered|## Consequences", req, "|") }
    NR == 1 && $0 !~ /^# ADR [0-9][0-9][0-9][0-9]: [^ ]/ { print "missing title line " q "# ADR NNNN: <title>" q }
    /^\*\*Requirements:\*\* (R[0-9]|none)/ { reqline = 1 }
    /^## / { cur = $0; pos[cur] = ++n; filled[cur] = 0; next }
    cur != "" && NF { filled[cur]++; if (cur == "## Status" && status == "") status = $0 }
    END {
      if (!reqline) print "missing **Requirements:** line"
      last = 0
      for (i = 1; i <= 5; i++) {
        h = req[i]
        if (!(h in pos)) { print "missing heading " q h q; continue }
        if (pos[h] < last) print "heading " q h q " out of order"
        last = pos[h]
        if (!filled[h]) print "empty section " q h q
      }
      if (status != "Proposed" && status != "Accepted" && status !~ /^Superseded by ADR [0-9][0-9][0-9][0-9]/)
        print "status must be Proposed, Accepted or " q "Superseded by ADR NNNN" q ", got " q status q
    }' "$f")
}

# Prints "R<n> <NNNN>" for every requirement/ADR pair in README's Traceability table.
trace_pairs() {
  awk '
    /^## / { t = ($0 == "## Traceability"); next }
    t && /^\| *R[0-9]/ {
      split($0, cell, "|")
      nr = split(cell[2], rs, /[^A-Za-z0-9]+/)
      na = split(cell[3], as, /[^0-9]+/)
      for (i = 1; i <= nr; i++) if (rs[i] ~ /^R[0-9]+$/)
        for (j = 1; j <= na; j++) if (as[j] ~ /^[0-9][0-9][0-9][0-9]$/) print rs[i], as[j]
    }' "$1"
}

lists_requirement() {
  awk -v r="$2" '/^\*\*Requirements:\*\*/ { n = split($0, a, /[ ,]+/); for (i = 1; i <= n; i++) if (a[i] == r) f = 1 } END { exit !f }' "$1"
}

if [ $# -gt 0 ]; then
  files=("$@")
else
  shopt -s nullglob
  files=("$adr_dir"/[0-9][0-9][0-9][0-9]-*.md)
fi
for f in "${files[@]}"; do check_adr "$f"; done

if [ $# -eq 0 ]; then
  index="$adr_dir/README.md"
  if [ ! -f "$index" ]; then
    fail "$index: missing"
  else
    for f in "${files[@]}"; do
      b=$(basename "$f")
      awk -v l="($b)" 'index($0, l) { found = 1 } END { exit !found }' "$index" \
        || fail "$index: does not link $b"
    done
    if [ "$complete" -eq 1 ]; then
      while read -r l; do
        [ -f "$adr_dir/$l" ] || fail "$index: links missing file $l"
      done < <(awk '{ while (match($0, /\([0-9][0-9][0-9][0-9]-[a-z0-9-]+\.md\)/)) { print substr($0, RSTART + 1, RLENGTH - 2); $0 = substr($0, RSTART + RLENGTH) } }' "$index" | sort -u)
      while read -r r n; do
        adr=("$adr_dir"/"$n"-*.md)
        if [ ${#adr[@]} -eq 0 ] || [ ! -f "${adr[0]}" ]; then fail "$index: traceability names missing ADR $n"; continue; fi
        lists_requirement "${adr[0]}" "$r" || fail "$index: traceability maps $r to ADR $n, but its **Requirements:** line lacks $r"
      done < <(trace_pairs "$index")
    fi
  fi
fi

[ "$errors" -eq 0 ]
```

Run: `chmod +x scripts/check-adrs.sh scripts/check-adrs.test.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `bash scripts/check-adrs.test.sh`
Expected: `check-adrs tests: 15 passed, 0 failed`, exit 0.

- [ ] **Step 5: Write the index**

Create `docs/adr/README.md` with exactly these sections and content (prose may be tightened, values may not change):

```markdown
# Architecture decision records

Decisions every Darkpanel epic builds on. Source design:
`docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md`.
Check the files with `bash scripts/check-adrs.sh`.

## Index

- [ADR 0001: Stack, layout and process model](0001-stack-and-layout.md)
- [ADR 0002: Module contract and compatibility](0002-module-contract.md)
- [ADR 0003: Controller–agent protocol, identity and enrollment](0003-agent-protocol.md)
- [ADR 0004: Change lifecycle, locking and recovery](0004-change-lifecycle.md)
- [ADR 0005: Storage, migrations, secrets and keys](0005-storage-and-secrets.md)
- [ADR 0006: API, listeners, authentication and CLI](0006-api-and-cli.md)
- [ADR 0007: Release, signing, update and rollback](0007-release-and-update.md)
- [ADR 0008: Integration test harness](0008-test-harness.md)
- [ADR 0009: Host execution safety](0009-host-execution-safety.md)

## Format

Every ADR starts with `# ADR NNNN: <title>`, then a `**Requirements:**` line
naming the product-spec requirements it serves, then the sections Status,
Context, Decision, Alternatives considered and Consequences, in that order.

## Lifecycle

- `Proposed`: written, may still be corrected while its epic is open.
- `Accepted`: set when the epic that wrote it closes. From then on the ADR is
  not edited; a change needs a new ADR.
- `Superseded by ADR NNNN`: the newer ADR replaces it; the old file stays.

## Shared contracts

Every ADR uses these definitions and does not redefine them.

- **Names.** Action names are `<module>.<noun>.<verb>` in lower case, for
  example `firewall.rule.add`. Versions are positive integers written `v1`.
  Host ids and change ids are ULIDs. JSON fields are camelCase.
- **Schemas.** JSON Schema draft 2020-12, generated from Go types with
  `github.com/invopop/jsonschema`. The struct tag `secret:"true"` marks a field
  that is encrypted at rest, write-only through the API, and redacted from
  logs, audit, snapshots, diffs, events and errors.
- **Action classes.** Every action is exactly one of:
  - `read`: changes nothing.
  - `revertible`: stores a snapshot and arms a confirm-or-revert timer.
  - `compensable`: undone by a named compensating action; no timer.
  - `irreversible`: cannot be undone (for example pruning volumes); requires
    typed confirmation.
- **Errors.** RFC 9457 problem details with a stable `code` field, from the
  registry in `internal/core/errcode`.
- **Actors.** Audit actors are `user:<id>`, `token:<id>`, `agent:<host id>`,
  `local:root@<host id>` and `system:<job>`.
- **Version skew.** The controller is never older than its agents. It supports
  agents of the same and the previous minor version, is updated first, and
  drives agent updates up to its own version.

## Deferred decisions

- How DERP servers run (container or systemd service): M5 DERP epic.
- How `cloudflared` runs: M6 Cloudflare Tunnel epic.
- How the install script verifies a download (it must not trust TLS alone):
  M2 install epic.
```

- [ ] **Step 6: Run the checker on the repository**

Run: `bash scripts/check-adrs.sh; echo "exit=$?"`
Expected: no output and `exit=0` (no ADR files exist yet, README exists).

- [ ] **Step 7: Commit**

```bash
git add scripts/check-adrs.sh scripts/check-adrs.test.sh docs/adr/README.md
git commit -m "docs(adr): add ADR index and structure checker"
```

---

### Task 2: ADR 0001 stack, layout and process model

**Files:**
- Create: `docs/adr/0001-stack-and-layout.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts.
- Produces: `docs/adr/0001-stack-and-layout.md`.

**Items:** 2

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0001-stack-and-layout.md`
Expected: `check-adrs: docs/adr/0001-stack-and-layout.md: no such file`, exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Stack, layout and process model` and `**Requirements:** R2, R13, R14, R26`.

- Context: one Go binary must hold CLI, controller, agent and the embedded web UI for `linux/amd64` and `linux/arm64` (product spec Constraints); the controller manages its own host through the same agent path (R26); reviewers flagged that the controller's run-as user and the toolchain pin were undecided.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0001` of the spec.

````markdown
- Go 1.27 with `go 1.27` and `toolchain go1.27.2` in `go.mod`; CI sets
  `GOTOOLCHAIN=local` on the exact version. CGO disabled; `-trimpath`; one
  binary for `linux/amd64` and `linux/arm64`.
- HTTP with the standard library router; CLI with `github.com/spf13/cobra`.
- Web UI: Vite, React 19, darkraise-ui 6.x, TanStack Query, built to `web/dist`
  and embedded with `go:embed`; pnpm.
- Process model: two systemd services from one binary. `darkpanel-controller`
  runs as an unprivileged `darkpanel` user (it never touches the host
  directly). `darkpanel-agent` runs as root on every managed host, including
  the controller's host, where it connects over loopback like any other agent.
  In M1, before the transport exists, the controller calls the agent handler
  in-process.
- Layout:

```
cmd/darkpanel/            entry point, role dispatch (serve, agent, CLI, update)
internal/core/            registry, actions, lifecycle, audit, errcode, schema checks
internal/controller/      HTTP API, auth, database, fleet, scheduler, CA
internal/agent/           agent runtime, local socket, journal, revert timers
internal/transport/       TLS, enrollment, yamux
internal/platform/        providers: exec, files, systemd, OS detection (ADR 0009)
internal/update/          updater, transaction, recovery
internal/modules/<id>/    manifest, controller part, agent part, migrations
schemas/<action>/v<n>.json committed action schema snapshots
web/src/modules/<id>/     module UI pages
docs/adr/                 decision records
```
````

- Alternatives considered:
  - Controller running as root: rejected; it never needs to touch the host directly, and the CyberPanel CVE-2024-51567 compromise ran with full privileges.
  - Agent inside the controller process on the controller host as the permanent design: rejected from M2 on; a controller crash or update would stop pending firewall reverts (owner ruling 2026-10-17). The in-process call in M1 is a temporary step before the transport exists, as the decision text says.
  - A web framework router (chi, gin): rejected; the standard library router covers the routes in ADR 0006 with one fewer dependency.
  - A `toolchain` directive alone: rejected; it allows newer toolchains, so builds would differ between machines.
- Consequences: two systemd units per controller host; the agent is the only long-running root service (root CLI invocations reach it through its local socket, ADR 0006); host-touching code lives only in agent parts and `internal/platform`; every later epic places files according to the layout; CI must set `GOTOOLCHAIN=local`.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0001-stack-and-layout.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0001-stack-and-layout.md
git commit -m "docs(adr): add ADR 0001 stack and layout"
```

---

### Task 3: ADR 0002 module contract and compatibility

**Files:**
- Create: `docs/adr/0002-module-contract.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (action classes, schemas, version skew).
- Produces: `docs/adr/0002-module-contract.md`.

**Items:** 3

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0002-module-contract.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Module contract and compatibility` and `**Requirements:** R1, R5, R6, R7, R9, R24`.

- Context: new features must ship as modules without breaking hosts already in production; agents of the previous minor version stay connected (R25); review finding 15: an "additive" field can be silently ignored by an older agent while the action version stays the same.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0002` of the spec.

````markdown
- A module implements one Go interface returning a manifest: id, version,
  actions, resources, settings schema, preflight checks, owned paths and
  marked blocks (ADR 0009), uninstall step, and migrations per role
  (controller, agent). It may supply a controller part, an agent part, or both;
  the UI part is a route bundle registered in `web/src/modules/registry.ts`.
- Modules are compiled in and listed once in `internal/modules`; no runtime
  plugins in v1.
- An action declares name, version, typed input and output, class (shared
  contracts), where it runs (controller or agent), whether it is break-glass,
  and the lock keys it takes (ADR 0004).
- A module never changes another module's resources directly; it calls the
  owning module's actions (for example DERP opens ports through
  `firewall.rule.add`), so locking, snapshots and audit stay with the owner.
- Enablement and preflight results are stored per host in
  `core_module_state`. A disabled module, or one whose migration or preflight
  fails, is marked unavailable with the reason; the rest of the system starts.
- Compatibility rules, enforced by a test against the schema snapshots of every
  released tag (snapshots are immutable once released):
  - Allowed in a published version: new optional input fields, new output
    fields, new output enum values (clients must accept unknown values).
  - Forbidden: removing or renaming a field, changing a type, a default or a
    constraint, adding a required input field, removing an enum value, or
    changing an action's meaning or class.
  - A breaking change is a new version; the old version stays until the next
    major release.
- Agents advertise, per action version, the hash of the schema they implement.
  The controller refuses to send an input field the agent's schema does not
  contain, so an older agent can never silently ignore a newer safety field.
````

- Alternatives considered:
  - Runtime plugins (Go plugins, subprocess plugins, WebAssembly): rejected for v1; Go plugins require identical toolchains, subprocess plugins add a second protocol, and none is needed while all modules live in this repository.
  - Protobuf instead of JSON Schema: rejected; a second schema system beside the JSON API.
  - Comparing schemas only against the current commit: rejected; a change could be made and reverted between releases, so the baseline is every released tag's snapshot.
- Consequences: adding a module touches the core in one registration line; every action needs a committed schema snapshot; the compatibility test runs in CI from M1; the controller must keep per-agent schema hashes from `hello` (ADR 0003); cross-module effects go through the owning module's actions (relied on by M3, M5, M6).

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0002-module-contract.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0002-module-contract.md
git commit -m "docs(adr): add ADR 0002 module contract"
```

---

### Task 4: ADR 0003 controller–agent protocol

**Files:**
- Create: `docs/adr/0003-agent-protocol.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (version skew, actors).
- Produces: `docs/adr/0003-agent-protocol.md`.

**Items:** 4

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0003-agent-protocol.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Controller–agent protocol, identity and enrollment` and `**Requirements:** R1, R18, R22, R23, R24, R25, R27, R28`.

- Context: agents open no listening port and dial out (product spec Constraints); the same agent handler must serve the local CLI; review findings 1, 5, 6, 21, 23: one authority signing both roles let an agent impersonate the controller (the 1Panel CVE-2025-54424 class), enrollment and revocation were underspecified, offline detection was undefined, and 90-day certificates locked out hosts offline for a month.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0003` of the spec.

````markdown
- **Identity.** The controller runs an internal Ed25519 certificate authority
  (10-year lifetime, key custody in ADR 0005). Controller certificates carry
  `serverAuth` and the URI SAN `darkpanel://controller/<controller id>`; agent
  certificates carry only `clientAuth` and `darkpanel://host/<host id>`. Agents
  verify the chain and require the controller SAN they pinned at enrollment;
  the controller requires a host SAN, a known host and an unrevoked serial.
- **Transport.** The agent dials the controller's agent listener (default port
  7443, bound to the Tailscale interface by default; any other bind is
  explicit). TLS 1.3, ALPN `darkpanel-agent/1` requires a client certificate;
  ALPN `darkpanel-enroll/1` accepts none and serves only enrollment. The
  connection carries a yamux session with stream limits and deadlines; the
  controller sends HTTP/1.1 requests to the agent handler over streams it
  opens, and the agent pushes events over streams it opens.
- **Agent endpoints.** `GET /agent/v1/hello`; `POST /agent/v1/actions/{name}/{version}/{plan|apply|confirm|revert}`;
  `GET /agent/v1/resources/{module}/{resource}`; `POST /agent/v1/journal/ack`.
- **Hello** is exchanged both ways: protocol version, binary version, OS, and
  for the agent its modules, action versions and schema hashes. A protocol or
  version outside the supported window is shown on the host (R27) and only
  updates are dispatched.
- **Liveness.** Heartbeat every 15 s; a host is offline after 45 s without one,
  and actions to it fail immediately.
- **Enrollment.** The admin creates a host record; the controller issues a
  single-use token bound to that host id: 128-bit random, stored hashed,
  valid one hour, compared in constant time, consumed atomically, rate
  limited. The token carries the authority fingerprint and controller SAN. The
  agent verifies the controller against them before sending the token, then
  sends a certificate signing request; the controller ignores any identity in
  the request and issues a certificate for the bound host id.
- **Certificates and revocation.** Agent certificates last one year and are
  renewed after two thirds of their life; renewal revokes the previous serial.
  Revocation is by serial, checked at every handshake, and closes the host's
  live session. An agent with an expired certificate re-enrolls with a new
  token.
- **Journal delivery.** The agent journal is forwarded in sequence order; the
  controller acknowledges by sequence and deduplicates; acknowledged entries
  are pruned, unacknowledged ones are kept up to a size cap.
````

- Alternatives considered:
  - Controller connects in to agents: rejected by the owner on 2026-10-17; it needs an inbound port per host.
  - WebSocket with JSON-RPC: rejected; a custom RPC layer and a separate API for the local CLI.
  - gRPC bidirectional streams: rejected; a second schema system and larger binaries.
  - Reverse HTTP/2 over the dialed connection: rejected; only the client opens streams, so agent-pushed events would need long-polling.
  - 90-day certificates: rejected; see Context.
- Consequences: the controller keeps a host table, revoked serials and per-agent schema hashes; the agent listener has no web routes; admins create a host record before enrolling; enrollment tests must cover token reuse, a CSR claiming another host id, a revoked serial and a closed live session.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0003-agent-protocol.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0003-agent-protocol.md
git commit -m "docs(adr): add ADR 0003 agent protocol"
```

---

### Task 5: ADR 0004 change lifecycle

**Files:**
- Create: `docs/adr/0004-change-lifecycle.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (action classes, actors).
- Produces: `docs/adr/0004-change-lifecycle.md`.

**Items:** 5

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0004-change-lifecycle.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Change lifecycle, locking and recovery` and `**Requirements:** R1, R10, R18, R28, R39, R40, R41, R42`.

- Context: success measure S1 (no firewall change can lock the owner out); R10 as amended on 2026-10-17 (revertible, compensable and irreversible actions); review findings 3, 8, 9, 10, 24: a crash between mutation and persisting the deadline caused a permanent lockout, a lost apply response could act twice, per-module locks missed shared resources, controller-side actions had no lifecycle, and snapshot formats were unversioned.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0004` of the spec.

````markdown
- **Plan** records host, action and version, principal, canonical input hash,
  guard results (for example the lockout check), a diff, and a hash of the
  state the action's lock keys cover. It expires after 10 minutes. Apply takes
  the plan id only, never new input; the caller is re-authorized on apply and
  confirm.
- **Locks** are taken by key (for example `firewall`), declared per action, held
  from apply until the change is confirmed, reverted or completed.
- **Apply ordering**, for revertible actions:
  1. Re-check guards and the state hash; refuse on mismatch.
  2. Persist snapshot, intent and revert deadline to the agent database
     (fsync) before touching the host.
  3. Mutate; run verify. If verify fails, revert immediately.
  4. Record the result against the plan id. A repeated apply with the same
     plan id returns the stored result instead of acting twice.
- **Confirm and revert** are arbitrated in one transaction; whichever commits
  first wins. Deadlines use the monotonic clock while running and wall-clock
  deadlines on restart.
- **Startup recovery.** On start the agent first processes incomplete changes:
  an intent without a result is reverted from its snapshot; an overdue
  unconfirmed change is reverted; then it accepts requests.
- **Compensable and irreversible actions** record intent and result but arm no
  timer; compensable actions name their compensating action.
- **Controller-side actions** (cloud APIs such as the tailnet policy) follow the
  same plan, apply and journal steps in the controller database, with
  snapshots where the remote API allows, and no revert timer.
- Snapshot and journal records carry a format version; new binaries read all
  formats written by the previous minor release.
````

- Alternatives considered:
  - Revert timer in the browser or the controller: rejected; a lost connection or controller restart would leave an unconfirmed change in place.
  - Hashing the whole host state for plans: rejected; unrelated changes such as a recreated container would refuse valid plans.
  - One global lock per host: rejected; it serializes unrelated modules for no safety gain.
- Consequences: every mutating action declares a class and lock keys; agents must fsync before mutating; agent startup runs recovery before serving; the M3 lockout guard plugs into plan guards; controller-side actions need snapshots where the remote API allows; integration tests kill the agent mid-apply (ADR 0008).

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0004-change-lifecycle.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0004-change-lifecycle.md
git commit -m "docs(adr): add ADR 0004 change lifecycle"
```

---

### Task 6: ADR 0005 storage, migrations, secrets and keys

**Files:**
- Create: `docs/adr/0005-storage-and-secrets.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (schemas: the `secret` tag).
- Produces: `docs/adr/0005-storage-and-secrets.md`.

**Items:** 6

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0005-storage-and-secrets.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Storage, migrations, secrets and keys` and `**Requirements:** R1, R8, R12, R34, R81`.

- Context: the binary is static with CGO disabled; updates must never break a production host (S5); review findings 2, 12, 19, 20, 33: restoring a backup on rollback erased pending reverts and enrollments, the secret key had no lifecycle, the authority key had no custody, a failed module migration could stop all startup, and the SQLite driver has connection caveats.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0005` of the spec.

````markdown
- SQLite through `modernc.org/sqlite` (exact versions pinned, including
  `modernc.org/libc`), write-ahead-log mode, pragmas set in the connection
  string, one writer connection: `/var/lib/darkpanel/controller.db` (owned by
  `darkpanel`) and `/var/lib/darkpanel/agent.db` (root).
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations: embedded SQL per module and role, numbered, forward-only, each in
  a transaction, recorded in `core_migrations` with a checksum. A changed
  checksum or failed core migration stops startup; a failed module migration
  marks only that module unavailable (ADR 0002).
- **Expand, then contract.** A release's migrations must leave the schema usable
  by the previous minor release's binary. Destructive changes (drop, rename)
  ship only in a later release, after nothing reads the old shape. A binary
  rollback therefore runs the old binary on the new schema without restoring
  data.
- Before migrating, the database is copied with `VACUUM INTO` to
  `/var/lib/darkpanel/backups/<update id>/` (mode 0600, last five kept), for
  disaster recovery only. The agent's journal and revert records are never
  restored from a backup.
- **Secrets.** XChaCha20-Poly1305 with a random 24-byte nonce and associated
  data `<table>.<column>.<row id>`; ciphertexts carry a key id so rotation can
  be added. Each role has its own key, created atomically at install
  (`/etc/darkpanel/controller.key` owned by `darkpanel`,
  `/etc/darkpanel/agent.key` owned by root, both 0600). Agents hold only the
  secrets their own modules use (for example Nginx Proxy Manager credentials
  on its host).
- **Certificate authority key** is stored encrypted in `controller.db`.
  `darkpanel controller backup` exports the database and the controller key
  into one encrypted archive; `restore` is the lost-controller path in the
  runbook (R81).
````

- Alternatives considered:
  - `mattn/go-sqlite3`: rejected; needs CGO.
  - PostgreSQL: rejected; an extra service on every controller host for a small data set.
  - Down migrations: rejected; rarely tested, and expand-then-contract makes a binary rollback safe without them.
  - Restoring the database on update rollback: rejected; see Context.
  - One shared secret key for controller and agent: rejected; the controller runs as `darkpanel` and the agent as root, so each role gets its own key.
- Consequences: every release's migrations must keep the previous minor's binary working, and destructive schema changes wait a release; the `secret` tag drives encryption and redaction everywhere; `darkpanel controller backup` and `restore` are M2 deliverables; losing `controller.key` without a backup means re-enrolling every host.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0005-storage-and-secrets.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0005-storage-and-secrets.md
git commit -m "docs(adr): add ADR 0005 storage and secrets"
```

---

### Task 7: ADR 0006 API, listeners, authentication and CLI

**Files:**
- Create: `docs/adr/0006-api-and-cli.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (errors, actors, action classes).
- Produces: `docs/adr/0006-api-and-cli.md`.

**Items:** 7

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0006-api-and-cli.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `API, listeners, authentication and CLI` and `**Requirements:** R1, R7, R12, R15, R16, R17, R20, R28, R31, R72`.

- Context: private by default (R20), every UI action available in the CLI (S4), break-glass access on the host (R31 as amended on 2026-10-17); review findings 7, 17, 18, 22, 25, 28, 32: CyberPanel CVE-2024-51567 was exploited through method-dependent authentication, break-glass commands did not fit the agent socket, the standard router rejects `v{n}:plan`, web TLS was undecided, and the generated CLI lacked input, secret and confirmation rules.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0006` of the spec.

````markdown
- **Listeners.** Web and API: 127.0.0.1 and the Tailscale interface by default,
  HTTPS with a `tailscale cert` certificate when the tailnet has HTTPS enabled,
  otherwise a certificate from the internal authority; plain HTTP only on
  127.0.0.1. Agent listener: ADR 0003, no web routes. Local sockets: below.
- **Authentication boundary.** One middleware wraps every route on every
  listener and denies unless the route is in an explicit public list (login,
  health). It applies to every HTTP method. Sessions use Secure, HttpOnly,
  SameSite=Strict cookies with CSRF tokens; API tokens are scoped
  (read, operate, admin) and expire. Behind Nginx Proxy Manager or a tunnel,
  forwarded headers are trusted only from configured proxy addresses.
- **REST** under `/api/v1`: hosts; resources; actions at
  `/api/v1/hosts/{host}/actions/{name}/{version}/plan` and `.../apply`,
  controller-side actions at `/api/v1/actions/{name}/{version}/plan` and
  `.../apply`; `/api/v1/changes/{id}/confirm` and `/revert`; audit; auth.
  (`{version}` is a whole path segment such as `v1`, which the standard router
  requires.)
- Live updates over server-sent events at `/api/v1/events`; OpenAPI 3.1 at
  `/api/v1/openapi.json`, generated from the registry; `/api/v1` is additive
  only (ADR 0002).
- **CLI**, generated from the registry: `darkpanel <module> <noun> <verb>`.
  Scalar inputs are flags; nested inputs come from `--input <file|->` as JSON;
  secret fields are prompted or read from stdin, never accepted as flags. Read
  actions print tables or `--json`. Mutating actions show the plan, then ask;
  `--yes` answers that question only. A revertible change is confirmed only by
  `--confirm` or by `darkpanel change confirm <id>`. Top-level names `serve`,
  `agent`, `update`, `user`, `controller`, `change`, `uninstall`, `version`
  are reserved.
- **Local access.** The agent serves its handler on `/run/darkpanel/agent.sock`
  (root, 0600) for every agent action, checked with `SO_PEERCRED` uid 0, with
  actor `local:root@<host>`; entries are journaled and reach the audit log when
  the controller is reachable (R28). The controller serves
  `/run/darkpanel/controller.sock` (0600, `darkpanel`, root allowed via
  `SO_PEERCRED`) for controller break-glass commands such as
  `darkpanel user reset-totp`. `darkpanel update rollback` needs neither
  service (ADR 0007).
````

- Alternatives considered:
  - `v1:plan` style custom methods in paths: rejected; the standard library router needs whole-segment wildcards.
  - WebSockets for live updates: rejected; server-sent events are one-way, which is all the UI needs, and work through proxies.
  - Accepting secrets as CLI flags: rejected; they leak into shell history and process lists.
  - File mode alone for socket access: rejected; `SO_PEERCRED` also identifies the actor for audit.
- Consequences: route registration is generated from the registry, so no handler can skip the middleware; the controller needs a certificate source (tailnet or internal authority); trusted proxy addresses are configuration that M6 panel exposure sets; reserved top-level names constrain module ids.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0006-api-and-cli.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0006-api-and-cli.md
git commit -m "docs(adr): add ADR 0006 API and CLI"
```

---

### Task 8: ADR 0007 release, signing, update and rollback

**Files:**
- Create: `docs/adr/0007-release-and-update.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts); README shared contracts (version skew).
- Produces: `docs/adr/0007-release-and-update.md`.

**Items:** 8

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0007-release-and-update.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Release, signing, update and rollback` and `**Requirements:** R1, R29, R31, R32, R33, R34, R35, R36`.

- Context: success measure S5 (upgrades never break; automatic rollback within two minutes); the owner chose CI signing in an approval-gated environment on 2026-10-17; review findings 4, 13, 14, 16: a new binary that cannot start cannot roll itself back, a compromised build would be signed (the Webmin 1.890–1.920 backdoor came from the build server), key rotation stranded hosts that skip releases, a replayed older signed file allowed downgrades, and upgrade ordering was undefined.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0007` of the spec.

````markdown
- **Build provenance.** Releases build from a clean checkout of a signed tag
  with pinned toolchain and dependencies, `-trimpath`, and third-party GitHub
  Actions pinned by commit SHA. The build job publishes GitHub artifact
  attestations.
- **Signing.** A separate job in the protected environment `release` (owner
  approval required) holds the minisign key, verifies the attestations against
  the tag's commit, and signs `checksums.txt`. The signature's trusted comment
  carries version, channel and the checksum file's purpose, so a replayed older
  file is detected. Verification uses `aead.dev/minisign`, pinned.
- **Keys.** Binaries embed a trusted key list. Rotation: releases are signed by
  both the old and the new key for at least two minor releases; a host that
  skips past the window updates through the last dual-signed release first.
  Emergency revocation of a key is a release signed by the remaining key.
- **Update transaction.** Run by the currently installed (old) binary as a
  transient systemd unit, independent of the services:
  1. Refuse while any revertible change is unconfirmed on the host; take all
     lock keys for the duration.
  2. Download, verify signature, trusted comment (refuse downgrades unless
     forced) and checksum; run the new binary's self-test.
  3. Write a transaction record, keep the current binary as `darkpanel.prev`,
     swap atomically, restart the services.
  4. Health-check within the limit (S5: two minutes); on failure, swap back and
     restart.
- The service units set `OnFailure=darkpanel-rollback.service`, which runs
  `darkpanel.prev update rollback`, so a new binary that cannot start is rolled
  back even after a reboot.
- On the controller's host the controller updates first, then its agent;
  fleet rollouts go one host at a time and stop at the first failure, with
  progress stored so an interrupted rollout resumes.
- Releases are published on GitHub Releases of `github.com/Darkraise/darkpanel`.
  The install script embeds the trusted keys and verifies before installing;
  its exact method is decided in the M2 install epic.
````

- Alternatives considered:
  - The owner signs locally: rejected by the owner on 2026-10-17 in favour of the gated environment; manual and slower.
  - Sigstore keyless signing: rejected; a much larger verification dependency and reliance on Sigstore services.
  - The service updating itself in-process: rejected; a crash during the swap leaves no process to roll back.
  - Writing minisign verification ourselves: rejected; the format is stable and `aead.dev/minisign` is small and pinned.
- Consequences: an update is refused, not queued, while a revertible change is unconfirmed; the build workflow pins actions by commit SHA; a key rotation spans at least two minor releases; the M2 self-update epic implements the transaction, the rollback unit and resumable rollouts; the M2 install epic decides the install script's verification method.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0007-release-and-update.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0007-release-and-update.md
git commit -m "docs(adr): add ADR 0007 release and update"
```

---

### Task 9: ADR 0008 integration test harness

**Files:**
- Create: `docs/adr/0008-test-harness.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts).
- Produces: `docs/adr/0008-test-harness.md`.

**Items:** 9

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0008-test-harness.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Integration test harness` and `**Requirements:** R21, R49, R75`.

- Context: success measures S1, S2, S3 and S5 need real UFW, Docker and tailscaled on four operating systems; the owner chose QEMU/KVM on GitHub Actions on 2026-10-17; the development host is an LXC container without `/dev/kvm`; review finding 26: nested virtualization on GitHub runners is not officially supported, a scanner inside a private range would be exempted by ufw-docker rules, and IPv6 and fault injection were missing.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0008` of the spec.

````markdown
- QEMU/KVM VMs on GitHub-hosted Linux runners from pinned, checksummed
  official cloud images (Ubuntu 22.04 and 24.04, Debian 12 and 13) with
  cloud-init, one operating system per matrix job. A first step checks for
  `/dev/kvm` and fails loudly if it is missing; GitHub documents nested
  virtualization as not officially supported, so this is a known risk with
  self-hosted KVM runners as the fallback.
- A scanner VM sits on an "internet" network addressed outside private ranges
  (198.18.0.0/15 and an IPv6 documentation prefix), so ufw-docker private-range
  exemptions cannot mask exposure; IPv4 and IPv6 are both tested.
- Fault injection: kill the agent mid-apply, reboot during an unconfirmed
  change, and ship a deliberately broken binary to the updater.
- Tailscale tests use a dedicated test tailnet with ephemeral auth keys stored
  as CI secrets (needed from M5).
- arm64 is covered by builds and unit tests in v1; arm64 VM tests are Unknown
  until GitHub arm64 runners are confirmed to expose KVM.
- Local runs need a KVM-capable machine; the development host is an LXC
  container that could get one if Proxmox passes `/dev/kvm` through.
````

- Alternatives considered:
  - Proxmox VMs in the owner's homelab: rejected as the primary backend; CI and contributors could not use it.
  - Privileged Docker containers with systemd: rejected; they share the host kernel and cannot prove per-distribution firewall behaviour.
  - Scanner on a private network: rejected; see Context.
- Consequences: the harness epic builds image pinning, VM boot, the scanner network and fault injection; a missing `/dev/kvm` fails the job instead of skipping tests; M5 needs a test tailnet and CI secrets; arm64 VM coverage stays Unknown until confirmed.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0008-test-harness.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0008-test-harness.md
git commit -m "docs(adr): add ADR 0008 test harness"
```

---

### Task 10: ADR 0009 host execution safety

**Files:**
- Create: `docs/adr/0009-host-execution-safety.md`

**Interfaces:**
- Consumes: ADR template and titles (Contracts).
- Produces: `docs/adr/0009-host-execution-safety.md`.

**Items:** 10

**Depends on:** Task 1
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the checker to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0009-host-execution-safety.md`
Expected: exit 1.

- [ ] **Step 2: Write the ADR**

Create the file from the ADR template with title `Host execution safety` and `**Requirements:** R11, R30, R54`.

- Context: the agent runs as root (ADR 0001); review finding 11: argument vectors alone still allow option injection, PATH lookup and inherited-environment abuse, and file ownership was unenforced; Coolify's January 2026 advisories showed user strings reaching shell commands and running as root.
- Decision: state every bullet of the decision text below, in the same order, with identical values (ports, paths, lifetimes, names, numbers). Add nothing it does not say. This text is section `### 0009` of the spec.

````markdown
- **Commands.** The exec provider runs only binaries from an allowlist of
  absolute paths, with a fixed minimal environment, a timeout from the caller's
  context, and argument vectors only; values that could be read as options are
  separated with `--` where the tool supports it. Input schemas constrain
  values with patterns (ports, names, CIDRs, comments) before they reach a
  provider.
- **Files.** The file provider writes only paths and marked blocks declared in
  the module manifest; it refuses symlinks (`O_NOFOLLOW`), writes atomically via
  a temporary file and rename, and records ownership in the ownership manifest
  that `darkpanel uninstall` reverses (R30).
- Prefer APIs over tools: Docker Engine API, Tailscale local API, systemd over
  D-Bus.
````

- Alternatives considered:
  - Running commands through `sh -c`: rejected; shell parsing of user-supplied values is the injection class above.
  - Parsing command-line tool output where an API exists (`docker ps`): rejected; output formats change between versions, and R54 requires the Docker Engine API.
  - Unrestricted file writes from module code: rejected; uninstall could not reverse them and a symlink could redirect a root write.
- Consequences: `internal/platform` is the only package that runs programs or writes host files; every module declares its owned paths and marked blocks in its manifest (ADR 0002); the exec provider's allowlist of absolute binary paths lives in `internal/platform`; input schemas carry patterns for every value passed to a program; the ownership manifest drives `darkpanel uninstall`.

- [ ] **Step 3: Check the substance**

Compare your `## Decision` section with the decision text in Step 2, bullet by bullet. Every bullet must be present with identical values, and nothing may be added. In your task result, list each bullet's first words with `present`.

- [ ] **Step 4: Run the checker**

Run: `bash scripts/check-adrs.sh docs/adr/0009-host-execution-safety.md; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0009-host-execution-safety.md
git commit -m "docs(adr): add ADR 0009 host execution safety"
```

---

### Task 11: Traceability table

**Files:**
- Modify: `docs/adr/README.md` (append a section at the end)

**Interfaces:**
- Consumes: README from Task 1; ADR files, titles and Requirements lines (Contracts, Tasks 2–10).
- Produces: the README section `## Traceability`, which `scripts/check-adrs.sh --complete` verifies.

**Items:** 11

**Depends on:** Task 1, Task 2, Task 3, Task 4, Task 5, Task 6, Task 7, Task 8, Task 9, Task 10
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 0 = 1

- [ ] **Step 1: Run the complete check to confirm all ADRs exist**

Run: `bash scripts/check-adrs.sh --complete; echo "exit=$?"`
Expected: `exit=0`. If it fails, stop and report the failing lines; do not create or edit ADRs.

- [ ] **Step 2: Append the traceability section**

Append exactly this to the end of `docs/adr/README.md`:

```markdown
## Traceability

Product-spec requirements that depend on these decisions, with the part of
each ADR's Decision section that serves them. `none` means the requirement is
decided by its own epic, not by an ADR.

| Requirement | ADRs (Decision part) |
|---|---|
| R1 | 0002 (module contract), 0003 (protocol), 0004 (change lifecycle), 0005 (storage and migrations), 0006 (API versioning), 0007 (release signing) |
| R5 | 0002 (manifest interface, compiled-in modules) |
| R6 | 0002 (compatibility rules) |
| R7 | 0002 (action declaration), 0006 (REST, CLI) |
| R8 | 0005 (migrations) |
| R9 | 0002 (enablement and preflight) |
| R10 | 0004 (plan, apply ordering, compensable and irreversible actions) |
| R11 | 0009 (commands, files) |
| R12 | 0005 (secrets), 0006 (authentication boundary) |
| R15, R16, R17 | 0006 (authentication boundary) |
| R18 | 0003 (journal delivery), 0004 (journal) |
| R19 | none: a stored role field, decided by the identity epic |
| R20 | 0006 (listeners) |
| R21 | 0008 (harness) |
| R22 | 0003 (identity, transport) |
| R23 | 0003 (enrollment, certificates and revocation) |
| R24 | 0002 (schema hashes), 0003 (hello) |
| R25 | 0003 (hello) |
| R26 | 0001 (process model) |
| R27 | 0003 (hello, liveness) |
| R28 | 0003 (journal delivery), 0004 (startup recovery), 0006 (local access) |
| R29 | 0007 (release hosting; method deferred to the M2 install epic) |
| R30 | 0009 (files) |
| R31 | 0006 (local access), 0007 (rollback unit) |
| R32 | 0007 (signing, keys) |
| R33 | 0007 (update transaction) |
| R34 | 0005 (expand then contract, backups), 0007 (update transaction) |
| R35 | 0007 (trusted comment carries the channel; scheduling is decided by the self-update epic) |
| R36 | 0007 (rollouts) |
```

- [ ] **Step 3: Run the checks**

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete; echo "exit=$?"`
Expected: `check-adrs tests: 15 passed, 0 failed` and `exit=0`. The `--complete` run checks every requirement–ADR pair in the table against the ADRs' `**Requirements:**` lines. If it reports `traceability maps ...` lines, do not edit the ADRs: commit, and report every such line in the task result for Task 12.

- [ ] **Step 4: Commit**

```bash
git add docs/adr/README.md
git commit -m "docs(adr): add requirement traceability table"
```

---

### Task 12: Cross-ADR reconciliation

**Files:**
- Modify: `docs/adr/0001-stack-and-layout.md`, `docs/adr/0002-module-contract.md`, `docs/adr/0003-agent-protocol.md`, `docs/adr/0004-change-lifecycle.md`, `docs/adr/0005-storage-and-secrets.md`, `docs/adr/0006-api-and-cli.md`, `docs/adr/0007-release-and-update.md`, `docs/adr/0008-test-harness.md`, `docs/adr/0009-host-execution-safety.md` (any of them, wherever a check below finds a discrepancy)

**Interfaces:**
- Consumes: all nine ADRs; the spec; README shared contracts and traceability table (authoritative; never edited here); any `traceability maps` lines Task 11 reported.
- Produces: ADRs that match the spec and each other, with `bash scripts/check-adrs.sh --complete` passing.

**Items:** 11

**Depends on:** Task 2, Task 3, Task 4, Task 5, Task 6, Task 7, Task 8, Task 9, Task 10, Task 11
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Compare every ADR with the spec**

For each ADR 0001–0009, read its `## Decision` section beside the spec section with the same number (`### 000N` in `docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md`). Record, with file and line, every bullet that is missing, every value that differs, and every decision the ADR adds that the spec does not contain. Also check that `## Alternatives considered` and `## Consequences` contradict neither the ADR's own Decision nor any other ADR.

- [ ] **Step 2: Check the cross-ADR facts**

Every ADR that mentions one of these facts must state the spec's value; record each mismatch with file and line:

1. Agent listener port 7443 and its default bind to the Tailscale interface — 0003, 0006.
2. Agent certificates: one year, renewed after two thirds, revocation by serial closing the live session — 0003, 0005.
3. Controller runs as user `darkpanel`; the agent is the only long-running root service — 0001, 0005, 0006.
4. Key files `/etc/darkpanel/controller.key` (owner `darkpanel`) and `/etc/darkpanel/agent.key` (root), both mode 0600 — 0005, 0006.
5. Socket paths `/run/darkpanel/agent.sock` and `/run/darkpanel/controller.sock` and their `SO_PEERCRED` rule — 0003, 0006.
6. Updates are refused while a revertible change is unconfirmed and hold all lock keys — 0004, 0007.
7. Expand-then-contract migrations; a binary rollback never restores the database; the journal is never restored — 0005, 0007.
8. Action classes `read`, `revertible`, `compensable`, `irreversible` used as the README defines them — 0002, 0004, 0006.
9. The `secret:"true"` tag drives encryption, write-only API fields and redaction — 0005, 0006.
10. Version skew: controller never older than agents, updated first, supports the same and previous minor — 0002, 0003, 0007.
11. Two-minute health-check limit and the `OnFailure=darkpanel-rollback.service` unit — 0007.
12. Plan expiry 10 minutes; apply by plan id only — 0004, 0006.
13. ADR cross-references use `ADR 000N` with the titles in the README index.
14. No ADR restates a shared contract with different wording.

- [ ] **Step 3: Fix each discrepancy**

Edit the ADR (any section, any of the nine files) so that it states the spec's value; for a `traceability maps` line, add the missing requirement to that ADR's `**Requirements:**` line. Never edit the README or the spec. When the spec itself is silent or ambiguous about a discrepancy, do not choose a value: record it as `UNRESOLVED` with both readings.

- [ ] **Step 4: Run the checks**

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete; echo "exit=$?"`
Expected: `check-adrs tests: 15 passed, 0 failed` and `exit=0`.

- [ ] **Step 5: Commit**

Only when Step 3 changed files:

```bash
git add docs/adr
git commit -m "docs(adr): reconcile ADRs with spec and each other"
```

- [ ] **Step 6: Report**

In the task result, list every finding from Steps 1 and 2 as `fixed (<file>)` or `UNRESOLVED (<both readings>)`, and each Step 2 fact with no finding as `consistent`. If any item is `UNRESOLVED`, end the task with status BLOCKED: the controller takes the items to the owner, and after the ruling this task runs again from Step 1. The task succeeds only when no item is `UNRESOLVED`.
