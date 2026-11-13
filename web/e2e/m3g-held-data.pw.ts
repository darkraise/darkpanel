import { createRequire } from "node:module"
import { expect, test, type Page } from "@playwright/test"
import type { Host, Plan } from "../src/api/types"
import { cfConnector, cfTunnel } from "../src/modules/cloudflare/fixtures"
import { npInstance } from "../src/modules/npm/fixtures"
import { HOST_ID, mockApi } from "./fixtures"

// vitest-axe depends on axe-core, and pnpm does not link it into web/.
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/m3g-held-data"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

const LONG_NAME = `web-${"x".repeat(59)}`
const LONG_TUNNEL = `darkpanel-${"t".repeat(54)}`
const FAILURE = `the controller could not read its database: open /var/lib/darkpanel/controller.db: ${"resource temporarily unavailable ".repeat(4)}(SQLITE_BUSY)`
const OFFLINE_HOST_ID = "01JE00000000000000000000H2"
const EXPOSED = `The NPM admin interface of ${LONG_NAME} is exposed at https://npm-admin.${"e".repeat(40)}.example.com.`
const UNREADABLE = "Could not check whether the NPM admin UI is exposed on: db-1."
const UNCHECKED = "Could not check whether the NPM admin UI is exposed."

function cfHost(id: string, name: string): Host {
  return {
    id, name, isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1,
    os: "debian-12", arch: "amd64", supported: true,
    modules: [{ id: "cloudflare", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
  }
}

const CF_HOSTS = [cfHost(HOST_ID, "web-1"), cfHost("01JE00000000000000000000L1", LONG_NAME)]
const TUNNELS = [
  cfTunnel({ id: "t1", name: "darkpanel-c1" }),
  cfTunnel({ id: "t2", name: LONG_TUNNEL, status: "inactive", connections: [] }),
  cfTunnel({ id: "t3", name: "darkpanel-c3" }),
]
const CONNECTORS = [
  cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" }),
  cfConnector({ tunnel: "t2", container: `darkpanel-cloudflared-${"t".repeat(40)}`, state: "exited" }),
]
const STOP_PLAN: Plan = {
  planId: "plan-cf-stop", hostId: HOST_ID, action: "cloudflare.connector.stop", version: 1, inputHash: "h",
  diff: [{ op: "-", text: "connector darkpanel-c1" }], guards: [], expiresAt: "2026-11-13T13:00:00Z", confirmation: null,
}

const failure = (detail: string) => ({ status: 500, contentType: "application/json", body: JSON.stringify({ type: "about:blank", title: detail, status: 500, detail, code: "internal" }) })
const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })

// Each read answers from its entry: "ok" with the held data, "fail" with a
// 500. reset() makes the event stream send one reset event, which refetches
// every open query as the controller's reset does.
interface Reads {
  hosts: "ok" | "fail"
  connectors: "ok" | "fail"
  fleet: "ok" | "fail"
  reset: () => void
}

// cloudflare answers the host list with two hosts that run the Cloudflare
// module; otherwise the shared fixtures answer it.
async function open(page: Page, theme: (typeof THEMES)[number], cloudflare: boolean): Promise<{ reads: Reads; unhandled: string[] }> {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true })
  let resets = 0
  const reads: Reads = { hosts: "ok", connectors: "ok", fleet: "ok", reset: () => void resets++ }
  await page.route(/\/api\/v1\/events$/, async (route) => {
    const body = resets > 0 ? "retry: 200\nevent: reset\ndata: {}\n\n" : "retry: 200\n\n"
    if (resets > 0) resets--
    await route.fulfill({ status: 200, contentType: "text/event-stream", body })
  })
  await page.route(/\/actions\/core\.host\.list\/v1\/read$/, async (route) => {
    if (reads.hosts === "fail") return route.fulfill(failure(FAILURE))
    return cloudflare ? route.fulfill(ok({ hosts: CF_HOSTS })) : route.fallback()
  })
  await page.route(/\/actions\/cloudflare\.tunnel\.list\/v1\/read$/, (route) => route.fulfill(ok({ tunnels: TUNNELS })))
  await page.route(/\/actions\/cloudflare\.connector\.list\/v1\/read$/, (route) =>
    route.fulfill(reads.connectors === "ok" ? ok({ connectors: CONNECTORS }) : failure(FAILURE)),
  )
  await page.route(/\/actions\/cloudflare\.connector\.stop\/v1\/plan$/, (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(STOP_PLAN) }),
  )
  await page.route(/\/actions\/npm\.instance\.list\/v1\/read$/, (route) =>
    route.fulfill(
      reads.fleet === "ok"
        ? ok({
            instances: [{ hostId: HOST_ID, hostName: LONG_NAME, status: npInstance({ deployed: true, adminExposure: { hostname: `npm-admin.${"e".repeat(40)}.example.com`, proxyHostId: 4, certificateId: 5 } }) }],
            unreadable: [OFFLINE_HOST_ID],
          })
        : failure(FAILURE),
    ),
  )
  return { reads, unhandled: fixtures.unhandled }
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

