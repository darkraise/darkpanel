# UI proof: NPM deploy form on deploy-public v2

**Contract:** touch
**Fingerprint:** bc727feab3b136f4
**Judge:** ship bc727feab3b136f4 — docs/dforge/ui-ux/proofs/2026-11-14-w12c-deploy-form.judge.md
**Platform:** web
**Viewports:** 375x812, 768x1024, 1440x900
**Themes:** light, dark
**Evidence:** local

Screenshots live under `.dforge/ui-ux/w12c-deploy-form/`, the `EVIDENCE` directory of `web/e2e/w12c-deploy-form.pw.ts`. The spec opens `/hosts/<HOST_ID>/npm` for a host without NPM. It answers every API request from `web/e2e/fixtures.ts` and from its own routes for `npm.instance.status` (not deployed), `firewall.docker`, `firewall.status` and the `npm.instance.deploy-public` plan. Each read can answer in one of three ways: with a body chosen per state, held without an answer until the test releases it, or with a 503. The worst-case data is a Let's Encrypt email of 54 characters (`letsencrypt-operations-team@infrastructure.example.com`) and a plan diff line wider than the dialog at 375 px. All 42 tests (7 tests at 6 viewport and theme pairs) pass, as do the 88 of `web/e2e/a11y.pw.ts`, in Chromium (headless shell build 1243 via `E2E_CHROMIUM`). The Vitest suite `web/src/modules/npm/instance-panel.test.tsx` covers the same states without a browser, including the plan input when UFW's IPv6 setting is off. I opened and checked by eye every screenshot cited below. The report's IDs are local to this delta.

At 375 px the host's page tabs and NPM's inner tabs run past the right edge of the screen ("Nginx Pro…", "Certificate…"). This is page chrome older than this change, and the page itself never scrolls sideways. It is outside this delta and recorded as a deferred minor in the run's ledger.

## Delta

**UI/UX depth:** touch. The NPM deploy form reads `firewall.status` beside `firewall.docker`, says how ports 80 and 443 will be opened, and plans `npm.instance.deploy-public` v2.

- **UI-1**: With the Docker firewall integration on, the form shows "The Docker firewall integration is on: ports 80 and 443 are opened for the NPM container." Plan stays off until an email is typed.
- **UI-2**: With the integration off and `firewall.status` reporting `supported` true, the form shows "The Docker firewall integration is off: the deploy allows ports 80 and 443 in the firewall as its last step. Then check them from outside and confirm that the instance is public."
- **UI-3**: With the integration off and `firewall.status` reporting `supported` false, the form shows today's text: "The Docker firewall integration is off: open ports 80 and 443 to the internet yourself, then confirm that the instance is public."
- **UI-4**: While either read (`firewall.docker` or `firewall.status`) is pending, the form shows a spinner labelled "Loading the firewall state". It shows no hint, and Plan stays off even with an email typed. Once the read answers, the spinner goes, the hint shows and Plan turns on.
- **UI-5**: When either read fails, the form shows that read's error message in an alert and no hint, and Plan stays off even with an email typed.
- **UX-1**: Using only the keyboard (type the email, Tab to Plan, Enter), Plan opens the "Deploy Nginx Proxy Manager" dialog. The form sends exactly one plan, to `/api/v1/actions/npm.instance.deploy-public/v2/plan`, with input `{ hostId, acmeEmail, integration, firewall, ipv6 }` as read: `firewall` is `firewall.status`'s `supported` and `ipv6` its `configured.ipv6`.
- **UI-6**: The plan dialog's "Planned changes" block, which scrolls sideways when a diff line is wider than the dialog (at 375 px here), takes keyboard focus (`tabIndex` 0 on `web/src/components/change-dialog.tsx`'s `pre`), so a keyboard user can scroll it. Opening the dialog still focuses its close button.
- **OP-1**: Proven at 375x812, 768x1024 and 1440x900, light and dark: no axe violation (WCAG 2.1 A and AA) on the form in any state or on the dialog, no console error other than the browser's own "Failed to load resource" line for a read that failed on purpose, no page-level horizontal scroll, and no API request without a fixture.

