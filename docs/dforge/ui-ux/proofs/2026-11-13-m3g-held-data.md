# UI proof: held data when a refetch fails

**Contract:** touch
**Fingerprint:** 8aa66e72f4d98806
**Judge:** ship 8aa66e72f4d98806 — docs/dforge/ui-ux/proofs/2026-11-13-m3g-held-data.judge.md
**Platform:** web
**Viewports:** 375x812, 768x1024, 1440x900
**Themes:** light, dark
**Evidence:** local

Screenshots live under `.dforge/ui-ux/m3g-held-data/`, the `EVIDENCE` directory of `web/e2e/m3g-held-data.pw.ts`. The spec answers every API request from `web/e2e/fixtures.ts` and its own routes, with worst-case data: two Cloudflare hosts, one named with 63 characters (`web-` and 59 `x`); a 64-character tunnel name; a container name of 62 characters; a 230-character error message holding a file path; an exposed NPM admin hostname of 61 characters. A refetch is forced the way the running panel forces one: the routed `/api/v1/events` stream sends a `reset` event, which makes `connectEvents` (`web/src/api/events.ts`) invalidate every open query. The read under test then answers 500, which the app retries twice before the query turns to error, and a second `reset` with the read answering again shows the alert clear. All 24 tests (4 tests at 6 viewport and theme pairs) pass in Chromium (headless shell build 1243 via `E2E_CHROMIUM`) in two consecutive runs at host load 3 to 4. With the two components restored to their state before `49ee0b3` (`git show 49ee0b3^:<file>`), the 1440x900-light run fails the UI-1, UI-2/UX-1 and UI-3 tests and passes UI-4, so the held-data assertions fail without the fix; the components were restored afterwards and the 1440x900-light screenshots retaken. No make target or CI workflow runs the Playwright specs (core register row 122), so rerun `E2E_CHROMIUM=<headless shell> pnpm -C web exec playwright test e2e/m3g-held-data.pw.ts` before merging a change to these two components. Every cited screenshot was opened and checked by eye. The report's IDs are local to this delta.

## Delta

**UI/UX depth:** touch — the Cloudflare connectors page and the NPM admin exposure banner keep held data when a refetch fails

