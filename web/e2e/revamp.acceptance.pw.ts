import { createRequire } from "node:module"
import { expect, test, type Page } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")

const viewports = [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1440, height: 900 },
]
const host = `/hosts/${HOST_ID}`
const routes = ["/", host, `${host}/system`, `${host}/system/updates`, `${host}/system/packages`, `${host}/docker`, `${host}/firewall`, "/audit", "/rollouts", "/settings/tokens"]

interface Violation {
  id: string
  nodes: { target: unknown[] }[]
}

// axeViolations runs the WCAG 2.1 A and AA rules (OP-3) once the theme and
// every finite animation have settled, so no half-faded colour is sampled.
async function axeViolations(page: Page, mode: string): Promise<string[]> {
  await expect(page.locator("html")).toHaveAttribute("data-mode", mode)
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => undefined)),
    ),
  )
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (c: Document, o: object) => Promise<{ violations: Violation[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
    return result.violations
  })
  return violations.flatMap((v) => v.nodes.map((n) => `${v.id} ${JSON.stringify(n.target)}`))
}

for (const mode of ["light", "dark"] as const) {
  for (const vp of viewports) {
    test(`A34-A35 ${mode} ${vp.width}: every route renders worst case, passes axe, no horizontal scroll`, async ({ page }) => {
      test.setTimeout(180_000)
      const fixtures = await mockApi(page, { signedIn: true, worstCase: true })
      await page.setViewportSize(vp)
      await page.addInitScript((m) => localStorage.setItem("mode", m), mode)
      for (const r of routes) {
        await page.goto(r)
        await expect(page.getByRole("main")).toBeVisible()
        await page.waitForLoadState("networkidle")
        expect(await axeViolations(page, mode), r).toEqual([])
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), r).toBe(true)
      }
      expect(fixtures.unhandled).toEqual([])
    })
  }
}

test("A36: LCP under 2.5 s and CLS under 0.1 on / and the host overview", async ({ page }) => {
  await mockApi(page, { signedIn: true })
  for (const r of ["/", host]) {
    await page.goto(r)
    const m = await page.evaluate(
      () =>
        new Promise<{ lcp: number; cls: number }>((resolve) => {
          let lcp = 0
          let cls = 0
          new PerformanceObserver((list) => {
            for (const e of list.getEntries()) lcp = e.startTime
          }).observe({ type: "largest-contentful-paint", buffered: true })
          new PerformanceObserver((list) => {
            for (const e of list.getEntries() as unknown as { value: number; hadRecentInput: boolean }[]) if (!e.hadRecentInput) cls += e.value
          }).observe({ type: "layout-shift", buffered: true })
          setTimeout(() => resolve({ lcp, cls }), 3000)
        }),
    )
    expect(m.lcp, r).toBeLessThan(2500)
    expect(m.cls, r).toBeLessThan(0.1)
  }
})
