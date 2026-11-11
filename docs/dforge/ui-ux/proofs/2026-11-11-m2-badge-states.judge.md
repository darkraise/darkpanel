## Verdict
verdict: ship 818b682aa4967397
scores: coverage 17 / fidelity 18 / states 17 / deviations 18

## What I opened

- All six `UI-4-<viewport>-<theme>-transactions.png` screenshots (375x812, 768x1024, 1440x900, light and dark).
- `UI-3-1440x900-light.png` and `UI-3-1440x900-dark-failed.png`, cited by UI-5.
- `web/src/lib/badges.ts`, `web/src/lib/badges.test.ts`, and `web/e2e/m2-web-hosts-states.pw.ts` (the `UI-4: transactions` row, lines 53-76 and 105-119).
- The call sites in `host-overview.tsx` (line 98) and `rollouts.tsx` (lines 97 and 130); both use `stateVariant` from `@/lib/badges`.

The report cites no state board, and none exists for this delta.

## Coverage {#coverage}

Score 17. Each Delta item has a row, and the evidence I opened shows it.

- UI-1: pending, downloading, staged, swapped and rolling-back are filled grey pills in all three light and dark screenshots I checked (1440 light, 1440 dark, 375 light, plus 768 both). They differ from the unfilled `skipped` outline pill in the dark rollouts screenshot. `badges.ts` and `badges.test.ts` map all five to `secondary`.
- UI-2: `rolled-back` is a red pill. At 1440x900 the Error column shows "the health check failed", in both themes. At 768x1024 the Error column is past the scroll edge, and at 375x812 it is truncated to "the health ch", exactly as the Delta states.
- UI-3: `committed` is a blue pill and `failed` a red pill in the 1440 light and 768 dark screenshots.
- UI-4: every badge shows its state as text at every viewport. The test asserts `getByText(state, { exact: true })` is visible for all eight states. At 768 the State column sits at the table's scroll edge, and "downloading" and "rolled-back" are close to the clip. They are still readable.
- UI-5: `running`, `committed`, `updating` and `waiting` are visible in the Rollouts 1440 light screenshot. `failed` (destructive) and `skipped` (outline) are visible in the 1440 dark screenshot. `succeeded` and `cancelled` are not in any screenshot, so the evidence for them is the `badges.test.ts` mapping test plus `rollouts.tsx` calling that function. The report says this itself. This is the weakest row, but it is backed by a test I read.
- OP-1: the six screenshots exist at every viewport and theme. In the spec, axe runs before the screenshot in the same test, so a fresh screenshot implies axe passed. The screenshots show the new mapping, so they came from a run after the change. I could not open a test run log, so the "168 tests pass" figure is unverified by me.

## Fidelity {#fidelity}

Score 18. The mapping in `badges.ts` matches the amended UI-4 rule for all 17 states in the rule. The rendered colours match the variants. Badges use `size="sm"` with the state as text, as in the earlier rows. The table layout is unchanged. No element is dropped and no copy changed.

## States {#states}

Score 17. This is a touch delta on a badge mapping. The states that matter are the eight transaction states, all of which appear in one worst-case fixture at all six viewport and theme combinations. Loading, error and empty history states were covered by the earlier m2-web-hosts proof and are untouched here. A keyboard walk does not apply to a static badge.

The Playwright test does not assert the variant, because its assertions pass without the `badges.ts` change. The variant is therefore proven by the screenshots (colour, by eye) plus the unit test. Together that is adequate.

## Deviations {#deviations}

Score 18. The report marks no row as a deviation, and I found no uncited difference from the contract. The Error column being unreachable at 768 and truncated at 375 is stated in the Delta and is pre-existing layout, not a deviation introduced here.

## Disputed items

None.

## Adjacent findings (not disputes)

- `web/src/boards/boards.tsx` lines 64-69 hold a second `stateVariant` copy with the old mapping (no `pending`, `downloading`, `staged`, `swapped`, `rolling-back` or `rolled-back`). The board's history table (line 288) uses it. Its current fixtures only use `committed` and `failed`, so nothing renders differently today. A future board row in an in-progress or `rolled-back` state would show `outline` instead of the amended variant. Importing the shared function would remove the drift.
- `change-dialog.tsx`, `pages/change.tsx` and `modules/tailscale/status-panel.tsx` each have their own `stateVariant` for different state vocabularies. They are unrelated to this rule.
- UI-5 would be stronger with a screenshot that shows a `succeeded` badge and a `cancelled` badge. The fixtures already contain both (`web/e2e/fixtures.ts` lines 151 and 171).

Verdict: ship 818b682aa4967397
