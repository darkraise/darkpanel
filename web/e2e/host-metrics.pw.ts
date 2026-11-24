import { expect, test } from "@playwright/test"
import type { Host, MetricsHistory, MetricsPoint } from "../src/api/types"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`
const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })
const GIB = 1024 ** 3

const HOST: Host = {
  id: HOST_ID, name: "web-1", scope: "admin", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-24T09:00:00Z", agentVersion: "1.0.0",
  protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true,
  modules: [{ id: "metrics", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
}

// history is n points a step apart, ending at 2026-11-24 09:00 UTC, with 3
// mounts and 2 interfaces: the 90-day range is 2160 hour points.
function history(range: MetricsHistory["range"], n: number, stepMs: number): MetricsHistory {
  const end = Date.UTC(2026, 10, 24, 9)
  const points: MetricsPoint[] = Array.from({ length: n }, (_, i) => {
    const w = Math.sin(i / 9)
    return {
      ts: new Date(end - (n - 1 - i) * stepMs).toISOString(), cpu: 30 + 20 * w, memUsed: Math.round((5 + w) * GIB), memTotal: 8 * GIB, swapUsed: 0, swapTotal: 2 * GIB,
      load1: 1.5 + w, load5: 1.4, load15: 1.3,
      fs: ["/", "/data", "/var/lib/docker"].map((mount, m) => ({ mount, used: Math.round((40 + 10 * m + 5 * w) * GIB), total: 100 * GIB })),
      net: ["eth0", "tailscale0"].map((iface, k) => ({ iface, rxBps: (4 - k) * 1e6 * (1 + w), txBps: (2 - k / 2) * 1e6 * (1 + w) })),
    }
  })
  return { hostId: HOST_ID, range, resolution: n > 7 * 1440 || stepMs === 3_600_000 ? "1h" : "1m", points, status: { lastOk: "2026-11-24T09:00:00Z", lastErrorCode: null, lastErrorAt: null, clockSkewS: 0 } }
}

test("A45 renders 90 days within 1 s", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  await page.route(/\/actions\/core\.host\.list\/v1\/read$/, (route) => route.fulfill(ok({ hosts: [HOST] })))
  let answered = 0
  await page.route(/\/actions\/metrics\.history\.get\/v1\/read$/, async (route) => {
    const range = (route.request().postDataJSON() as { input: { range: MetricsHistory["range"] } }).input.range
    const body = range === "90d" ? history("90d", 90 * 24, 3_600_000) : history(range, 60, 60_000)
    if (range === "90d") answered = Date.now()
    await route.fulfill(ok(body))
  })

  await page.goto(url(`/hosts/${HOST_ID}`))
  await expect(page.getByRole("img", { name: /^CPU \(%\), last hour\./ })).toBeVisible()
  await page.getByRole("radiogroup", { name: "Range" }).getByRole("radio", { name: "90d" }).click()
  await page.waitForFunction(
    () => [...document.querySelectorAll('[role="img"][aria-label*="last 90 days"]')].filter((el) => el.querySelector("path.recharts-curve") !== null).length === 5,
    undefined,
    { polling: "raf" },
  )
  const elapsed = Date.now() - answered
  expect(answered).toBeGreaterThan(0)
  expect(elapsed, `five charts drew ${elapsed} ms after the response`).toBeLessThan(1000)
})
