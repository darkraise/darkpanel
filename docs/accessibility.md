# Accessibility

Darkpanel's web interface targets WCAG 2.1 level AA. This record covers the six core flows named in Scope and, for each flow, the success criteria that apply to it. A criterion is marked `automated` when a Vitest test checks it with `vitest-axe` or a keyboard test, and `manual` when the Playwright pass in Chromium checks it against fixtures or when it comes from a reading of a Markdown page. The Playwright pass rewrites the screenshots under `docs/accessibility/`.

```sh
pnpm -C web test
pnpm -C web exec playwright test -c playwright.config.ts
```

Set `E2E_CHROMIUM` to the path of a Chromium executable when the build that Playwright pins is not installed. The browser pass covers the default theme (blue accent, zinc surface) in light and dark mode, in Chromium only. The last run, on 2026-11-06 with Chromium 153.0.8010.12, passed all 88 checks.

## Scope

- Login with TOTP: the login page.
- First-admin and lockout recovery pages: `docs/user/first-admin.md` and `docs/user/lockout-recovery.md`.
- Host list: the hosts page and the shell around it.
- Firewall change with confirm and revert: the firewall page, the rule form and the change dialog through its pending, confirm and revert states.
- Container actions: the Docker containers table, its change dialogs and the container page with the firewall allow and deny forms.
- Expose a container: the expose form of the container page and its change dialog.

The confirm and revert states covered are those of a revertible change. A composite change that waits for confirmation, such as a public deploy of Nginx Proxy Manager with the firewall integration on, shows the same countdown with Confirm and Revert in the change dialog, counted to the deadline of its last step. The Vitest tests of the change dialog (`web/src/components/change-dialog.test.tsx` and `web/src/components/change-dialog.a11y.test.tsx`) check that state; the browser pass does not render it.

The contrast evidence covers the default accent (blue) and surface (zinc) in both modes. The record does not cover other accent or surface choices of the theme switcher, other browsers, or screen readers other than the accessible-name check of the change dialog.

## Criteria

