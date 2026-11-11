# UI profile

**Platform:** web
**Harness:** `pnpm -C web exec playwright test`, which serves the app with `vite --host 127.0.0.1 --port 0` and answers every `/api/` request from `web/e2e/fixtures.ts` (`mockApi`); a walkthrough runs `pnpm -C web dev` with the same fixtures, driven by Playwright MCP
**UI test runner:** `pnpm -C web test` (Vitest with Testing Library and vitest-axe); `pnpm -C web exec playwright test` for the browser pass
**UI paths:** `web/src/*.tsx` `web/src/*.css` `web/e2e/*.ts`
**Design system:** darkraise-ui 6.9.6, theme in `web/src/theme.config.ts`
**Token paths:** `web/src/theme.config.ts` `web/src/styles/*.css`
**Viewports:** 375x812, 768x1024, 1440x900
**Themes:** light, dark
**Users:** self-hosting operators managing a handful of VPS hosts from a laptop, sometimes a phone
**Voice:** plain, sentence case, verbs on buttons
**Screenshots:** local

## Decisions

- 2026-11-11 — Accessibility target WCAG 2.1 AA, as `docs/accessibility.md` records.
- 2026-11-11 — Every change goes through the plan, typed confirmation and apply flow of `ChangeDialog`; secrets an action returns are shown once, in a dialog with a copy button.
- 2026-11-11 — State boards are built for new screens before they are coded (owner, M2 web hosts).