- **UI-1** — When a refetch of a host's connectors fails, the "Connectors on <host>" card shows the error's own message in a destructive alert at the top of the card and keeps the held connectors table below it; the deploy form still leaves out the tunnels that have a connector. The alert clears on the next successful read.
- **UI-2** — When a refetch of the host list fails, the page shows the error's message in a destructive alert above the Host choice and keeps the Host choice and the host's cards. The alert clears on the next successful read.
- **UX-1** — A change dialog open over the page (opened by keyboard from a connector's Stop button) stays open and is not remounted while the host-list refetch fails; Escape then closes it and focus returns to the same Stop button.
- **UI-3** — When a refetch of the NPM fleet fails, the banner keeps the "Admin interface exposed" warning with its link and the per-host "could not check … on: <hosts>" warning, and adds the generic "Could not check whether the NPM admin UI is exposed." warning. It clears on the next successful read.
- **UI-4** — A first read that fails still shows only the error: the connectors card shows the alert and no table; a failed first host-list read shows the alert alone, with no Host choice; a failed first fleet read shows only the generic could-not-check warning.
- **OP-1** — Proven at 375x812, 768x1024 and 1440x900, light and dark: no axe violation in the refetch-failed states, no console error other than the browser's own "Failed to load resource" line for the forced 500s, no page-level horizontal scroll, and no API request without a fixture.

## Results

| ID | Verdict | Evidence | Note |
|---|---|---|---|
| UI-1 | pass | .dforge/ui-ux/m3g-held-data/UI-1-375x812-light-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-1-768x1024-dark-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-1-1440x900-dark-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-1-1440x900-light-refetch-failed.png; m3g-held-data.pw.ts "UI-1: connectors refetch fails" at all six pairs (alert text equals the message, alert above the table, header and two rows, the long tunnel cell visible, darkpanel-c1 absent from the deploy form, alert gone after the next good read with three rows) | At 375 the screenshot is scrolled to the alert, so the table sits below the fold; the test asserts it. |
| UI-2 | pass | .dforge/ui-ux/m3g-held-data/UI-2-375x812-dark-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-2-768x1024-light-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-2-1440x900-light-refetch-failed.png; m3g-held-data.pw.ts "UI-2, UX-1: hosts refetch fails under an open dialog" at all six pairs (alert above the Host radiogroup, both host radios and "Connectors on web-1" visible, alert gone after the next good read) | Full-page screenshots. |
| UX-1 | pass | .dforge/ui-ux/m3g-held-data/UX-1-375x812-light-dialog-kept.png, .dforge/ui-ux/m3g-held-data/UX-1-768x1024-dark-dialog-kept.png, .dforge/ui-ux/m3g-held-data/UX-1-1440x900-light-dialog-kept.png; m3g-held-data.pw.ts "UI-2, UX-1: hosts refetch fails under an open dialog" (the dialog keeps a `data-m3g-marker` attribute set before the failure, so it was not remounted; after Escape the Stop button is focused) | Behind the dialog the page shows the alert above the Host card. |
| UI-3 | pass | .dforge/ui-ux/m3g-held-data/UI-3-375x812-dark-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-3-768x1024-light-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UI-3-1440x900-dark-refetch-failed.png; m3g-held-data.pw.ts "UI-3: banner refetch fails" at all six pairs (exposed warning, its link's href, the "on: db-1" warning and the generic warning all shown; the generic warning gone after the next good read) | Three warnings stacked in that order on the hosts page. |
| UI-4 | pass | .dforge/ui-ux/m3g-held-data/UI-4-768x1024-light-connectors-first-read.png, .dforge/ui-ux/m3g-held-data/UI-4-375x812-dark-connectors-first-read.png, .dforge/ui-ux/m3g-held-data/UI-4-1440x900-dark-hosts-first-read.png; m3g-held-data.pw.ts "UI-4: first reads fail" at all six pairs | Unchanged behaviour; the test also passes against the components before the fix. |
| OP-1 | pass | m3g-held-data.pw.ts: the UI-1, UI-2/UX-1 and UI-3 tests at all six pairs run axe (WCAG 2.1 A and AA tags) in the refetch-failed state, check `scrollWidth <= clientWidth` on the document, and assert no console error and no unhandled API request | 24 of 24 pass, twice. |

## Automated checks

- axe: 0 violations (WCAG 2.1 A and AA) in every refetch-failed state at all six pairs
- console errors: 0, apart from the browser's "Failed to load resource" line for each forced 500
- horizontal overflow of the page: none at any viewport

## Adjacent findings (outside this delta)

The worst-case data shows three layout defects in markup that `49ee0b3` did not change; they hold before and after the fix and are not part of this delta's verdicts:

- The connectors table's State badge breaks its text across lines at 375 and 768 ("ru / nni / ng", "ex / it / ed"), and the 64-character tunnel and container names wrap one or two characters per line at 375: .dforge/ui-ux/m3g-held-data/UI-2-375x812-dark-refetch-failed.png, .dforge/ui-ux/m3g-held-data/UX-1-768x1024-dark-dialog-kept.png.
- A 63-character host name runs past the right edge of the Host card's radio label at 375: .dforge/ui-ux/m3g-held-data/UI-2-375x812-dark-refetch-failed.png.
- The same name runs past the right edge of the banner's "Open Nginx Proxy Manager on <host>" link at 375: .dforge/ui-ux/m3g-held-data/UI-3-375x812-dark-refetch-failed.png.

The page itself does not scroll sideways (the overflow is clipped), which is why OP-1's check passes.

On the Cloudflare pages the banner's per-host warning names `01JE00000000000000000000H2` rather than a host name: the Cloudflare tests serve a host list without that host, and the banner falls back to the id, as it does for any unreadable host the host list lacks.
