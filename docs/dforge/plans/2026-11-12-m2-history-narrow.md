# M2 Narrow Update History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the host Overview update history as a stacked list wherever its container is narrower than the table's 40rem minimum, so a transaction's state and error are visible without sideways scrolling (core register row 121), and make the state boards use the shared badge mapping (core register row 120).

**Architecture:** The history becomes a Tailwind container: a table wrapper shown at `@min-[40rem]` and wider, and a list shown below it, both rendered from the same transactions. A unit test pins the list's content, and the host-page browser walk pins which of the two shows at each viewport. `boards.tsx` drops its private `stateVariant` copy for `@/lib/badges`.

**Tech Stack:** React 19.3, Tailwind CSS 4.3.3 (built-in container queries), darkraise-ui 6.9.6, Vitest, Playwright 1.64.

**Spec:** docs/dforge/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** inline — `claude --model sonnet --effort high` — Task 1 scores 5

**Parallelism:** sequential — inline execution

**Task check:** `make lint test build verify-build` — rule 2: the full suite last passed in 173 s

**Integration check:** `make lint test build verify-build`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Plan review:** 2026-11-12 — dforge:judge-opus — executability 17 / coherence 19 / coverage 19 / assumptions 18 (round 1)

## Global Constraints

- UX-2 as amended 2026-11-12: where the history's container is narrower than the table's 40rem minimum, it renders as a list labelled "Update history", one item per transaction, newest first: the state badge and "<from> → <to>" on the first line, "<time> · <kind>" on the second in `text-xs text-muted-foreground`, and "Error: <message>" on a third line when the transaction has an error; nothing scrolls sideways. At 40rem and wider the table shows unchanged.
- UI-4: badge variants come from `stateVariant` in `web/src/lib/badges.ts`; every badge carries its state as text, never colour alone.
- Components and tokens come from darkraise-ui 6.9.6; no raw colours outside its tokens.
- Every test that proves a contract item has a name starting with that item's ID and a colon.
- UI/UX depth is touch: the proof report carries a `## Delta`, and its judge is `dforge:judge-sonnet-high`.
- Viewports 375x812, 768x1024 and 1440x900; themes light and dark (`docs/dforge/ui-ux/profile.md`).
- Run Playwright by file, never the whole suite: a full run rewrites the six tracked `docs/dforge/ui-ux/boards` PNGs (core register row 114); if one is rewritten, `git checkout -- docs/dforge/ui-ux/boards`.

## Contracts

None — one task.

## Assumptions (evidence)