// A failed read logs the browser's own "Failed to load resource" line; any
// other console error is a defect.
function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text())
  })
  page.on("pageerror", (e) => errors.push(String(e)))
  return errors
}

// The app retries a 5xx read twice, about three seconds in all, before the
// query turns to error.
const SETTLE = { timeout: 15_000 }

async function openConnectors(page: Page) {
  await page.goto(url("/cloudflare/connectors"))
  await page.getByRole("radio", { name: "web-1", exact: true }).click()
  const card = page.locator("div").filter({ has: page.getByText("Connectors on web-1", { exact: true }) }).filter({ has: page.getByRole("table", { name: "Connectors" }) }).last()
  await expect(card.getByRole("table", { name: "Connectors" })).toBeVisible()
  return card
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      test(`UI-1: connectors refetch fails ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const { reads, unhandled } = await open(page, theme, true)
        const card = await openConnectors(page)
        reads.connectors = "fail"
        reads.reset()
        const alert = card.getByRole("alert")
        await expect(alert).toHaveText(FAILURE, SETTLE)
        const table = card.getByRole("table", { name: "Connectors" })
        await expect(table.getByRole("row")).toHaveCount(3)
        await expect(table.getByRole("cell", { name: LONG_TUNNEL, exact: true })).toBeVisible()
        expect((await alert.boundingBox())!.y, "the alert sits above the table").toBeLessThan((await table.boundingBox())!.y)
        const form = page.getByRole("form", { name: "Deploy a connector" })
        await expect(form.getByRole("radio", { name: "darkpanel-c3", exact: true })).toBeVisible()
        await expect(form.getByRole("radio", { name: "darkpanel-c1", exact: true })).toHaveCount(0)
        await expect(form.getByRole("radio", { name: LONG_TUNNEL, exact: true })).toHaveCount(0)
        await form.screenshot({ path: `${EVIDENCE}/UI-1-${tag}-deploy-form.png` })
        await expectNoAxeViolations(page, "connectors refetch failed")
        await expectNoSidewaysScroll(page)
        await alert.scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${EVIDENCE}/UI-1-${tag}-refetch-failed.png` })
        reads.connectors = "ok"
        reads.reset()
        await expect(alert).toHaveCount(0, SETTLE)
        await expect(table.getByRole("row")).toHaveCount(3)
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UI-2, UX-1: hosts refetch fails under an open dialog ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const { reads, unhandled } = await open(page, theme, true)
        await openConnectors(page)
        const stop = page.getByRole("button", { name: "Stop the connector of darkpanel-c1" })
        await stop.focus()
        await page.keyboard.press("Enter")
        const dialog = page.getByRole("dialog", { name: "Stop the connector of darkpanel-c1" })
        await expect(dialog.getByLabel("Planned changes")).toBeVisible()
        await dialog.evaluate((el) => el.setAttribute("data-m3g-marker", ""))
        reads.hosts = "fail"
        reads.reset()
        // The open dialog hides the page from the accessibility tree, so the alert is found by its text.
        await expect(page.locator('[role="alert"]', { hasText: FAILURE })).toBeAttached(SETTLE)
        await expect(dialog, "the dialog was not remounted").toHaveAttribute("data-m3g-marker", "")
        await page.waitForFunction(() => document.getAnimations().length === 0)
        await page.screenshot({ path: `${EVIDENCE}/UX-1-${tag}-dialog-kept.png` })
        await page.keyboard.press("Escape")
        await expect(dialog).toHaveCount(0)
        await expect(stop, "focus returns to the Stop button, which was not remounted").toBeFocused()
        const alert = page.getByRole("alert").filter({ hasText: FAILURE })
        await expect(alert).toBeVisible()
        await expect(page.getByRole("radiogroup", { name: "Host" })).toBeVisible()
        await expect(page.getByRole("radio", { name: LONG_NAME })).toBeVisible()
        await expect(page.getByText("Connectors on web-1", { exact: true })).toBeVisible()
        expect((await alert.boundingBox())!.y, "the alert sits above the host choice").toBeLessThan((await page.getByRole("radiogroup", { name: "Host" }).boundingBox())!.y)
        await expectNoAxeViolations(page, "hosts refetch failed")
        await expectNoSidewaysScroll(page)
        await alert.scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${EVIDENCE}/UI-2-${tag}-refetch-failed.png`, fullPage: true })
        reads.hosts = "ok"
        reads.reset()
        await expect(alert).toHaveCount(0, SETTLE)
        await expect(page.getByText("Connectors on web-1", { exact: true })).toBeVisible()
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UI-3: banner refetch fails ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const { reads, unhandled } = await open(page, theme, false)
        await page.goto(url("/"))
        await expect(page.getByText(EXPOSED)).toBeVisible()
        await expect(page.getByText(UNREADABLE)).toBeVisible()
        reads.fleet = "fail"
        reads.reset()
        await expect(page.getByText(UNCHECKED, { exact: true })).toBeVisible(SETTLE)
        await expect(page.getByText(EXPOSED)).toBeVisible()
        await expect(page.getByRole("link", { name: `Open Nginx Proxy Manager on ${LONG_NAME}` })).toHaveAttribute("href", `/hosts/${HOST_ID}/npm`)
        await expect(page.getByText(UNREADABLE)).toBeVisible()
        await expectNoAxeViolations(page, "banner refetch failed")
        await expectNoSidewaysScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-3-${tag}-refetch-failed.png` })
        reads.fleet = "ok"
        reads.reset()
        await expect(page.getByText(UNCHECKED, { exact: true })).toHaveCount(0, SETTLE)
        await expect(page.getByText(EXPOSED)).toBeVisible()
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UI-4: first reads fail ${tag}`, async ({ page }) => {
        const { reads } = await open(page, theme, true)
        reads.connectors = "fail"
        reads.fleet = "fail"
        await page.goto(url("/cloudflare/connectors"))
        await page.getByRole("radio", { name: "web-1", exact: true }).click()
        await expect(page.getByRole("alert").filter({ hasText: FAILURE })).toBeVisible(SETTLE)
        await expect(page.getByRole("table", { name: "Connectors" })).toHaveCount(0)
        await expect(page.getByText(UNCHECKED, { exact: true })).toBeVisible(SETTLE)
        await expect(page.getByText(EXPOSED)).toHaveCount(0)
        await expectNoSidewaysScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-4-${tag}-connectors-first-read.png`, fullPage: true })
        reads.hosts = "fail"
        await page.goto(url("/cloudflare/connectors"))
        await expect(page.getByRole("alert").filter({ hasText: FAILURE })).toBeVisible(SETTLE)
        await expect(page.getByRole("radiogroup", { name: "Host" })).toHaveCount(0)
        await page.screenshot({ path: `${EVIDENCE}/UI-4-${tag}-hosts-first-read.png` })
      })

      test(`UI-5: hosts refetch fails with no Cloudflare host ${tag}`, async ({ page }) => {
        const errors = watchConsole(page)
        const { reads, unhandled } = await open(page, theme, false)
        await page.goto(url("/cloudflare/connectors"))
        const empty = page.getByText("No host runs the Cloudflare module")
        await expect(empty).toBeVisible()
        reads.hosts = "fail"
        reads.reset()
        const alert = page.getByRole("alert").filter({ hasText: FAILURE })
        await expect(alert).toBeVisible(SETTLE)
        await expect(empty).toBeVisible()
        expect((await alert.boundingBox())!.y, "the alert sits above the empty state").toBeLessThan((await empty.boundingBox())!.y)
        await expectNoAxeViolations(page, "hosts refetch failed with no Cloudflare host")
        await expectNoSidewaysScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-5-${tag}-refetch-failed.png`, fullPage: true })
        reads.hosts = "ok"
        reads.reset()
        await expect(alert).toHaveCount(0, SETTLE)
        await expect(empty).toBeVisible()
        expect(errors, "console errors").toEqual([])
        expect(unhandled, "API requests without a fixture").toEqual([])
      })
    })
  }
}
