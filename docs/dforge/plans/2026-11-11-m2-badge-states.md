# M2 Transaction Badge States Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the update transaction states that UI-4 now names their badge variants: `pending`, `downloading`, `staged`, `swapped` and `rolling-back` show `secondary`, and `rolled-back` shows `destructive` (core register row 117).

**Architecture:** One mapping change in the shared `stateVariant` helper that the host Overview history and the Rollouts page already use, pinned by the existing UI-4 unit test, and proven in a real browser by one more forced state in the host-page state walk.

**Tech Stack:** React 19, darkraise-ui 6.9.6, Vitest, Playwright 1.64.

**Spec:** docs/dforge/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** inline — `claude --model sonnet --effort high` — Task 1 scores 5

**Parallelism:** sequential — inline execution

**Task check:** `make lint test build verify-build` — rule 2: the full suite last passed in 62 s

**Integration check:** `make lint test build verify-build`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Plan review:** 2026-11-11 — dforge:judge-opus — executability 15 / coherence 18 / coverage 18 / assumptions 13 (round 1)

## Global Constraints

- UI-4 as amended 2026-11-11: `committed`, `succeeded` → `default`; `updating`, `running`, `draining`, `waiting`, `pending`, `downloading`, `staged`, `swapped`, `rolling-back` → `secondary`; `failed`, `quarantined`, `rolled-back` → `destructive`; `skipped`, `cancelled` and any other state → `outline`. Every badge carries its state as text, never colour alone.
- Components and tokens come from darkraise-ui 6.9.6; no raw colours outside its tokens.
- Every test that proves a contract item has a name starting with that item's ID and a colon.
- UI/UX depth is touch: the proof report carries a `## Delta`, and its judge is `dforge:judge-sonnet-high`.
- Viewports 375x812, 768x1024 and 1440x900; themes light and dark (`docs/dforge/ui-ux/profile.md`).

## Contracts

None — one task.

## Assumptions (evidence)

- `stateVariant(state: string): BadgeVariant` in `web/src/lib/badges.ts` is the only mapping the host Overview history (`web/src/pages/host-overview.tsx`, the `Badge variant={stateVariant(t.state)}` line) and the Rollouts page (`web/src/pages/rollouts.tsx`) use; `rg -n stateVariant web/src`, 2026-11-11.
- The update transaction states are `pending`, `downloading`, `staged`, `swapped`, `rolling-back`, `committed`, `rolled-back` and `failed` (`internal/core/fleet.go`, the `Update*` constants); none of the new ones is a rollout or rollout-host state (`Rollout*` constants in the same file), so rollout badges do not change.
- `web/e2e/m2-web-hosts-states.pw.ts` forces a read's answer with `answerRead(page, action, { output })`, registered after `mockApi` so it wins, and screenshots each `STATES` row at every viewport and theme into `.dforge/ui-ux/m2-web-hosts/`; read 2026-11-11.
- The `core.update.status` read answers `{ output: { transactions: [...] } }`, each transaction carrying `id`, `operationId`, `parentId`, `kind`, `fromVersion`, `toVersion`, `targetVersion`, `state`, `error`, `startedAt` and `updatedAt` (`web/src/pages/host-overview.test.tsx`, the `tx` constant).
- The ui-proof gate's UI paths are `web/src/*.tsx`, `web/src/*.css` and `web/e2e/*.ts` (`docs/dforge/ui-ux/profile.md`); this task changes `web/e2e/m2-web-hosts-states.pw.ts`, so `ui-proof check` gates the branch.
- `plan-lint` reports 15 errors "UI/UX contract item <id> is named by no task's **UI/UX:** line" for UI-1 to UI-3 and UX-1 to UX-12: it expects one plan to deliver the spec's whole contract, but those items were built by `docs/dforge/plans/2026-11-11-m2-web-hosts.md` (merged 4e5fd58) and proven in `docs/dforge/ui-ux/proofs/2026-11-11-m2-web-hosts.md`. This follow-up plan delivers only the amended UI-4. On 2026-11-11 the owner was offered "1: cite only UI-4 and record the errors as a false positive" or "2: split UI-4 into a separate one-item spec", with option 1 announced as the default if unanswered; the owner's reply ("none") answered only the executor question asked alongside, so option 1 stands as that announced default, not as an explicit owner acceptance.
- The host Overview wraps the history table in `overflow-x-auto` with `min-w-[40rem]` (`web/src/pages/host-overview.tsx`), so at 375x812 the State column sits outside the visible box until the container scrolls; `fullPage` does not scroll it, and Playwright's `toBeVisible()` passes for clipped content.
- `web/src/boards/boards.tsx` keeps its own copy of the old mapping for the state boards; no board row uses a new state, so it is left as is and reported to the owner.
- Plan review round 1 scored assumptions 13 (borderline): its two causes, the unverified lint ruling and the clipped 375 table, are answered by the two bullets above; no second round, as no score was 8 or below.

