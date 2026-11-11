# UI proof: M2 narrow update history

**Contract:** touch
**Fingerprint:** b9c63bb9417c24ff
**Judge:** ship b9c63bb9417c24ff — docs/dforge/ui-ux/proofs/2026-11-12-m2-history-narrow.judge.md
**Platform:** web
**Viewports:** 375x812, 768x1024, 1440x900
**Themes:** light, dark
**Evidence:** local

Screenshots live under `.dforge/ui-ux/m2-web-hosts/`, the `EVIDENCE` directory of `web/e2e/m2-web-hosts-states.pw.ts` and `web/e2e/m2-web-hosts.pw.ts`. The `UI-4: transactions` rows force the host page's `core.update.status` read to answer eight transactions, one per update state, with worst-case data: the newest carries a 40-character version (`1.1.0-beta.1+aaaa…`) and the `rolled-back` one a 220-character error holding an unbroken URL. At 375 and 768 the `UX-2: history` and `UI-4: transactions` tests assert that the list is visible, the table is hidden, the list's `scrollWidth` does not exceed its `clientWidth`, and every "Error:" line fits its block; at 1440 they assert the reverse. All 168 tests of the two files pass in Chromium (headless shell build 1243 via `E2E_CHROMIUM`, the build Playwright pins not being installed on this host); an earlier run under host load above 6 had three element-not-found timeouts, and the rerun at normal load passed all 168. Every cited screenshot was opened and checked by eye. The report's IDs are local to this delta.

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

## Results

| ID | Verdict | Evidence | Note |
|---|---|---|---|
| UI-1 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-375x812-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-768x1024-dark-transactions.png, .dforge/ui-ux/m2-web-hosts/UX-2-375x812-dark-history.png, .dforge/ui-ux/m2-web-hosts/UX-2-768x1024-light-history.png; m2-web-hosts-states.pw.ts "UX-2: history" and "UI-4: transactions" at 375x812 and 768x1024 (list visible, table hidden); m2-web-hosts.pw.ts "UX-1, UX-2, UX-8, UX-12: worst-case pages" (three list items below 1440) | Eight blocks, newest (`pending`, 3:00 PM) first, down to `failed` (8:00 AM). |
| UI-2 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-375x812-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-768x1024-dark-transactions.png; host-overview.test.tsx "UX-2: the narrow update history lists each transaction with its state, versions, time, kind and error" | Line 1 badge and "1.0.0 → 1.1.x"; line 2 "Nov 11, 2026, 3:00 PM · update" in the smaller muted grey. |
| UI-3 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-375x812-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-768x1024-dark-transactions.png, .dforge/ui-ux/m2-web-hosts/UX-2-375x812-dark-history.png; m2-web-hosts-states.pw.ts "UI-4: transactions" at 375x812 and 768x1024 (each "Error:" line's scrollWidth within its clientWidth); host-overview.test.tsx "UX-2: the narrow update history lists each transaction with its state, versions, time, kind and error" (no Error line on the committed item) | The 220-character error breaks inside the URL and wraps within the card at 375; `committed` and the in-progress states have no Error line. |
| UI-4 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-375x812-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-768x1024-dark-transactions.png; host-overview.test.tsx "UI-4: each history badge carries the variant of its transaction state" (list badges' `data-variant`) | Grey `secondary` for the five in-progress states, red `destructive` for `rolled-back` and `failed`, blue `default` for `committed`, each with its state as text. |
| UI-5 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-375x812-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-768x1024-light-transactions.png; m2-web-hosts-states.pw.ts "UI-4: transactions" at 375x812 and 768x1024 (list scrollWidth within clientWidth); m2-web-hosts.pw.ts "UX-1, UX-2, UX-8, UX-12: worst-case pages" (no page-level horizontal scroll) | At 375 the long version wraps onto a second line inside its block; at 768 it fits on one line. |
| UI-6 | pass | .dforge/ui-ux/m2-web-hosts/UI-4-1440x900-light-transactions.png, .dforge/ui-ux/m2-web-hosts/UI-4-1440x900-dark-transactions.png, .dforge/ui-ux/m2-web-hosts/UX-2-1440x900-light-history.png; m2-web-hosts-states.pw.ts "UX-2: history" and "UI-4: transactions" at 1440x900 (table visible, list hidden); m2-web-hosts.pw.ts "UX-1, UX-2, UX-8, UX-12: worst-case pages 1440x900-light" (four table rows) | The table's markup and classes are unchanged. With the new 220-character error the 1440 table squeezes its State and Kind columns so badges and "update" break mid-word; that is the unchanged table meeting the new worst-case data, logged as a deferred follow-up. |
| OP-1 | pass | m2-web-hosts-states.pw.ts "UI-4: transactions" at all six viewport and theme pairs (axe scan with the list at 375 and 768, the table at 1440), "OP-3: axe and console on every page and dialog" at all six | 168 of 168 pass on the rerun at normal load. |

## Automated checks

- axe: 0 serious, 0 critical (the `UI-4: transactions` tests in both layouts and `OP-3` on every page and dialog)
- console errors: 0 (`OP-3`)
- horizontal overflow at 375 and 768: none (the list's own `scrollWidth` check and `m2-web-hosts.pw.ts`'s page-level check)
