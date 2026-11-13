## Verdict
verdict: ship 701a2622098b36c9
scores: coverage 17 / fidelity 17 / states 16 / deviations 17

## Basis

I read the criteria, the report's Delta and Results, the Playwright spec `web/e2e/m3g-held-data.pw.ts`, and the relevant lines of `connectors-page.tsx` and `banner.tsx`. I opened all 20 screenshots the report cites. I could not run the tests (no shell), so "30 of 30 pass" is the report's claim, not something I saw. My scores rest on what the spec asserts and what the screenshots show.

## Disputed items

None.

## Evidence by item

- UI-1: The six cited screenshots show a destructive alert at the top of the "Connectors on web-1" card with the 230-character message, and the held table below it with its header and two rows, including the 64-character tunnel. This holds at 375 light, 768 dark and 1440 dark and light. The deploy-form crops at 375 light and 1440 dark offer "Create a new tunnel" and darkpanel-c3 only. The spec asserts alert text equals the message, three rows, alert above table, c1 and the long tunnel absent from the form, and the alert gone after the next good read.
- UI-2: The 375 dark, 768 light and 1440 light screenshots show a destructive alert above the Host card, with the Host radios and the "Connectors on web-1" card still shown. The spec asserts the alert is above the radiogroup and clears on the next good read. After Escape, the first Stop button shows a focus ring in the 375 dark and 1440 light screenshots.
- UX-1: The three dialog screenshots show the "Stop the connector of darkpanel-c1" dialog open over the page, with the host-list alert visible behind it. The spec opens it with Enter from the focused Stop button, sets a `data-m3g-marker` attribute on the dialog before the failure and asserts it is still there, then presses Escape, expects the dialog gone, and expects the Stop button focused.
- UI-3: The 375 dark, 768 light and 1440 dark screenshots show three stacked warnings in the order exposed, "on: db-1", generic "Could not check whether the NPM admin UI is exposed." The spec asserts the link's href and that the generic warning clears. `banner.tsx` line 53 renders the generic warning in addition to the others when `fleet.isError`.
- UI-4: The 768 light connectors screenshot shows the alert and no table. The 375 dark screenshot shows only the generic warning from the fleet. The 1440 dark hosts screenshot shows the alert alone with no Host choice. The spec asserts each of these.
- UI-5: The 768 dark and 1440 light screenshots show the destructive alert above "No host runs the Cloudflare module". The spec asserts the alert is above the empty state and clears. `connectors-page.tsx` lines 254-255 match.
- OP-1: This is spec-only evidence. The spec runs axe (WCAG 2.1 A and AA) in the refetch-failed state for the UI-1, UI-2/UX-1, UI-3 and UI-5 tests, checks `scrollWidth <= clientWidth`, and asserts no console error other than "Failed to load resource" and no unhandled API request. All of this is run at six viewport and theme pairs through the loop over `THEMES` and `VIEWPORTS`.

## Notes on the report

- UI-1 note: the report says the table sits below the fold at 375. The cited 375 screenshot actually shows the table and both Stop buttons. The "Connectors on web-1" heading is clipped at the top edge. The note is wrong, but the evidence is better than the note says.
- UI-2 note: "Full-page screenshots" is loose. The app scrolls an inner container, so the captures are viewport-sized (1440x900, 768x1024).
- UX-1: the keyboard path starts at `stop.focus()` and Enter, not a Tab walk to the button. The contract only asks for opening by keyboard, Escape, and focus return, and the spec evidences those. The assertion message "was not remounted" for the Stop button is stronger than a `toBeFocused` check on a locator proves, but the dialog marker proves the dialog was not remounted, which is what the contract states.
- Only the refetch-failed states are checked with axe, console and network assertions. The UI-4 first-read test checks overflow only. The contract's OP-1 does not ask for more.
- Adjacent findings: the report discloses three layout defects in unchanged markup, visible in the cited screenshots: State badge text breaking across lines, long names overflowing the Host radio label, and the banner link at 375. I confirmed all three in UI-2 375 dark and UI-3 375 dark. They are outside this delta, so I did not count them against it. They are real and have no forcing decision cited, so they need their own follow-up. The Cloudflare banner showing a host id instead of a name is also disclosed and explained.

## Criteria reasoning

- coverage 17: every item has a row, cited screenshots that show the item, and a named spec test at all six pairs. Held back slightly because OP-1 and the clear-on-next-read behaviour rest on the spec alone, and I could not see a test result.
- fidelity 17: the destructive alert, held table, Host card, dialog, warning stack and empty state all match the contract, with the contract's copy in the code and screenshots. The pre-existing overflow defects are disclosed and outside the delta.
- states 16: refetch-failed, first-read-failed, clear-on-recovery and the open-dialog case are covered at all six pairs. The loading state is not in this delta. The keyboard evidence starts from a programmatic focus.
- deviations 17: the report marks no deviations. The differences I found are the disclosed adjacent findings and the two loose notes above.

Verdict: ship 701a2622098b36c9