## Task index

1. Transaction state badge variants

---

### Task 1: Transaction state badge variants

**Files:**
- Modify: `web/src/lib/badges.ts`
- Modify: `web/e2e/m2-web-hosts-states.pw.ts`
- Test: `web/src/lib/badges.test.ts`
- Create: `docs/dforge/ui-ux/proofs/2026-11-11-m2-badge-states.md`, `docs/dforge/ui-ux/proofs/2026-11-11-m2-badge-states.judge.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `stateVariant` keeps its signature; only its mapping grows.

**Items:** 117

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

**UI/UX:** UI-4

- [ ] **Step 1: Extend the test `web/src/lib/badges.test.ts::UI-4: each host and rollout state maps to its badge variant`**

Rename it to `UI-4: each host, rollout and transaction state maps to its badge variant`, and add these rows to its `cases` array, before `["anything", "outline"]`:

```ts
    ["pending", "secondary"], ["downloading", "secondary"], ["staged", "secondary"], ["swapped", "secondary"], ["rolling-back", "secondary"],
    ["rolled-back", "destructive"],
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm -C web exec vitest run src/lib/badges.test.ts`
Expected: FAIL — `expected 'outline' to be 'secondary'` for `pending`.

- [ ] **Step 3: Implement**

1. `stateVariant` in `web/src/lib/badges.ts`: add `pending`, `downloading`, `staged`, `swapped` and `rolling-back` to the cases that return `"secondary"`, and `rolled-back` to the cases that return `"destructive"`. Every other state keeps its variant, and the default stays `"outline"`.
2. `web/e2e/m2-web-hosts-states.pw.ts`, the browser proof:
   - Above `STATES`, add a constant holding one transaction per update state, newest first:

     ```ts
     const TX_STATES = ["pending", "downloading", "staged", "swapped", "rolling-back", "rolled-back", "committed", "failed"]
     const TRANSACTIONS = TX_STATES.map((state, i) => {
       const at = new Date(Date.UTC(2026, 10, 11, 8 - i)).toISOString()
       const failed = state === "rolled-back" || state === "failed"
       return {
         id: `tx-${i}`, operationId: `tx-${i}`, parentId: null, kind: "update",
         fromVersion: "1.0.0", toVersion: `1.1.${i}`, targetVersion: `1.1.${i}`, state,
         error: failed ? { code: "update.health_failed", message: "the health check failed" } : null,
         startedAt: at, updatedAt: at,
       }
     })
     ```

   - Add a `STATES` row after the `history` row: `{ id: "UI-4", state: "transactions", path: \`/hosts/${HOST_ID}\`, worstCase: true, setup: (p) => answerRead(p, "core.update.status", { output: { transactions: TRANSACTIONS } }), shows: (p) => p.getByRole("table", { name: "Update history" }) }`.
   - In the `STATES` loop, when `s.state === "transactions"`, after `expect(shown).toBeVisible()`: assert every state's text is in the table, `for (const t of TX_STATES) await expect(shown.getByText(t, { exact: true })).toBeVisible()`; run `await expectNoAxeViolations(page, "transactions")`; and, after the loop's `shown.scrollIntoViewIfNeeded()`, call `await shown.getByText("rolled-back", { exact: true }).scrollIntoViewIfNeeded()` so the table's own horizontal scroll brings the State column into view at 375x812. Pass `fullPage: s.state === "transactions"` to that loop's `page.screenshot` so all eight rows are captured. No other row's behaviour or screenshot changes.
   - This row is browser evidence for the proof, not a regression test of the variants: its assertions pass without the `badges.ts` change. The unit test of Step 1 pins the mapping.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm -C web exec vitest run src/lib/badges.test.ts && pnpm -C web exec playwright test e2e/m2-web-hosts-states.pw.ts e2e/m2-web-hosts.pw.ts`
Expected: PASS; six new `UI-4: transactions <viewport>-<theme>` tests, `.dforge/ui-ux/m2-web-hosts/UI-4-<viewport>-<theme>-transactions.png` for all six viewport and theme pairs, and the rollouts page screenshots `UI-3-<viewport>-<theme>.png` and `UI-3-<viewport>-<theme>-failed.png` regenerated. The run also regenerates `UI-2-*.png` and `UX-2-*-history.png`, whose fixture `rolled-back` row now shows `destructive` instead of `outline`: that is this change, not a regression.

- [ ] **Step 5: Prove and check**

1. Follow the ui-ux walkthrough (`/root/.claude-alt/plugins/cache/darkraise/dforge/4.15.2/skills/ui-ux/references/walkthrough.md`) at touch depth with slug `m2-badge-states`; Step 4's Playwright run is the walk, and its screenshots stay under `.dforge/ui-ux/m2-web-hosts/` (the spec file's `EVIDENCE` directory), which the report says. Open each `UI-4-*-transactions.png` and check the colours by eye. Write `docs/dforge/ui-ux/proofs/2026-11-11-m2-badge-states.md` with `**Contract:** touch` and this `## Delta`, one result row per ID:

   ```markdown
   ## Delta

   **UI/UX depth:** touch — update transaction states get their UI-4 badge variants

   - **UI-1** — `pending`, `downloading`, `staged`, `swapped` and `rolling-back` history badges render `secondary`.
   - **UI-2** — A `rolled-back` history badge renders `destructive`, with its error in the Error column.
   - **UI-3** — `committed` stays `default` and `failed` stays `destructive`.
   - **UI-4** — Every history badge shows its state as text.
   - **UI-5** — Rollout and rollout-host badges are unchanged: the Rollouts page still shows `succeeded` and `committed` as `default`, `updating` and `waiting` as `secondary`, `failed` as `destructive`, `skipped` and `cancelled` as `outline`.
   - **OP-1** — Proven at 375x812, 768x1024 and 1440x900, light and dark, with no axe violation on the host page showing all eight transaction states (the `UI-4: transactions` tests' axe scan).
   ```

   The Delta's IDs are local to the report; the `UI-4: transactions` tests and `UI-4-*-transactions.png` screenshots are named for the spec's UI-4, and each Delta row names the screenshot or test it cites. UI-1 to UI-4 and OP-1 cite the `UI-4-<viewport>-<theme>-transactions.png` screenshots and the `UI-4: transactions` tests. UI-5 cites the regenerated `UI-3-<viewport>-<theme>.png` (running rollout: `committed`, `updating`, `waiting`; finished ones `succeeded`, `cancelled`) and `UI-3-<viewport>-<theme>-failed.png` (`failed`, `skipped`) from Step 4's run. Stamp the report, have it judged by `dforge:judge-sonnet-high`, copy the judge's report beside it as `2026-11-11-m2-badge-states.judge.md`, and add the Judge line.
2. Run:

```bash
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.15.2/scripts/ui-proof check --base "$(git merge-base HEAD main)" && make lint test build verify-build
```

Expected: `ui-proof check` exits 0; lint, tests and build pass.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/badges.ts web/src/lib/badges.test.ts web/e2e/m2-web-hosts-states.pw.ts
git commit -m "feat(web): colour update transaction badges"
git add docs/dforge/ui-ux/proofs/2026-11-11-m2-badge-states.md docs/dforge/ui-ux/proofs/2026-11-11-m2-badge-states.judge.md
git commit -m "docs(ui-ux): prove the transaction badge variants"
```