- The history's container is about 1,110 px wide at 1440x900, about 438 px at 768x1024 and about 300 px at 375x812 (`.dforge/ui-ux/m2-web-hosts/UX-2-1440x900-light-history.png`, `UI-4-768x1024-light-transactions.png`, `UX-2-375x812-light-history.png`, read 2026-11-12), so a switch at 40rem (640 px) shows the table at 1440 only and the list at 768 and 375.
- Tailwind 4.3.3 (`web/package.json`) has container queries built in: `@container` on the parent, `@min-[40rem]:` on children; no plugin is needed. It also defines `wrap-anywhere` as `overflow-wrap: anywhere` (`web/node_modules/tailwindcss/dist/lib.js`, 2026-11-12). No other page uses a responsive variant yet (`rg '@container|md:hidden|hidden md:' web/src`, 2026-11-12).
- jsdom loads no CSS, so in Vitest both the table and the list are in the accessibility tree; the existing host-overview tests already scope their queries to the table (`web/src/pages/host-overview.test.tsx`, `findByRole("table", { name: "Update history" })`), so they keep passing. In a browser, `display: none` removes the hidden one, and Playwright role locators skip hidden elements.
- `focusableScroller("Update history")` (`web/src/lib/table.ts`) names the table's own scroll wrapper as a focusable region; inside a hidden parent that region is hidden and leaves the tab order, so the narrow layout has no unreachable tab stop.
- Queries that expect the history table at every viewport: `web/e2e/m2-web-hosts-states.pw.ts` (the `history` and `transactions` rows of `STATES`, and the OP-3 `ready` check for `/hosts/${HOST_ID}`) and `web/e2e/m2-web-hosts.pw.ts` (`toHaveCount(4)` on the table's rows); `rg -n 'Update history' web/e2e`, 2026-11-12.
- `web/src/boards/boards.tsx` (`function stateVariant`) is the old mapping without the in-progress transaction states or `rolled-back`; its board fixtures use none of the new states, so the board PNGs do not change. `BadgeVariant` is still used there by `BoardHost.modules`, so its import stays. The owner chose not to redraw the host-overview board for the narrow layout (2026-11-12).
- Row 120 is folded into this task rather than given its own: it is a one-file import swap with no behaviour of its own and no test that could fail for it; it gets its own commit.
- `plan-lint` reports "UI/UX contract item <id> is named by no task's **UI/UX:** line" for the contract items this follow-up plan does not touch: they were built by `docs/dforge/plans/2026-11-11-m2-web-hosts.md` (merged 4e5fd58) and proven in `docs/dforge/ui-ux/proofs/2026-11-11-m2-web-hosts.md`. This is the false positive recorded for `docs/dforge/plans/2026-11-11-m2-badge-states.md`; there is no scoping escape.

## Task index

1. Stacked update history at narrow widths

---

### Task 1: Stacked update history at narrow widths

**Files:**
- Modify: `web/src/pages/host-overview.tsx`
- Modify: `web/src/boards/boards.tsx`
- Modify: `web/e2e/m2-web-hosts-states.pw.ts`, `web/e2e/m2-web-hosts.pw.ts`
- Test: `web/src/pages/host-overview.test.tsx`
- Create: `docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.md`, `docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.judge.md`

**Interfaces:**
- Consumes: `stateVariant(state: string): BadgeVariant` from `web/src/lib/badges.ts`, unchanged.
- Produces: nothing other code uses.

**Items:** 120, 121

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 2 - coupling 0 - risk 1 = 5

**UI/UX:** UX-2, UI-4

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/host-overview.test.tsx`, add after the test `UX-2: the update history lists transactions newest first with state and error`:

```tsx
test("UX-2: the narrow update history lists each transaction with its state, versions, time, kind and error", async () => {
  const committed = { ...tx, id: "t2", operationId: "t2", state: "committed", error: null, startedAt: "2026-11-10T07:00:00Z" }
  stubFetch({ [STATUS]: { body: { output: { transactions: [tx, committed] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  const list = await screen.findByRole("list", { name: "Update history" })
  const items = within(list).getAllByRole("listitem")
  expect(items).toHaveLength(2)
  const [first, second] = items
  if (first === undefined || second === undefined) throw new Error("no items")
  within(first).getByText("rolled-back")
  within(first).getByText("1.0.0 → 1.1.0")
  within(first).getByText(`${formatTime("2026-11-11T07:00:00Z")} · update`)
  within(first).getByText("Error: the health check failed")
  within(second).getByText("committed")
  within(second).getByText(`${formatTime("2026-11-10T07:00:00Z")} · update`)
  expect(within(second).queryByText(/^Error:/)).toBeNull()
})
```

And extend the test `UI-4: each history badge carries the variant of its transaction state`: after its existing `for` line, add

```tsx
  const list = screen.getByRole("list", { name: "Update history" })
  for (const [state, variant] of states) expect(within(list).getByText(state).closest("[data-variant]")?.getAttribute("data-variant")).toBe(variant)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `pnpm -C web exec vitest run src/pages/host-overview.test.tsx`
Expected: FAIL — `Unable to find an accessible element with the role "list" and name "Update history"` in both tests; every other test passes.

- [ ] **Step 3: Implement**

1. `UpdateHistory` in `web/src/pages/host-overview.tsx`, the branch that renders the transactions (after the `No updates yet` return):
   - Wrap what it returns in one `div` with class `@container`.
   - Inside it, the existing `overflow-x-auto` div and its `Table` (with its comment, `aria-label`, `min-w-[40rem]` and `focusableScroller` ref) stay as they are, with `hidden @min-[40rem]:block` added to that div's classes.
   - After it, a `ul` with `aria-label="Update history"` and classes `flex flex-col divide-y rounded-md border @min-[40rem]:hidden`, holding one `li` per transaction in the same order as the table rows, keyed by `t.id`, with padding `px-3 py-2` and `flex flex-col gap-1`:
     - line 1, a `div` with `flex flex-wrap items-center gap-2`: the same `Badge` the table's State cell renders (`variant={stateVariant(t.state)} size="sm"`, text `t.state`), then a `span` with class `break-all` holding `` `${t.fromVersion} → ${t.toVersion}` ``;
     - line 2, a `p` with `text-xs text-muted-foreground` holding `` `${formatTime(t.startedAt)} · ${t.kind}` ``;
     - line 3, only when `t.error` is not null, a `p` with `text-sm wrap-anywhere` (Tailwind 4's `overflow-wrap: anywhere`, which lets the unbroken URL in the worst-case error break) holding `` `Error: ${t.error.message}` ``.
   - The loading, error, empty and offline states and the table itself do not change.
2. `web/src/boards/boards.tsx` (row 120): delete the local `function stateVariant(state: string): BadgeVariant` and add `import { stateVariant } from "@/lib/badges"` beside the other `@/lib` import. Keep `type BadgeVariant` in the darkraise-ui badge import: `BoardHost.modules` still uses it.
3. `web/e2e/m2-web-hosts-states.pw.ts`, the browser proof and the regression test for the switch:
   - Add `const history = (p: Page) => p.getByRole("table", { name: "Update history" }).or(p.getByRole("list", { name: "Update history" }))` above `STATES`, and use it as `shows` for the `UX-2` `history` row and the `UI-4` `transactions` row, and as the `ready` locator of the OP-3 `/hosts/${HOST_ID}` entry.
   - Worst-case data: declare above `TRANSACTIONS` the constants `const LONG_VERSION = \`1.1.0-beta.1+${"a".repeat(27)}\`` (40 characters) and `` const LONG_ERROR = `the health check failed: GET https://127.0.0.1:7443/api/v1/health/${"x".repeat(60)} answered 503 after 30 s, so the agent rolled back to 1.0.0 and restarted darkpanel-agent.service` `` (about 220 characters). In the `TX_STATES.map` that builds `TRANSACTIONS`, write `toVersion: i === 0 ? LONG_VERSION : \`1.1.${i}\``, the same for `targetVersion`, and the error message as `state === "rolled-back" ? LONG_ERROR : "the health check failed"`. The `failed` transaction keeps "the health check failed".
   - In the `STATES` loop, when `s.state` is `history` or `transactions`, right after `await expect(shown).toBeVisible()`:

     ```ts
     const narrow = vp.width < 1440
     await expect(page.locator(narrow ? "ul[aria-label='Update history']" : "table[aria-label='Update history']")).toBeVisible()
     await expect(page.locator(narrow ? "table[aria-label='Update history']" : "ul[aria-label='Update history']")).toBeHidden()
     await expect(page.locator(narrow ? "table[aria-label='Update history']" : "ul[aria-label='Update history']")).toHaveCount(1)
     if (narrow) {
       expect(await shown.evaluate((el) => el.scrollWidth <= el.clientWidth), "the list does not scroll sideways").toBe(true)
       for (const error of await shown.getByText(/^Error: /).all()) {
         expect(await error.evaluate((el) => el.scrollWidth <= el.clientWidth), "the error wraps inside its block").toBe(true)
       }
     }
     ```

     Test names stay `${s.id}: ${s.state} ${tag}`. The existing `transactions` assertions (every `TX_STATES` text visible, the axe scan, scrolling to `rolled-back`, the `fullPage` screenshot) stay.
4. `web/e2e/m2-web-hosts.pw.ts`, in `UX-1, UX-2, UX-8, UX-12: worst-case pages`: replace `await expect(page.getByRole("table", { name: "Update history" }).getByRole("row")).toHaveCount(4)` with a check that counts the list's three items below 1440 and the table's four rows (header included) at 1440:

   ```ts
   if (vp.width < 1440) await expect(page.getByRole("list", { name: "Update history" }).getByRole("listitem")).toHaveCount(3)
   else await expect(page.getByRole("table", { name: "Update history" }).getByRole("row")).toHaveCount(4)
   ```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm -C web exec vitest run src/pages/host-overview.test.tsx && pnpm -C web exec playwright test e2e/m2-web-hosts-states.pw.ts e2e/m2-web-hosts.pw.ts && test -z "$(rg -n 'function stateVariant' web/src/boards/boards.tsx)"`
Expected: PASS. The `UX-2: history` and `UI-4: transactions` tests at 375x812 and 768x1024 find the list and a hidden table; at 1440x900 the table and a hidden list. Screenshots `UX-2-<viewport>-<theme>-history.png`, `UI-4-<viewport>-<theme>-transactions.png` and `UI-2-<viewport>-<theme>.png` under `.dforge/ui-ux/m2-web-hosts/` show the list at 375 and 768. `git status --short docs/dforge/ui-ux/boards` prints nothing.

- [ ] **Step 5: Prove and check**

1. Follow the ui-ux walkthrough (`/root/.claude-alt/plugins/cache/darkraise/dforge/4.16.0/skills/ui-ux/references/walkthrough.md`) at touch depth with slug `m2-history-narrow`; Step 4's Playwright run is the walk, and its screenshots stay under `.dforge/ui-ux/m2-web-hosts/` (the spec file's `EVIDENCE` directory), which the report says. Open each `UI-4-*-transactions.png` and `UX-2-*-history.png` and check by eye that at 375 and 768 every block shows its badge, versions, time and kind, and the long error wraps inside the card; and that 1440 still shows the table. Write `docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.md` with `**Contract:** touch` and this `## Delta`, one result row per ID:

   ```markdown
   ## Delta

   **UI/UX depth:** touch — the host Overview update history stacks below the table's 40rem minimum

   These IDs are local to this report, not the spec's; the cited test and screenshot names carry the spec's IDs (UX-2 the history, UI-4 the badge variants).

   - **UI-1** — At 375x812 and 768x1024 the update history is a list, one block per transaction, newest first; the table is hidden.
   - **UI-2** — Each block shows the state badge and "<from> → <to>", then "<time> · <kind>" in small muted text.
   - **UI-3** — A transaction with an error shows "Error: <message>" on its own line; the long error with an unbroken URL wraps inside the card.
   - **UI-4** — Badge variants in the list match the table's: `pending` to `rolling-back` `secondary`, `rolled-back` and `failed` `destructive`, `committed` `default`.
   - **UI-5** — The 40-character version wraps inside its block; nothing scrolls sideways.
   - **UI-6** — At 1440x900 the table shows unchanged and the list is hidden.
   - **OP-1** — Proven at 375x812, 768x1024 and 1440x900, light and dark, with no axe violation on the host page in either layout (the `UI-4: transactions` tests' axe scan and the OP-3 test).
   ```

   The Delta's IDs are local to the report; each Delta row names the screenshot or test it cites: UI-1 to UI-5 cite `UI-4-<viewport>-<theme>-transactions.png`, `UX-2-<viewport>-<theme>-history.png` and the `UI-4: transactions` and `UX-2: history` tests at 375 and 768, plus the unit test `UX-2: the narrow update history lists each transaction with its state, versions, time, kind and error`; UI-6 cites the 1440 screenshots and tests; OP-1 cites the axe scans. Stamp the report, have it judged by `dforge:judge-sonnet-high`, copy the judge's report beside it as `2026-11-12-m2-history-narrow.judge.md`, and add the Judge line.
2. Run:

```bash
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.16.0/scripts/ui-proof check --base "$(git merge-base HEAD main)" && make lint test build verify-build
```

Expected: `ui-proof check` exits 0; lint, tests and build pass.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/host-overview.tsx web/src/pages/host-overview.test.tsx web/e2e/m2-web-hosts-states.pw.ts web/e2e/m2-web-hosts.pw.ts
git commit -m "feat(web): stack the update history when narrow"
git add web/src/boards/boards.tsx
git commit -m "refactor(web): share the state badge mapping"
git add docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.md docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.judge.md
git commit -m "docs(ui-ux): prove the narrow update history"
```
