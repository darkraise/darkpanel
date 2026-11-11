import { createRequire } from "node:module"
import { expect, test, type Locator, type Page } from "@playwright/test"
import { HOST_ID, PENDING_HOST_ID, mockApi } from "./fixtures"

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/m2-web-hosts"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`
const FAILURE = "The controller could not read its database."

type Answer = "hang" | "fail" | { output: unknown }

// Registered after mockApi, so it answers the read before the fixtures do.
async function answerRead(page: Page, action: string, answer: Answer) {
  await page.route(new RegExp(`/actions/${action.replace(/\./g, "\\.")}/v1/read$`), async (route) => {
    if (answer === "hang") return
    if (answer === "fail") {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ type: "about:blank", title: FAILURE, status: 500, detail: FAILURE, code: "internal" }) })
      return
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(answer) })
  })
}

async function open(page: Page, theme: (typeof THEMES)[number], worstCase: boolean) {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  return mockApi(page, { signedIn: true, worstCase })
}

async function expectNoAxeViolations(page: Page, where: string) {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: { id: string; nodes: { target: string[] }[] }[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
    return result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)
  })
  expect(violations, where).toEqual([])
}

interface State {
  id: string
  state: string
  path: string
  worstCase: boolean
  setup: (page: Page) => Promise<void>
  shows: (page: Page) => Locator
}

const none = async () => {}

const TX_STATES = ["pending", "downloading", "staged", "swapped", "rolling-back", "rolled-back", "committed", "failed"]
const TRANSACTIONS = TX_STATES.map((state, i) => {
  const at = new Date(Date.UTC(2026, 10, 11, 8 - i)).toISOString()
  const failed = state === "rolled-back" || state === "failed"
  return {
    id: `tx-${i}`, operationId: `tx-${i}`, parentId: null, kind: "update",
    fromVersion: "1.0.0", toVersion: `1.1.${i}`, targetVersion: `1.1.${i}`, state,
    error: failed ? { code: "update.health_failed", message: "the health check failed" } : null,
    startedAt: at, updatedAt: at,
  }
})

const STATES: State[] = [
  { id: "UX-1", state: "loading", path: "/", worstCase: true, setup: (p) => answerRead(p, "core.host.list", "hang"), shows: (p) => p.getByLabel("Loading the hosts") },
  { id: "UX-1", state: "error", path: "/", worstCase: true, setup: (p) => answerRead(p, "core.host.list", "fail"), shows: (p) => p.getByRole("alert").filter({ hasText: FAILURE }) },
  { id: "UX-1", state: "empty", path: "/", worstCase: true, setup: (p) => answerRead(p, "core.host.list", { output: { hosts: [] } }), shows: (p) => p.getByText("No hosts yet") },
  { id: "UX-2", state: "loading", path: `/hosts/${HOST_ID}`, worstCase: true, setup: (p) => answerRead(p, "core.host.list", "hang"), shows: (p) => p.getByLabel("Loading the host") },
  { id: "UX-2", state: "error", path: `/hosts/${HOST_ID}`, worstCase: true, setup: (p) => answerRead(p, "core.host.list", "fail"), shows: (p) => p.getByRole("alert").filter({ hasText: FAILURE }) },
  { id: "UX-2", state: "not-found", path: "/hosts/01JE0000000000000000000NONE", worstCase: true, setup: none, shows: (p) => p.getByText("Host not found") },
  { id: "UX-2", state: "history-loading", path: `/hosts/${HOST_ID}`, worstCase: true, setup: (p) => answerRead(p, "core.update.status", "hang"), shows: (p) => p.getByLabel("Loading the update history") },
  { id: "UX-2", state: "history-error", path: `/hosts/${HOST_ID}`, worstCase: true, setup: (p) => answerRead(p, "core.update.status", "fail"), shows: (p) => p.getByRole("alert").filter({ hasText: FAILURE }) },
  { id: "UX-2", state: "history-empty", path: `/hosts/${HOST_ID}`, worstCase: false, setup: none, shows: (p) => p.getByText("No updates yet") },
  { id: "UX-2", state: "history", path: `/hosts/${HOST_ID}`, worstCase: true, setup: none, shows: (p) => p.getByRole("table", { name: "Update history" }) },
  { id: "UI-4", state: "transactions", path: `/hosts/${HOST_ID}`, worstCase: true, setup: (p) => answerRead(p, "core.update.status", { output: { transactions: TRANSACTIONS } }), shows: (p) => p.getByRole("table", { name: "Update history" }) },
  { id: "UX-2", state: "offline", path: `/hosts/${PENDING_HOST_ID}`, worstCase: true, setup: none, shows: (p) => p.getByText("The update history is unavailable while the host is offline.") },
  { id: "UI-3", state: "failed", path: "/rollouts", worstCase: true, setup: none, shows: (p) => p.getByRole("region", { name: "1.2.9 (beta)" }).getByText("web-1 failed its health check").first() },
  { id: "UX-8", state: "loading", path: "/rollouts", worstCase: true, setup: (p) => answerRead(p, "core.update.rollouts", "hang"), shows: (p) => p.getByLabel("Loading the rollouts") },
  { id: "UX-8", state: "error", path: "/rollouts", worstCase: true, setup: (p) => answerRead(p, "core.update.rollouts", "fail"), shows: (p) => p.getByRole("alert").filter({ hasText: FAILURE }) },
  { id: "UX-8", state: "empty", path: "/rollouts", worstCase: false, setup: none, shows: (p) => p.getByText("No rollouts yet") },
  { id: "UX-9", state: "disabled", path: "/rollouts", worstCase: true, setup: (p) => answerRead(p, "core.host.list", "hang"), shows: (p) => p.getByRole("button", { name: "Start rollout" }).first() },
]

