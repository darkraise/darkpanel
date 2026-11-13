## Verdict
verdict: ship 8aa66e72f4d98806
scores: coverage 17 / fidelity 17 / states 16 / deviations 16

## What I opened

- The criteria, the report (Delta and Results), and the test file `web/e2e/m3g-held-data.pw.ts` in full.
- The two components under proof, `web/src/modules/npm/banner.tsx` (read in full) and `web/src/modules/cloudflare/connectors-page.tsx` (searched for the query-state branches).
- All 16 screenshots the report cites: UI-1 (4), UI-2 (3), UX-1 (3), UI-3 (3), UI-4 (3). Another 20 uncited screenshots exist in the directory; I did not open them.
- I could not run the specs, so "24 of 24 pass twice" and "fails before the fix" are the report's claims, not something I observed. I scored them from the test code and the screenshots.

## Per-item findings

- UI-1 (connectors refetch fails): confirmed at 1440x900 light and dark, 768x1024 dark and 375x812 light.
  - The "Connectors on web-1" card shows the 230-character error text in a destructive alert, with the held table below it (header, 2 rows, the 64-character tunnel name).
  - At 375 the screenshot is cut off after the alert, as the report says. The held table and the deploy form's omission of `darkpanel-c1` are shown only by the test: it asserts 3 rows, the long tunnel cell, alert above the table, `darkpanel-c1` absent from the deploy radios, and the alert gone after the next good read.
  - The component matches: `connectors.isError && <ErrorAlert>` sits above the table, and `taken` is built from `connectors.data`.
- UI-2 (host-list refetch fails): confirmed at 1440 light, 768 light and 375 dark. The destructive alert sits above the Host card, and the Host radios and "Connectors on web-1" remain. The test asserts the alert is above the radiogroup and clears on the next read.
- UX-1 (dialog stays open): the three screenshots show the "Stop the connector of darkpanel-c1" dialog open over the page, with the host-list alert visible behind it and focus on the close button.
  - The no-remount claim rests on the `data-m3g-marker` attribute, which the test sets before the failure and checks afterwards.
  - Opening by keyboard (focus, then Enter), Escape closing the dialog, and focus returning to the same Stop button are covered by the test only; no screenshot follows Escape. The test code does assert each step.
- UI-3 (NPM fleet refetch fails): confirmed at 375 dark, 768 light and 1440 dark. Three warnings are stacked: "Admin interface exposed" with its link text, "…exposed on: db-1.", and the generic "Could not check whether the NPM admin UI is exposed." The test asserts the link href and that the generic warning clears. The banner code matches (the `fleet.data === undefined` guard, then `fleet.isError` adds the generic warning).
- UI-4 (first reads fail):
  - 768 light and 375 dark connectors: the alert is shown, no table, and a generic could-not-check banner on top.
  - 1440 dark hosts: the alert alone, no Host card.
  - The tests also assert `toHaveCount(0)` for the table and the exposed warning.
- OP-1: test-only evidence, which the criteria allow. The tests run axe (WCAG 2.1 A and AA), check `scrollWidth <= clientWidth`, and assert console errors are empty (apart from lines starting "Failed to load resource") and no unhandled API request. The UI-1, UI-2/UX-1 and UI-3 tests run them at all six pairs. UI-4 only checks sideways scroll, which the Delta does not require of it. No screenshot is needed for these checks.

## Criteria

- coverage 17: every Delta item has a row, and the cited screenshots show the claimed state. The shortfalls are that the held table at 375 and the deploy-form filter are shown only by assertion, and that focus return after Escape has no screenshot.
- fidelity 17: the components are destructive and warning `Alert`s in the design system, with theme-appropriate colours in both modes and the error message shown verbatim. Layout holds at all three widths.
- states 16: refetch-failed, first-read-failed, dialog-open and clearing on the next good read are covered at six viewport and theme pairs by tests, with screenshots at a spread of pairs. The keyboard walk is in the test. The Delta lists no loading, empty, hover or pending states.
- deviations 16: the report marks no row as a deviation. It lists three pre-existing layout defects under adjacent findings and does not mark them pass. I accepted this because the Delta is about held data and the defects are in markup this change did not touch. They are not cited to any user answer or accessibility need, and a strict reading of this criterion could dock for that.

## Disputed items

None.

## Notes outside the contract

- The adjacent defects the report lists are visible in the screenshots. The State badge breaks as "ru/nni/ng" at 375 and 768, and the 63-character host name overflows the radio label and the banner link at 375.
- In the NPM banner, "Open Nginx Proxy Manager on <host>" has no underline or colour at rest (`underline-offset-4 hover:underline`). It reads as plain text in every UI-3 screenshot, so it is hard to tell it is a link. This is existing styling and not in the Delta.
- The pre-fix failure comparison and the repeat runs could not be checked from the files.

Verdict: ship 8aa66e72f4d98806
