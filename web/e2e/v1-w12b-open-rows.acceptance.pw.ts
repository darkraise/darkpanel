import { createRequire } from "node:module"
import { expect, test, type Locator, type Page } from "@playwright/test"
import type { Host } from "../src/api/types"
import { cfConnector, cfTunnel } from "../src/modules/cloudflare/fixtures"
import { npInstance } from "../src/modules/npm/fixtures"
import { HOST_ID, mockApi } from "./fixtures"

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

const LONG_HOST = `web-${"x".repeat(59)}`
const LONG_TUNNEL = `darkpanel-${"t".repeat(54)}`
const LONG_CONTAINER = `darkpanel-cloudflared-${"c".repeat(40)}`
const LONG_ADMIN = "a".repeat(62)
const LONG_HOST_ID = "01JE00000000000000000000L1"

function cfHost(id: string, name: string): Host {
  return {
    id, name, isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1,
    os: "debian-12", arch: "amd64", supported: true,
    modules: [{ id: "cloudflare", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
  }
}

const TUNNELS = [
  cfTunnel({ id: "t1", name: "darkpanel-c1" }),
  cfTunnel({ id: "t2", name: LONG_TUNNEL, status: "inactive", connections: [] }),
  cfTunnel({ id: "t3", name: "darkpanel-c3" }),
  cfTunnel({ id: "t4", name: "darkpanel-c4" }),
]
const CONNECTORS = [
  cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1", state: "running" }),
  cfConnector({ tunnel: "t2", container: LONG_CONTAINER, state: "exited" }),
  cfConnector({ tunnel: "t3", container: "darkpanel-cloudflared-t3", state: "restarting" }),
  cfConnector({ tunnel: "t4", container: "darkpanel-cloudflared-t4", state: "missing" }),
]

const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })

async function open(page: Page, theme: (typeof THEMES)[number]): Promise<string[]> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  await page.route(/\/actions\/core\.host\.list\/v1\/read$/, (route) => route.fulfill(ok({ hosts: [cfHost(HOST_ID, "web-1"), cfHost(LONG_HOST_ID, LONG_HOST)] })))
  await page.route(/\/actions\/cloudflare\.tunnel\.list\/v1\/read$/, (route) => route.fulfill(ok({ tunnels: TUNNELS })))
  await page.route(/\/actions\/cloudflare\.connector\.list\/v1\/read$/, (route) => route.fulfill(ok({ connectors: CONNECTORS })))
  await page.route(/\/actions\/npm\.instance\.list\/v1\/read$/, (route) =>
    route.fulfill(
      ok({
        instances: [{ hostId: LONG_HOST_ID, hostName: LONG_HOST, status: npInstance({ deployed: true, adminExposure: { hostname: LONG_ADMIN, proxyHostId: 4, certificateId: 5 } }) }],
        unreadable: [],
      }),
    ),
  )
  return fixtures.unhandled
}

function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text())
  })
  page.on("pageerror", (e) => errors.push(String(e)))
  return errors
}

async function openConnectors(page: Page): Promise<Locator> {
  await page.goto(url("/cloudflare/connectors"))
  await page.getByRole("radio", { name: LONG_HOST, exact: true }).click()
  const table = page.getByRole("table", { name: "Connectors" })
  await expect(table).toBeVisible()
  await expect(table.getByRole("cell", { name: LONG_TUNNEL, exact: true })).toBeVisible()
  return table
}

async function openBanner(page: Page): Promise<Locator> {
  await page.goto(url("/"))
  const link = page.getByRole("link", { name: `Open Nginx Proxy Manager on ${LONG_HOST}` })
  await expect(link).toBeVisible()
  return link
}

async function noSidewaysScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true)
}

async function noAxeViolations(page: Page, where: string) {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: { id: string; nodes: { target: string[] }[] }[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
    return result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)
  })
  expect(violations, where).toEqual([])
}

// right edge of el minus the right edge of container, in CSS pixels
async function overhang(el: Locator, container: Locator): Promise<number> {
  const [a, b] = [await el.boundingBox(), await container.boundingBox()]
  expect(a, "named element is rendered").not.toBeNull()
  expect(b, "its container is rendered").not.toBeNull()
  return a!.x + a!.width - (b!.x + b!.width)
}

function banner(page: Page): Locator {
  return page.getByRole("status").filter({ has: page.getByText("Admin interface exposed", { exact: true }) })
}

function hostCard(page: Page): Locator {
  return page
    .locator("div")
    .filter({ has: page.getByText("Host", { exact: true }) })
    .filter({ has: page.getByRole("radiogroup", { name: "Host" }) })
    .last()
}

// The number of lines the text of el occupies.
function lineCount(el: Locator): Promise<number> {
  return el.evaluate((node) => {
    const range = document.createRange()
    range.selectNodeContents(node)
    const tops = new Set(Array.from(range.getClientRects()).filter((r) => r.width > 0).map((r) => Math.round(r.top)))
    return tops.size
  })
}

