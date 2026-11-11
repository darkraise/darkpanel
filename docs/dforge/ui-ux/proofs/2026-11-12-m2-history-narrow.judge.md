## Verdict
verdict: ship b9c63bb9417c24ff
scores: coverage 17 / fidelity 17 / states 16 / deviations 16

## What I opened

- Criteria and the report's Delta (UI-1 to UI-6, OP-1).
- Screenshots: UI-4 transactions at 375x812 light and dark, 768x1024 light and dark, 1440x900 light and dark (all six); UX-2 history at 375x812 dark, 768x1024 light and 1440x900 light.
- Tests read: `m2-web-hosts-states.pw.ts` (the `UX-2: history` and `UI-4: transactions` cases and `OP-3`), `m2-web-hosts.pw.ts` (worst-case pages), `host-overview.test.tsx`, and `host-overview.tsx` (list and table markup).
- I did not run any test. Pass counts (168 of 168) and axe results are the report's claim. The screenshots are written after the in-test assertions, and all six UI-4 files exist, which is consistent with those cases having passed.

## Per item

| ID | Finding |
|---|---|
| UI-1 | Held. 375 light and dark and 768 light and dark show a list of eight blocks, newest (3:00 PM `pending`) first, table absent. The states test asserts list visible, table hidden and a count of 1 for each element. The component hides the table below the 40rem container query. |
| UI-2 | Held. Each block shows badge and "1.0.0 → 1.1.x" on line 1 and "Nov 11, 2026, 3:00 PM · update" in small muted text on line 2. The unit test asserts both strings. |
| UI-3 | Held. The 220-character error with the unbroken URL breaks inside the URL and stays inside the card at 375 and 768, in light and dark. Each "Error:" line's `scrollWidth <= clientWidth` is asserted at both narrow viewports. The committed item has no Error line (unit test and screenshot). |
| UI-4 | Held on screenshots. Five in-progress states are grey, `rolled-back` and `failed` are red, `committed` is blue, in the list and in the 1440 table, in both themes. The unit test asserts `data-variant` for only three states (pending, rolled-back, committed). `downloading`, `staged`, `swapped`, `rolling-back` and `failed` are proven by colour in the screenshots, not by an assertion. |
| UI-5 | Held. At 375 the 40-character version wraps to a second line inside its block. At 768 it fits on one line. The list's `scrollWidth` check and the page-level `noPageScroll` check cover both. |
| UI-6 | Held literally. At 1440 the table is visible and the list hidden (asserted); the table markup is unchanged. See the observation below. |
| OP-1 | Held as far as reading shows. All six viewport and theme pairs have a `UI-4: transactions` case with an axe scan, and `OP-3` scans the host page. I did not re-run them. |

## Observation, not disputed

In `UI-4-1440x900-light-transactions.png` and the dark one, the table squeezes its State and Kind columns under the 220-character unbroken URL. Badges and "update" break mid-word ("pendin/g", "upda/te"). The report names this under UI-6 and calls it the unchanged table meeting new worst-case data, logged as a deferred follow-up. The table is unchanged, so UI-6 as written holds. It is a visible defect with no user answer, profile decision or accessibility need behind it, which is why Deviations is 16 and not higher. Someone should confirm the follow-up exists. The default 1440 history (`UX-2-1440x900-light-history.png`) is clean.

## Scores

- coverage 17: every item has a row with opened screenshots and a read test. UI-4's variant assertion covers three of eight states.
- fidelity 17: components, hierarchy, copy and badge colours match the Delta in both themes. The 1440 squeeze is the only blemish.
- states 16: worst-case data at three viewports and two themes is shown. Loading, empty, error and offline history states have screenshots from the existing suite. No hover or focus state is part of this delta. The keyboard walk is not new here.
- deviations 16: no row is marked deviation. The one difference I found (the 1440 squeeze) is disclosed but cites no forcing decision.

## Disputed items

None.

Verdict: ship b9c63bb9417c24ff
