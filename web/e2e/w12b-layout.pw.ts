import { createRequire } from "node:module"
import { expect, test, type Locator, type Page } from "@playwright/test"
import type { Host } from "../src/api/types"
import { cfConnector, cfTunnel } from "../src/modules/cloudflare/fixtures"
import { npInstance } from "../src/modules/npm/fixtures"
import { HOST_ID, mockApi } from "./fixtures"

const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/w12b-layout"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

const LONG_NAME = `web-${"x".repeat(59)}`
const LONG_TUNNEL = `darkpanel-${"t".repeat(54)}`
const LONG_CONTAINER = `darkpanel-cloudflared-${"t".repeat(40)}`
const ADMIN_HOST = `npm-admin.${"e".repeat(40)}.example.com`
const EXPOSED = `The NPM admin interface of ${LONG_NAME} is exposed at https://${ADMIN_HOST}.`

function cfHost(id: string, name: string): Host {
  return {
    id, name, isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1,
    os: "debian-12", arch: "amd64", supported: true,
    modules: [{ id: "cloudflare", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
  }
}

const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })

async function open(page: Page, theme: (typeof THEMES)[number], cloudflare: boolean): Promise<string[]> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  await page.route(/\/actions\/core\.host\.list\/v1\/read$/, (route) =>
    cloudflare ? route.fulfill(ok({ hosts: [cfHost(HOST_ID, "web-1"), cfHost("01JE00000000000000000000L1", LONG_NAME)] })) : route.fallback(),
  )
  await page.route(/\/actions\/cloudflare\.tunnel\.list\/v1\/read$/, (route) =>
    route.fulfill(ok({ tunnels: [cfTunnel({ id: "t1", name: "darkpanel-c1" }), cfTunnel({ id: "t2", name: LONG_TUNNEL, status: "inactive", connections: [] })] })),
  )
  await page.route(/\/actions\/cloudflare\.connector\.list\/v1\/read$/, (route) =>
    route.fulfill(ok({ connectors: [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" }), cfConnector({ tunnel: "t2", container: LONG_CONTAINER, state: "exited" })] })),
  )
  await page.route(/\/actions\/npm\.instance\.list\/v1\/read$/, (route) =>
    route.fulfill(ok({ instances: [{ hostId: HOST_ID, hostName: LONG_NAME, status: npInstance({ deployed: true, adminExposure: { hostname: ADMIN_HOST, proxyHostId: 4, certificateId: 5 } }) }], unreadable: [] })),
  )
  return fixtures.unhandled
}

function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text())
  })
  page.on("pageerror", (e) => errors.push(String(e)))
  return errors
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

async function expectNoSidewaysScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true)
}

// expectInside fails when el's right edge passes its container's.
async function expectInside(el: Locator, container: Locator, what: string) {
  const [a, b] = [await el.boundingBox(), await container.boundingBox()]
  expect(a && b, `${what} is laid out`).toBeTruthy()
  expect(a!.x + a!.width, `${what} stays inside its container`).toBeLessThanOrEqual(b!.x + b!.width + 0.5)
}

async function lineCount(el: Locator): Promise<number> {
  return el.evaluate((e) => {
    const r = document.createRange()
    r.selectNodeContents(e)
    const tops = new Set(Array.from(r.getClientRects()).map((c) => Math.round(c.top)))
    return tops.size
  })
}

async function atLeastTenCharacters(el: Locator): Promise<boolean> {
  return el.evaluate((e) => {
    const s = document.createElement("span")
    s.style.font = getComputedStyle(e).font
    s.style.position = "absolute"
    s.style.visibility = "hidden"
    s.textContent = "0".repeat(10)
    document.body.appendChild(s)
    const w = s.getBoundingClientRect().width
    s.remove()
    return e.getBoundingClientRect().width >= w
  })
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      test(`UI-1, UI-2, UI-3: connectors page ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const unhandled = await open(page, theme, true)
        await page.goto(url("/cloudflare/connectors"))
        const radios = page.getByRole("radiogroup", { name: "Host" })
        await expectInside(page.getByText(LONG_NAME, { exact: true }), radios, "UI-3 the long host name")
        await page.getByRole("radio", { name: "web-1", exact: true }).click()
        const table = page.getByRole("table", { name: "Connectors" })
        await expect(table).toBeVisible()
        for (const state of ["running", "exited"]) {
          const badge = table.getByText(state, { exact: true })
          expect(await lineCount(badge), `UI-1 the ${state} badge`).toBe(1)
          await expectInside(badge, badge.locator("xpath=ancestor::td[1]"), `UI-1 the ${state} badge`)
        }
        const remPx = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize))
        const box = await table.boundingBox()
        expect(box!.width, "UI-2 the table is at least 40rem wide").toBeGreaterThanOrEqual(40 * remPx - 0.5)
        const wrapper = table.locator("xpath=..")
        const fit = await wrapper.evaluate((w) => ({ client: w.clientWidth, scroll: w.scrollWidth, overflowX: getComputedStyle(w).overflowX }))
        if (vp.width === 375) {
          expect(fit.client + 1 < box!.width, "UI-2 at 375 px the wrapper is narrower than the table").toBe(true)
        }
        if (fit.client + 1 < box!.width) {
          expect(["auto", "scroll"], "UI-2 a wrapper narrower than the table scrolls").toContain(fit.overflowX)
          expect(fit.scroll, "UI-2 the wrapper's scroll width holds the whole table").toBeGreaterThanOrEqual(box!.width - 0.5)
        }
        for (const name of [LONG_TUNNEL, LONG_CONTAINER]) {
          const cell = table.getByRole("cell", { name, exact: true })
          expect(await atLeastTenCharacters(cell), `UI-2 the cell of ${name.slice(0, 16)}… is at least 10 characters wide`).toBe(true)
          await expectInside(cell, table, `UI-2 the cell of ${name.slice(0, 16)}…`)
        }
        await expectNoSidewaysScroll(page)
        await expectNoAxeViolations(page, "connectors page")
        await page.screenshot({ path: `${EVIDENCE}/UI-1-UI-2-UI-3-${tag}.png` })
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UI-4: NPM banner ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const unhandled = await open(page, theme, false)
        await page.goto(url("/"))
        const banner = page.getByRole("status").filter({ hasText: "Admin interface exposed" })
        await expect(banner).toBeVisible()
        const link = banner.getByRole("link", { name: `Open Nginx Proxy Manager on ${LONG_NAME}` })
        await expectInside(link, banner, "UI-4 the banner link")
        await expectInside(banner.getByText(EXPOSED), banner, "UI-4 the banner description")
        expect(await link.evaluate((e) => getComputedStyle(e).textDecorationLine), "UI-4 the link is underlined at rest").toContain("underline")
        await expectNoSidewaysScroll(page)
        await expectNoAxeViolations(page, "NPM banner")
        await page.screenshot({ path: `${EVIDENCE}/UI-4-${tag}.png` })
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    })
  }
}