### Login with TOTP

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.1.1 Non-text content | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 1.3.1 Info and relationships | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 3.3.2 Labels or instructions | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 4.1.2 Name, role, value | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 2.1.1 Keyboard | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 2.4.3 Focus order | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 2.4.2 Page titled | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 3.3.1 Error identification | A | automated | `web/src/pages/login.a11y.test.tsx` |
| 4.1.3 Status messages | AA | automated | `web/src/pages/login.a11y.test.tsx` |
| 1.4.3 Contrast (minimum) | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/login-light-contrast.png`, `docs/accessibility/login-dark-contrast.png` |
| 1.4.4 Resize text | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/login-light-zoom-200.png`, `docs/accessibility/login-dark-zoom-200.png` |
| 1.4.10 Reflow | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/login-light-reflow-320.png`, `docs/accessibility/login-dark-reflow-320.png` |
| 1.4.11 Non-text contrast | AA | manual | `web/e2e/a11y.pw.ts` |
| 2.4.7 Focus visible | AA | manual | `web/e2e/a11y.pw.ts` |

### First-admin and lockout recovery pages

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.3.1 Info and relationships | A | manual | `docs/user/first-admin.md`, `docs/user/lockout-recovery.md` |
| 2.4.2 Page titled | A | manual | `docs/user/first-admin.md`, `docs/user/lockout-recovery.md` |

Both pages were read on 2026-11-05 for heading order and for link text that names its target.

### Host list

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.1.1 Non-text content | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/host-overview.a11y.test.tsx`, `web/src/pages/rollouts.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx`, `web/src/modules/derp/global.a11y.test.tsx` |
| 1.3.1 Info and relationships | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/host-overview.a11y.test.tsx`, `web/src/pages/rollouts.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx`, `web/src/modules/derp/global.a11y.test.tsx` |
| 3.3.2 Labels or instructions | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx`, `web/src/modules/derp/global.a11y.test.tsx` |
| 4.1.2 Name, role, value | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/host-overview.a11y.test.tsx`, `web/src/pages/rollouts.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx`, `web/src/modules/derp/global.a11y.test.tsx` |
| 2.1.1 Keyboard | A | automated | `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx` |
| 2.4.3 Focus order | A | automated | `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx` |
| 2.1.2 No keyboard trap | A | automated | `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx` |
| 2.4.2 Page titled | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx`, `web/src/modules/tailnet/global.a11y.test.tsx`, `web/src/modules/derp/global.a11y.test.tsx` |
| 3.3.1 Error identification | A | automated | `web/src/pages/hosts.a11y.test.tsx`, `web/src/pages/host.a11y.test.tsx`, `web/src/pages/audit.a11y.test.tsx`, `web/src/pages/tokens.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx`, `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change.a11y.test.tsx` |
| 4.1.3 Status messages | AA | automated | `web/src/pages/create-host-dialog.a11y.test.tsx`, `web/src/pages/change-actions.a11y.test.tsx` |
| 1.4.3 Contrast (minimum) | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/hosts-light-contrast.png`, `docs/accessibility/hosts-dark-contrast.png` |
| 1.4.4 Resize text | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/hosts-light-zoom-200.png`, `docs/accessibility/hosts-dark-zoom-200.png` |
| 1.4.10 Reflow | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/hosts-light-reflow-320.png`, `docs/accessibility/hosts-dark-reflow-320.png` |
| 1.4.11 Non-text contrast | AA | manual | `web/e2e/a11y.pw.ts` |
| 2.4.7 Focus visible | AA | manual | `web/e2e/a11y.pw.ts` |

### Firewall change with confirm and revert

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.1.1 Non-text content | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 1.3.1 Info and relationships | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 3.3.2 Labels or instructions | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 4.1.2 Name, role, value | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 2.1.1 Keyboard | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 2.4.3 Focus order | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/firewall-page.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx`, `web/src/modules/firewall/docker-panel.a11y.test.tsx` |
| 2.1.2 No keyboard trap | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/firewall-change.a11y.test.tsx`, `web/src/modules/firewall/rule-form.a11y.test.tsx`, `web/src/modules/firewall/rules-table.a11y.test.tsx`, `web/src/modules/firewall/snapshots-panel.a11y.test.tsx`, `web/src/modules/firewall/state-controls.a11y.test.tsx`, `web/src/modules/firewall/status-panel.a11y.test.tsx` |
| 2.4.2 Page titled | A | automated | `web/src/pages/host.a11y.test.tsx` |
| 3.3.1 Error identification | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/firewall/exposure-page.a11y.test.tsx` |
| 4.1.3 Status messages | AA | automated | `web/src/components/change-dialog.a11y.test.tsx` |
| 1.4.3 Contrast (minimum) | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/firewall-change-plan-light-contrast.png`, `docs/accessibility/firewall-change-plan-dark-contrast.png`, `docs/accessibility/firewall-change-applied-light-contrast.png`, `docs/accessibility/firewall-change-applied-dark-contrast.png` |
| 1.4.4 Resize text | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/firewall-change-plan-light-zoom-200.png`, `docs/accessibility/firewall-change-plan-dark-zoom-200.png`, `docs/accessibility/firewall-change-applied-light-zoom-200.png`, `docs/accessibility/firewall-change-applied-dark-zoom-200.png` |
| 1.4.10 Reflow | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/firewall-change-plan-light-reflow-320.png`, `docs/accessibility/firewall-change-plan-dark-reflow-320.png`, `docs/accessibility/firewall-change-applied-light-reflow-320.png`, `docs/accessibility/firewall-change-applied-dark-reflow-320.png` |
| 1.4.11 Non-text contrast | AA | manual | `web/e2e/a11y.pw.ts` |
| 2.4.7 Focus visible | AA | manual | `web/e2e/a11y.pw.ts` |
| 4.1.2 Name, role, value | A | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/firewall-change-plan-light-names.png`, `docs/accessibility/firewall-change-plan-dark-names.png`, `docs/accessibility/firewall-change-applied-light-names.png`, `docs/accessibility/firewall-change-applied-dark-names.png` |

