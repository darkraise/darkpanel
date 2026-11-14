## Verdict
verdict: ship
scores: coverage 18 / fidelity 18 / states 17 / deviations 18

Disputed items: 0.

## How I judged

I read the criteria, the contract (the report's Delta, touch depth, no state boards), the report, `web/e2e/w12c-deploy-form.pw.ts`, `web/src/modules/npm/instance-panel.test.tsx`, `instance-panel.tsx` and the `pre` in `change-dialog.tsx`. I opened all 13 distinct screenshots the report cites. The directory holds 60 screenshots, which matches 10 per viewport and theme pair across 6 pairs. I did not run any test: I have no shell, so test results rest on reading the test files, not on a run.

## Coverage (18)

Every contract item has a Results row, and the evidence for each opened and showed that item.

- UI-1: `UI-1-1440x900-dark.png` and `UI-1-375x812-light.png` show the exact "integration is on" sentence with Plan dimmed and the email empty. The spec's `planned[0]` asserts the exact string and `toBeDisabled()` before typing. Plan can only take focus after typing because a disabled button cannot be focused, and the spec asserts `toBeFocused()` after Tab.
- UI-2: `UI-2-375x812-light.png` and `UI-2-1440x900-dark.png` show the "deploy allows ports 80 and 443 ... as its last step" sentence. It wraps to four lines at 375 px as the report's note says, and the copy matches `instance-panel.tsx`.
- UI-3: `UI-3-768x1024-dark.png` and `UI-3-375x812-light.png` show today's "open ports 80 and 443 ... yourself" sentence. The spec drives it with `status.supported=false`.
- UI-4: `UI-4-375x812-dark-status-loading.png` and `UI-4-1440x900-light-docker-loading.png` show a spinner under a filled-in 54-character email, no hint, and a dimmed Plan. The spec holds each read in turn, asserts the label "Loading the firewall state", no hint (count 0), Plan disabled, then after release the hint, no spinner and Plan enabled. The component renders the spinner on `docker.isPending || status.isPending` and the hint only when both reads succeeded.
- UI-5: `UI-5-768x1024-light-status-failed.png` and `UI-5-375x812-dark-docker-failed.png` show the read's own message in an alert, no hint, and a dimmed Plan with the email typed. The spec asserts the exact alert text, hint count 0 and Plan disabled for both reads. The Vitest file covers both failures.
- UX-1: the three spec tests type the email, press Tab and assert Plan is focused, press Enter, and assert the recorded plan list equals exactly one request to `/api/v1/actions/npm.instance.deploy-public/v2/plan` with `{ hostId, acmeEmail, integration, firewall, ipv6 }`. The Vitest `test.each` covers five input combinations, including `ipv6: false`. `instance-panel.tsx` builds that input from `docker`, `status.supported` and `status.data.configured.ipv6`. The dialog subtitle "npm.instance.deploy-public/v2 on the controller" is visible in the screenshots.
- UI-6: `change-dialog.tsx` line 373 has `tabIndex={0}` on the "Planned changes" `pre` with `overflow-x-auto`. `UX-1-UI-2-375x812-light.png` and `-dark.png` show the first diff line clipped at the dialog edge and the close button holding the focus ring. The dialog axe run in the spec covers `scrollable-region-focusable`.
- OP-1: every spec test runs axe with the four WCAG tags, checks `scrollWidth <= clientWidth`, and asserts no console error and no unhandled API request. This holds at all six pairs by construction of the loops.

The one weakness is UI-6: no test presses Tab to the `pre` and asserts focus. The proof is the `tabIndex` in the source plus the axe rule. That is enough for the contract's wording, but it is the least direct row.

## Fidelity (18)

- The copy in every screenshot matches the contract's strings character for character, and the strings in the component, the spec and the Vitest file agree.
- The build uses the existing darkraise-ui parts: `Input`, `Label`, `Button`, `Spinner`, `Alert` with the destructive variant, and the shared change dialog. The hint is muted small text. The UI-5 alert is red-tinted in light and dark with readable text. No colours or type sizes outside the design system appear.
- Hierarchy and rhythm are consistent across the form: label, input, spinner / alert / hint slot, Plan button. The 54-character email is clipped inside the input at 375 px without changing the layout.
- The 375 px dialog shows the wide diff line clipped inside the block rather than widening the dialog, and the Apply button stays full width.

## States (17)

The contract's data states are covered: hint on, hint off with firewall managed, hint off without it, either read pending, either read failed, and worst-case content (a 54-character email, a long hint, and a diff line wider than the dialog at 375 px). Control states are covered: Plan disabled (screenshots), Plan focused by keyboard (spec assertion), close button focused on dialog open (screenshots), and the input focus ring (visible in the UI-4 and UI-5 screenshots). Each runs at three viewports and two themes by the spec's loops.

Gaps, none enough to dispute:
- The keyboard walk starts with a programmatic `.focus()` on the email input, not a Tab from the page. The contract's walk starts at typing the email, so this fits.
- No screenshot shows Plan itself holding the focus ring, and none shows the `pre` focused. Both rest on assertions or source.
- A malformed but non-empty email is not covered; the contract does not ask for it.

## Deviations (18)

The report has no deviation rows, and I found no difference from the contract in the screenshots. One thing is visible in the 375 px screenshots and disclosed in the report's prose: the host page tabs and NPM inner tabs run past the right edge ("Nginx Pro...", "Certificate..."). The report calls this page chrome older than this change, outside the delta, and deferred. It is not a contract item and is not marked pass, so I did not count it as a difference. I could not verify the claim that it is older than this change or that a ledger entry exists, because neither is among the evidence.

## Disputed items

None.

Verdict: ship bc727feab3b136f4