// Each dialog opens from a page, is scanned by axe while open and, where an ID
// is given, screenshotted; invalid opens it and presses Continue empty.
const DIALOGS: { path: string; button: string; id?: string; invalid?: boolean }[] = [
  { path: `/hosts/${HOST_ID}`, button: "Update now", id: "UX-7" },
  { path: `/hosts/${HOST_ID}`, button: "Update now", id: "UX-7", invalid: true },
  { path: `/hosts/${HOST_ID}`, button: "Change update policy" },
  { path: `/hosts/${PENDING_HOST_ID}`, button: "New enrollment token" },
  { path: `/hosts/${PENDING_HOST_ID}`, button: "Revoke host", id: "UX-3" },
  { path: "/rollouts", button: "Start rollout", id: "UX-9" },
  { path: "/rollouts", button: "Start rollout", id: "UX-9", invalid: true },
  { path: "/rollouts", button: "Cancel rollout", id: "UX-10" },
]

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      for (const s of STATES) {
        test(`${s.id}: ${s.state} ${tag}`, async ({ page }) => {
          await open(page, theme, s.worstCase)
          await s.setup(page)
          await page.goto(url(s.path))
          const shown = s.shows(page)
          await expect(shown).toBeVisible()
          if (s.state === "disabled") await expect(shown).toBeDisabled()
          if (s.state === "transactions") {
            for (const t of TX_STATES) await expect(shown.getByText(t, { exact: true })).toBeVisible()
            await expectNoAxeViolations(page, "transactions")
          }
          await shown.scrollIntoViewIfNeeded()
          if (s.state === "transactions") await shown.getByText("rolled-back", { exact: true }).scrollIntoViewIfNeeded()
          await page.screenshot({ path: `${EVIDENCE}/${s.id}-${tag}-${s.state}.png`, fullPage: s.state === "transactions" })
        })
      }

      test(`UX-11: focus ring ${tag}`, async ({ page }) => {
        await open(page, theme, true)
        await page.goto(url(`/hosts/${HOST_ID}`))
        const button = page.getByRole("button", { name: "Update now" })
        await expect(button).toBeVisible()
        await page.keyboard.press("Tab")
        await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused()
        await page.keyboard.press("Enter")
        for (let i = 0; i < 80 && !(await button.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
        await expect(button).toBeFocused()
        await page.screenshot({ path: `${EVIDENCE}/UX-11-${tag}-focus.png` })
      })

      test(`OP-3: axe and console on every page and dialog ${tag}`, async ({ page }) => {
        test.setTimeout(180_000)
        const errors: string[] = []
        page.on("console", (m) => {
          if (m.type() === "error") errors.push(m.text())
        })
        page.on("pageerror", (e) => errors.push(String(e)))
        const fixtures = await open(page, theme, true)
        for (const [path, ready] of [
          ["/", (p: Page) => p.getByRole("table").getByRole("link", { name: "node-45" })],
          [`/hosts/${HOST_ID}`, (p: Page) => p.getByRole("table", { name: "Update history" })],
          [`/hosts/${PENDING_HOST_ID}`, (p: Page) => p.getByText("The update history is unavailable while the host is offline.")],
          ["/rollouts", (p: Page) => p.getByRole("heading", { name: "1.3.0 (stable)" })],
        ] as const) {
          await page.goto(url(path))
          await expect(ready(page)).toBeVisible()
          await expectNoAxeViolations(page, path)
        }
        for (const d of DIALOGS) {
          await page.goto(url(d.path))
          const button = page.getByRole("button", { name: d.button }).first()
          await expect(button).toBeEnabled()
          await button.click()
          const dialog = page.getByRole("dialog")
          await expect(dialog).toHaveAttribute("data-state", "open")
          await page.waitForFunction(() => document.getAnimations().length === 0)
          if (d.invalid === true) await dialog.getByRole("button", { name: "Continue" }).click()
          await expectNoAxeViolations(page, `${d.button}${d.invalid === true ? " refused" : ""}`)
          if (d.id !== undefined) await page.screenshot({ path: `${EVIDENCE}/${d.id}-${tag}${d.invalid === true ? "-error" : ""}.png` })
        }
        expect(errors, "console errors").toEqual([])
        expect(fixtures.unhandled, "API requests without a fixture").toEqual([])
      })
    })
  }
}