### Container actions

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.1.1 Non-text content | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 1.3.1 Info and relationships | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 3.3.2 Labels or instructions | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 4.1.2 Name, role, value | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 2.1.1 Keyboard | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 2.4.3 Focus order | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 2.1.2 No keyboard trap | A | automated | `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/docker-change.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 2.4.2 Page titled | A | automated | `web/src/pages/host.a11y.test.tsx` |
| 3.3.1 Error identification | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/docker/containers-tab.a11y.test.tsx`, `web/src/modules/docker/container-page.a11y.test.tsx`, `web/src/modules/firewall/container-actions.a11y.test.tsx` |
| 4.1.3 Status messages | AA | automated | `web/src/components/change-dialog.a11y.test.tsx` |
| 1.4.3 Contrast (minimum) | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-actions-list-light-contrast.png`, `docs/accessibility/container-actions-list-dark-contrast.png`, `docs/accessibility/container-actions-dialog-light-contrast.png`, `docs/accessibility/container-actions-dialog-dark-contrast.png`, `docs/accessibility/container-page-light-contrast.png`, `docs/accessibility/container-page-dark-contrast.png` |
| 1.4.4 Resize text | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-actions-list-light-zoom-200.png`, `docs/accessibility/container-actions-list-dark-zoom-200.png`, `docs/accessibility/container-actions-dialog-light-zoom-200.png`, `docs/accessibility/container-actions-dialog-dark-zoom-200.png`, `docs/accessibility/container-page-light-zoom-200.png`, `docs/accessibility/container-page-dark-zoom-200.png` |
| 1.4.10 Reflow | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-actions-list-light-reflow-320.png`, `docs/accessibility/container-actions-list-dark-reflow-320.png`, `docs/accessibility/container-actions-dialog-light-reflow-320.png`, `docs/accessibility/container-actions-dialog-dark-reflow-320.png`, `docs/accessibility/container-page-light-reflow-320.png`, `docs/accessibility/container-page-dark-reflow-320.png` |
| 1.4.11 Non-text contrast | AA | manual | `web/e2e/a11y.pw.ts` |
| 2.4.7 Focus visible | AA | manual | `web/e2e/a11y.pw.ts` |
| 4.1.2 Name, role, value | A | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-actions-dialog-light-names.png`, `docs/accessibility/container-actions-dialog-dark-names.png` |

### Expose a container

| Criterion | Level | Method | Evidence |
|---|---|---|---|
| 1.1.1 Non-text content | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 1.3.1 Info and relationships | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 3.3.2 Labels or instructions | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 4.1.2 Name, role, value | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 2.1.1 Keyboard | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx` |
| 2.4.3 Focus order | A | automated | `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/cloudflare/global.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 2.1.2 No keyboard trap | A | automated | `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/cloudflare-change.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx`, `web/src/modules/npm/npm-change.a11y.test.tsx` |
| 2.4.2 Page titled | A | automated | `web/src/modules/cloudflare/global.a11y.test.tsx` |
| 3.3.1 Error identification | A | automated | `web/src/components/change-dialog.a11y.test.tsx`, `web/src/modules/cloudflare/container-actions.a11y.test.tsx`, `web/src/modules/cloudflare/expose-dialog.a11y.test.tsx`, `web/src/modules/npm/container-actions.a11y.test.tsx` |
| 4.1.3 Status messages | AA | automated | `web/src/components/change-dialog.a11y.test.tsx` |
| 1.4.3 Contrast (minimum) | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-page-light-contrast.png`, `docs/accessibility/container-page-dark-contrast.png`, `docs/accessibility/expose-plan-light-contrast.png`, `docs/accessibility/expose-plan-dark-contrast.png` |
| 1.4.4 Resize text | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-page-light-zoom-200.png`, `docs/accessibility/container-page-dark-zoom-200.png`, `docs/accessibility/expose-plan-light-zoom-200.png`, `docs/accessibility/expose-plan-dark-zoom-200.png` |
| 1.4.10 Reflow | AA | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/container-page-light-reflow-320.png`, `docs/accessibility/container-page-dark-reflow-320.png`, `docs/accessibility/expose-plan-light-reflow-320.png`, `docs/accessibility/expose-plan-dark-reflow-320.png` |
| 1.4.11 Non-text contrast | AA | manual | `web/e2e/a11y.pw.ts` |
| 2.4.7 Focus visible | AA | manual | `web/e2e/a11y.pw.ts` |
| 4.1.2 Name, role, value | A | manual | `web/e2e/a11y.pw.ts`, `docs/accessibility/expose-plan-light-names.png`, `docs/accessibility/expose-plan-dark-names.png` |

## Known issues

- 1.4.10 Reflow: at 320 px the "Planned changes" block scrolls sideways inside the change dialog. This is an exception WCAG 1.4.10 allows for content that needs two-dimensional layout, and the browser pass accepts it by design. The page and the rest of the dialog do not scroll sideways.