## Results

| ID | Verdict | Evidence | Note |
|---|---|---|---|
| UI-1 | pass | .dforge/ui-ux/w12c-deploy-form/UI-1-1440x900-dark.png, .dforge/ui-ux/w12c-deploy-form/UI-1-375x812-light.png; w12c-deploy-form.pw.ts "UI-1, UX-1: integration on" at all six pairs (exact hint visible; Plan disabled before the email) | |
| UI-2 | pass | .dforge/ui-ux/w12c-deploy-form/UI-2-375x812-light.png, .dforge/ui-ux/w12c-deploy-form/UI-2-1440x900-dark.png; w12c-deploy-form.pw.ts "UI-2, UX-1: integration off, firewall managed" at all six pairs | At 375 px the text wraps over four lines inside the form. |
| UI-3 | pass | .dforge/ui-ux/w12c-deploy-form/UI-3-768x1024-dark.png, .dforge/ui-ux/w12c-deploy-form/UI-3-375x812-light.png; w12c-deploy-form.pw.ts "UI-3, UX-1: integration off, firewall not managed" at all six pairs | |
| UI-4 | pass | .dforge/ui-ux/w12c-deploy-form/UI-4-375x812-dark-status-loading.png, .dforge/ui-ux/w12c-deploy-form/UI-4-1440x900-light-docker-loading.png; w12c-deploy-form.pw.ts "UI-4: docker read loading" and "UI-4: status read loading" at all six pairs (spinner by its label; no hint; Plan disabled with the email typed; after release, hint visible, spinner gone, Plan enabled) | |
| UI-5 | pass | .dforge/ui-ux/w12c-deploy-form/UI-5-768x1024-light-status-failed.png, .dforge/ui-ux/w12c-deploy-form/UI-5-375x812-dark-docker-failed.png; w12c-deploy-form.pw.ts "UI-5: docker read failed" and "UI-5: status read failed" at all six pairs (the alert holds exactly the read's detail; no hint; Plan disabled with the email typed); instance-panel.test.tsx "keeps Plan off and shows the reason while firewall.status cannot be read" | |
| UX-1 | pass | .dforge/ui-ux/w12c-deploy-form/UX-1-UI-1-1440x900-light.png, .dforge/ui-ux/w12c-deploy-form/UX-1-UI-2-375x812-dark.png; w12c-deploy-form.pw.ts the three "UX-1" tests at all six pairs (keyboard only; Plan focused after Tab; the recorded plan requests equal one v2 request with the expected input); instance-panel.test.tsx "deploys with the integration flag read from firewall.docker when it is %s" (five cases, including ipv6 false) | The dialog's subtitle reads "npm.instance.deploy-public/v2 on the controller". |
| UI-6 | pass | .dforge/ui-ux/w12c-deploy-form/UX-1-UI-2-375x812-light.png, .dforge/ui-ux/w12c-deploy-form/UX-1-UI-2-375x812-dark.png; w12c-deploy-form.pw.ts the three "UX-1" tests at 375x812 light and dark, whose dialog axe run includes `scrollable-region-focusable` and passes with the diff line clipped | Before the fix, those six runs failed on exactly that rule. The screenshots show the close button holding focus when the dialog opens. a11y.pw.ts, the other change dialogs' browser pass, still passes (88 of 88), and so does the full Vitest suite (1324 tests). |
| OP-1 | pass | w12c-deploy-form.pw.ts: all seven tests at all six pairs run axe (WCAG 2.1 A and AA tags, no rule skipped) once animations finish, check `scrollWidth <= clientWidth` on the document, and assert no console error and no unhandled API request | 42 of 42 pass. |

## Automated checks

- axe: 0 violations (WCAG 2.1 A and AA) on the form in all five states at all six pairs, and on the plan dialog, with no rule skipped
- console errors: 0 (the browser's own "Failed to load resource" lines for the UI-5 failed reads are excluded)
- horizontal overflow of the page: none at any viewport
