import { createRequire } from "node:module"
import { expect, test, type Locator, type Page } from "@playwright/test"
import type { Host } from "../src/api/types"
import { cfConnector, cfTunnel } from "../src/modules/cloudflare/fixtures"
import { HOST_ID, mockApi } from "./fixtures"

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/m6-tunnel-status"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

const LONG_TUNNEL = `darkpanel-${"t".repeat(54)}`
const LONG_CONTAINER = `darkpanel-cloudflared-${"c".repeat(40)}`
const NOT_CONNECTED = "no Cloudflare account is connected"

const HOST: Host = {
  id: HOST_ID, name: "web-1", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0",
  protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true,
  modules: [{ id: "cloudflare", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
}
const TUNNELS = [
  cfTunnel({ id: "t1", name: "darkpanel-c1", status: "healthy" }),
  cfTunnel({ id: "t2", name: LONG_TUNNEL, status: "degraded" }),
  cfTunnel({ id: "t3", name: "darkpanel-c3", status: "down", connections: [] }),
  cfTunnel({ id: "t4", name: "darkpanel-c4", status: "inactive", connections: [] }),
  cfTunnel({ id: "t5", name: "darkpanel-c5", status: "unknown", connections: null }),
]
const CONNECTORS = [
  cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1", state: "running" }),
  cfConnector({ tunnel: "t2", container: LONG_CONTAINER, state: "running" }),
  cfConnector({ tunnel: "t3", container: "darkpanel-cloudflared-t3", state: "running" }),
  cfConnector({ tunnel: "t4", container: "darkpanel-cloudflared-t4", state: "exited" }),
  cfConnector({ tunnel: "t5", container: "darkpanel-cloudflared-t5", state: "restarting" }),
  cfConnector({ tunnel: "t-gone", container: "darkpanel-cloudflared-t-gone", state: "missing" }),
]
const EXPECTED = [
  ["darkpanel-c1", "healthy"],
  [LONG_TUNNEL, "degraded"],
  ["darkpanel-c3", "down"],
  ["darkpanel-c4", "inactive"],
  ["darkpanel-c5", "unknown"],
  ["t-gone", "unknown"],
]

const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })

async function open(page: Page, theme: (typeof THEMES)[number], tunnels: "ok" | "fail"): Promise<string[]> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  await page.route(/\/actions\/core\.host\.list\/v1\/read$/, (route) => route.fulfill(ok({ hosts: [HOST] })))
  await page.route(/\/actions\/cloudflare\.tunnel\.list\/v1\/read$/, (route) =>
    route.fulfill(
      tunnels === "ok"
        ? ok({ tunnels: TUNNELS })
        : { status: 409, contentType: "application/json", body: JSON.stringify({ type: "about:blank", title: "Not connected", status: 409, detail: NOT_CONNECTED, code: "cloud.not_connected" }) },
    ),
  )
  await page.route(/\/actions\/cloudflare\.connector\.list\/v1\/read$/, (route) => route.fulfill(ok({ connectors: CONNECTORS })))
  await page.route(/\/actions\/npm\.instance\.list\/v1\/read$/, (route) => route.fulfill(ok({ instances: [], unreadable: [] })))
  return fixtures.unhandled
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

// A refused read logs the browser's own "Failed to load resource" line; any
// other console error is a defect.
function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text())
  })
  page.on("pageerror", (e) => errors.push(String(e)))
  return errors
}

// The number of line boxes the cell's text lays out in: 1 when a badge keeps
// its word on one line.
function textLines(cell: Locator): Promise<number> {
  return cell.evaluate((el) => {
    const tops = new Set<number>()
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      const range = document.createRange()
      range.selectNodeContents(node)
      for (const r of range.getClientRects()) if (r.width > 0) tops.add(Math.round(r.top))
    }
    return tops.size
  })
}

async function openTable(page: Page): Promise<Locator> {
  await page.goto(url("/cloudflare/connectors"))
  const table = page.getByRole("table", { name: "Connectors" })
  await expect(table.getByRole("row")).toHaveCount(CONNECTORS.length + 1)
  return table
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      test(`UI-1, UI-3, UX-1, OP-1: each connector beside its tunnel's status ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const unhandled = await open(page, theme, "ok")
        const table = await openTable(page)
        await expect(table.getByRole("columnheader")).toHaveText(["Tunnel", "Tunnel status", "Container", "State", "Actions"])
        const rows = table.getByRole("row")
        for (const [i, [tunnel, status]] of EXPECTED.entries()) {
          const cells = rows.nth(i + 1).getByRole("cell")
          await expect(cells.nth(0)).toHaveText(tunnel)
          await expect(cells.nth(1)).toHaveText(status)
          expect(await textLines(cells.nth(1)), `the ${status} badge of ${tunnel} stays on one line`).toBe(1)
          expect(await textLines(cells.nth(3)), `the state badge of ${tunnel} stays on one line`).toBe(1)
        }
        if (vp.width === 375) {
          expect(await textLines(rows.nth(2).getByRole("cell").nth(0)), "control: the line count sees the long tunnel name wrap").toBeGreaterThan(1)
        }
        await expectNoAxeViolations(page, "connectors with every tunnel status")
        await expectNoSidewaysScroll(page)
        await table.screenshot({ path: `${EVIDENCE}/UI-1-${tag}-table.png` })
        await rows.nth(2).getByRole("cell").nth(1).scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${EVIDENCE}/UI-3-${tag}-page.png` })
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UI-2, OP-1: a tunnel list that cannot be read shows unknown ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const unhandled = await open(page, theme, "fail")
        const table = await openTable(page)
        await expect(page.getByRole("alert").filter({ hasText: NOT_CONNECTED })).toBeVisible()
        const rows = table.getByRole("row")
        for (const [i, c] of CONNECTORS.entries()) {
          const cells = rows.nth(i + 1).getByRole("cell")
          await expect(cells.nth(0)).toHaveText(c.tunnel)
          await expect(cells.nth(1)).toHaveText("unknown")
        }
        await expectNoAxeViolations(page, "tunnel list refused")
        await expectNoSidewaysScroll(page)
        await table.screenshot({ path: `${EVIDENCE}/UI-2-${tag}-table.png` })
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    })
  }
}