for (const vp of VIEWPORTS) {
  const tag = `${vp.width}x${vp.height}`
  test.describe(tag, () => {
    test.use({ viewport: vp })

    test(`A21: UI-1 the State badge of every row stays on one line ${tag}`, async ({ page }) => {
      const unhandled = await open(page, "light")
      const table = await openConnectors(page)
      const rows = table.getByRole("row")
      await expect(rows).toHaveCount(CONNECTORS.length + 1)
      for (const state of ["running", "exited", "restarting", "missing"]) {
        const badge = table.getByRole("cell").getByText(state, { exact: true })
        await expect(badge, state).toHaveCount(1)
        expect(await lineCount(badge), `the ${state} badge`).toBe(1)
      }
      expect(unhandled, "API requests without a fixture").toEqual([])
    })

    test(`A22: UI-2 the table keeps 40rem and scrolls inside its wrapper ${tag}`, async ({ page }) => {
      await open(page, "light")
      const table = await openConnectors(page)
      const measured = await table.evaluate((el) => {
        const root = parseFloat(getComputedStyle(document.documentElement).fontSize)
        let wrapper: HTMLElement | null = el.parentElement
        while (wrapper !== null && !["auto", "scroll"].includes(getComputedStyle(wrapper).overflowX)) wrapper = wrapper.parentElement
        const probeWidth = (cell: Element) => {
          const probe = document.createElement("span")
          probe.style.cssText = "display:inline-block;width:10ch;visibility:hidden"
          cell.appendChild(probe)
          const w = probe.getBoundingClientRect().width
          probe.remove()
          return w
        }
        const names = Array.from(el.querySelectorAll("tbody tr")).flatMap((tr) => Array.from(tr.children).slice(0, 2))
        return {
          minWidth: 40 * root,
          tableWidth: el.getBoundingClientRect().width,
          wrapperClient: wrapper?.clientWidth ?? null,
          wrapperScroll: wrapper?.scrollWidth ?? null,
          cells: names.map((c) => ({ text: (c.textContent ?? "").slice(0, 20), width: c.getBoundingClientRect().width, tenChars: probeWidth(c) })),
        }
      })
      expect(measured.tableWidth, "table width").toBeGreaterThanOrEqual(measured.minWidth - 0.5)
      expect(measured.wrapperClient, "the table sits in a scrolling wrapper").not.toBeNull()
      if (measured.wrapperClient! < measured.tableWidth) {
        expect(measured.wrapperScroll!, "a wrapper narrower than the table scrolls").toBeGreaterThan(measured.wrapperClient!)
      }
      await noSidewaysScroll(page)
      for (const c of measured.cells) expect(c.width, `name cell ${c.text}`).toBeGreaterThanOrEqual(c.tenChars - 0.5)
    })

    test(`A23: UI-3 a 63-character host name stays inside the Host card ${tag}`, async ({ page }) => {
      await open(page, "light")
      await page.goto(url("/cloudflare/connectors"))
      const label = page.getByRole("radiogroup", { name: "Host" }).getByText(LONG_HOST, { exact: true })
      await expect(label).toBeVisible()
      expect(await overhang(label, hostCard(page)), "the Host label runs past the card's right edge by").toBeLessThanOrEqual(0.5)
      await noSidewaysScroll(page)
    })

    test(`A24: UI-4 the banner link and description stay inside the banner ${tag}`, async ({ page }) => {
      await open(page, "light")
      const link = await openBanner(page)
      const box = banner(page)
      await expect(box).toBeVisible()
      const description = box.getByText(`The NPM admin interface of ${LONG_HOST} is exposed at https://${LONG_ADMIN}.`)
      await expect(description).toBeVisible()
      expect(await overhang(link, box), "the link runs past the banner by").toBeLessThanOrEqual(0.5)
      expect(await overhang(description, box), "the description runs past the banner by").toBeLessThanOrEqual(0.5)
      const decoration = await link.evaluate((el) => getComputedStyle(el).textDecorationLine)
      expect(decoration, "the link at rest").toContain("underline")
      await noSidewaysScroll(page)
    })

    for (const theme of THEMES) {
      test(`A25: OP-1 the two surfaces are clean ${tag} ${theme}`, async ({ page }) => {
        const errors = watchConsole(page)
        const unhandled = await open(page, theme)
        const table = await openConnectors(page)
        const badges = table.getByRole("cell").getByText(/^(running|exited|restarting|missing)$/)
        await expect(badges).toHaveCount(CONNECTORS.length)
        for (const badge of await badges.all()) expect(await overhang(badge, table), "a State badge past the table").toBeLessThanOrEqual(0.5)
        for (const name of [LONG_TUNNEL, LONG_CONTAINER]) {
          expect(await overhang(table.getByRole("cell", { name, exact: true }), table), `the cell of ${name} past the table`).toBeLessThanOrEqual(0.5)
        }
        const label = page.getByRole("radiogroup", { name: "Host" }).getByText(LONG_HOST, { exact: true })
        expect(await overhang(label, hostCard(page)), "the Host label past the card").toBeLessThanOrEqual(0.5)
        await noSidewaysScroll(page)
        await noAxeViolations(page, `connectors ${tag} ${theme}`)

        const link = await openBanner(page)
        const box = banner(page)
        const description = box.getByText(`The NPM admin interface of ${LONG_HOST} is exposed at https://${LONG_ADMIN}.`)
        expect(await overhang(link, box), "the banner link past the banner").toBeLessThanOrEqual(0.5)
        expect(await overhang(description, box), "the banner description past the banner").toBeLessThanOrEqual(0.5)
        await noSidewaysScroll(page)
        await noAxeViolations(page, `banner ${tag} ${theme}`)
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    }
  })
}
