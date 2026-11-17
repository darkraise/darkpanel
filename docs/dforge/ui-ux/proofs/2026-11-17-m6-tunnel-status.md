# UI proof: tunnel status beside each connector

**Contract:** touch
**Fingerprint:** 3269e1eb2ab144e4
**Judge:** ship 3269e1eb2ab144e4 — docs/dforge/ui-ux/proofs/2026-11-17-m6-tunnel-status.judge.md
**Platform:** web
**Viewports:** 375x812, 768x1024, 1440x900
**Themes:** light, dark
**Evidence:** local

Screenshots live under `.dforge/ui-ux/m6-tunnel-status/`, the `EVIDENCE` directory of `web/e2e/m6-tunnel-status.pw.ts`. The spec answers every API request from `web/e2e/fixtures.ts` and its own routes, with worst-case data: one Cloudflare host with six connectors, one per tunnel status the API returns (`healthy`, `degraded`, `down`, `inactive`, `unknown`) plus one whose tunnel (`t-gone`) is not in the tunnel list; a 64-character tunnel name (`darkpanel-` and 54 `t`); a 62-character container name; container states `running`, `exited`, `restarting` and `missing`. A second test refuses the tunnel list with 409 `cloud.not_connected`. All 12 tests (2 tests at 6 viewport and theme pairs) pass in Chromium (headless shell build 1243 via `E2E_CHROMIUM`) in two consecutive runs. With `connectors-page.tsx` as it was before this change (`git show HEAD:...`), all 12 fail on the cell texts. The line-count check is validated by a control assertion at 375 px: the 64-character tunnel name, which wraps, counts more than one line. With the `whitespace-nowrap` class removed from the badges the badge assertions still pass, so the class was dropped as redundant; the header keeps it so "Tunnel status" does not wrap at 1440. Unit tests: `connectors-page.test.tsx` (16 tests) asserts the column header, each row's status, and `unknown` when the tunnel list is refused or pending; it failed before the change. No make target or CI workflow runs the Playwright specs, so rerun `E2E_CHROMIUM=<headless shell> pnpm -C web exec playwright test e2e/m6-tunnel-status.pw.ts` before merging a change to this page. Every cited screenshot was opened and checked by eye. The report's IDs are local to this delta.

## Delta

**UI/UX depth:** touch — the Cloudflare connectors page shows each connector beside its tunnel's Cloudflare-side status (M6 audit gap on R70)

- **UI-1** — The "Connectors on <host>" table has a "Tunnel status" column between Tunnel and Container. Each row shows its tunnel's status from `cloudflare.tunnel.list` (`healthy`, `degraded`, `down`, `inactive` or `unknown`) as a small badge, the primary badge for `healthy` and the secondary badge otherwise, as the Tunnels page does.
- **UI-2** — When the connector's tunnel is not in the tunnel list, or the tunnel list is refused or still loading, the cell shows `unknown`; the connectors still list by tunnel id, and a refused list's reason shows in the alert the page already has.
- **UI-3** — With worst-case data, every status badge and state badge stays on one line at 375, 768 and 1440, and the "Tunnel status" header stays on one line; at narrow widths the table scrolls inside its card, not the page.
- **UX-1** — The status is text in a column whose header is a table column header ("Tunnel status"), so it does not rely on colour and a screen reader reads it with its header.
- **OP-1** — Proven at 375x812, 768x1024 and 1440x900, light and dark: no axe violation (WCAG 2.1 A and AA), no console error other than the browser's own "Failed to load resource" line for the refused read, no page-level horizontal scroll, and no API request without a fixture.

## Results

| ID | Verdict | Evidence | Note |
|---|---|---|---|
| UI-1 | pass | .dforge/ui-ux/m6-tunnel-status/UI-1-375x812-light-table.png, .dforge/ui-ux/m6-tunnel-status/UI-1-768x1024-dark-table.png, .dforge/ui-ux/m6-tunnel-status/UI-1-1440x900-light-table.png, .dforge/ui-ux/m6-tunnel-status/UI-1-1440x900-dark-table.png; m6-tunnel-status.pw.ts "UI-1, UI-3, UX-1, OP-1: each connector beside its tunnel's status" at all six pairs (header texts in order, each row's tunnel and status); connectors-page.test.tsx "lists the connectors of the only host with the tunnel's name, its Cloudflare status, the container and the state" | The element screenshots at 375 and 768 show the part of the table inside its scroll container. |
| UI-2 | pass | .dforge/ui-ux/m6-tunnel-status/UI-2-375x812-dark-table.png, .dforge/ui-ux/m6-tunnel-status/UI-2-768x1024-light-table.png, .dforge/ui-ux/m6-tunnel-status/UI-2-1440x900-dark-table.png, and the `t-gone` row of every UI-1 screenshot; m6-tunnel-status.pw.ts "UI-2, OP-1: a tunnel list that cannot be read shows unknown" at all six pairs (alert with the reason, every row `unknown`, tunnels listed by id); connectors-page.test.tsx "a tunnel list that cannot be read …" and "a tunnel list that is still pending …" | Pending is proven in the unit test only. |
| UI-3 | pass | .dforge/ui-ux/m6-tunnel-status/UI-3-375x812-dark-page.png, .dforge/ui-ux/m6-tunnel-status/UI-3-375x812-light-page.png, .dforge/ui-ux/m6-tunnel-status/UI-3-768x1024-light-page.png, .dforge/ui-ux/m6-tunnel-status/UI-3-1440x900-dark-page.png; the UI-1 test counts the line boxes of every status and state badge's text (1 each) at all six pairs, with the control at 375 | Long tunnel and container names wrap in their own cells, as before. |
| UX-1 | pass | m6-tunnel-status.pw.ts reads the header with `getByRole("columnheader")` and the status as cell text; axe passes at all six pairs | |
| OP-1 | pass | both tests at all six pairs run axe (WCAG 2.1 A and AA tags), check `scrollWidth <= clientWidth` on the document, and assert no console error and no unhandled API request | 12 of 12 pass, twice. |

## Automated checks

- axe: 0 violations (WCAG 2.1 A and AA) in both states at all six pairs
- console errors: 0, apart from the browser's "Failed to load resource" line for the refused tunnel read
- horizontal overflow of the page: none at any viewport
- ui-lint on `connectors-page.tsx`: no findings
