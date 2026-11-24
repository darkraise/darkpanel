import { expect, test, type Page } from "@playwright/test"
import type { AlertRule, FiredAlert, Host, HostGroup, MetricsPoint, NotifyChannel } from "../src/api/types"
import { HOST_ID, mockApi } from "./fixtures"

// UX-10 at every viewport and theme of the profile, on the worst-case data of
// spec §6, and the M10 screenshots for the proof report.
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/m10"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`
const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })
const GIB = 1024 ** 3

const LONG = `ops-${"g".repeat(44)}`
const HOST: Host = {
  id: HOST_ID, name: "web-1", scope: "admin", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-24T09:00:00Z", agentVersion: "1.0.0",
  protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true,
  modules: [{ id: "metrics", version: 1, enabled: true, available: true, reason: null, checkedAt: null }], ingestion: null,
}
const groups: HostGroup[] = Array.from({ length: 32 }, (_, i) => ({ id: `g${i}`, name: i === 0 ? LONG : `group-${i}`, hostIds: [HOST_ID], hostCount: i, grantCount: i % 3, createdAt: "2026-11-20T08:00:00Z" }))
const users = Array.from({ length: 50 }, (_, i) => ({
  id: `u${i}`, username: i === 0 ? "admin" : `user-${String(i).padStart(2, "0")}`, role: i === 0 ? "admin" : "member", state: "active", createdAt: "2026-11-20T08:00:00Z",
  disabled: false, grants: i === 0 ? [] : [{ role: i % 2 === 0 ? "viewer" : "operator", target: "g0" }], email: "", tailnetLogin: "", signIn: ["password"],
}))
const channels: NotifyChannel[] = [
  { id: "c1", kind: "ntfy", name: "ops-phone", minSeverity: "warning", kinds: [], groupIds: [], enabled: true, createdAt: "2026-11-20T08:00:00Z", ntfy: { server: "https://ntfy.sh", topic: "ops", tokenSet: true }, lastDelivery: { state: "sent", at: "2026-11-24T09:12:00Z" } },
  { id: "c2", kind: "telegram", name: "team-chat", minSeverity: "critical", kinds: ["alert.firing", "alert.resolved"], groupIds: ["g0"], enabled: true, createdAt: "2026-11-20T08:00:00Z", telegram: { chatId: "-100123" }, lastDelivery: { state: "failed", at: "2026-11-24T09:00:00Z", error: "403" } },
  { id: "c3", kind: "email", name: "oncall", minSeverity: "info", kinds: [], groupIds: [], enabled: false, createdAt: "2026-11-20T08:00:00Z", email: { host: "smtp.example.com", port: 587, security: "starttls", username: "dp", passwordSet: true, from: "dp@example.com", recipients: ["a@example.com"] }, lastDelivery: null },
]
const alerts: FiredAlert[] = Array.from({ length: 20 }, (_, i) => ({
  ruleId: "r2", ruleName: i % 4 === 0 ? "Host offline" : "Disk almost full", metric: i % 4 === 0 ? "offline" : "disk", severity: i % 4 === 0 ? "critical" : "warning",
  hostId: HOST_ID, hostName: `web-${String(i).padStart(3, "0")}`, subject: i % 4 === 0 ? "" : `/srv/data-${i}`, state: "firing", since: "2026-11-24T08:00:00Z", value: i % 4 === 0 ? 12 : 90 + (i % 9),
}))
const rules: AlertRule[] = [
  { id: "r1", name: "Host offline", metric: "offline", comparator: ">", threshold: 0, durationMin: 5, severity: "critical", target: "*", targetName: "", enabled: true, createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z" },
  { id: "r2", name: "Disk almost full", metric: "disk", comparator: ">", threshold: 90, durationMin: 10, severity: "warning", target: "g0", targetName: LONG, enabled: false, createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z" },
]
const points: MetricsPoint[] = Array.from({ length: 60 }, (_, i) => ({
  ts: new Date(Date.UTC(2026, 10, 24, 8, i)).toISOString(), cpu: 30 + 15 * Math.sin(i / 7), memUsed: 5 * GIB, memTotal: 8 * GIB, swapUsed: 0, swapTotal: 0, load1: 1.2, load5: 1, load15: 1,
  fs: Array.from({ length: 16 }, (_, m) => ({ mount: m === 0 ? "/" : `/srv/data-${m}`, used: (40 + m * 3) * GIB, total: 100 * GIB })),
  net: [{ iface: "eth0", rxBps: 3e6, txBps: 1e6 }],
})).filter((_, i) => i < 38 || i > 41)

const PAGES = [
  { id: "users", path: "/settings/users", table: "users" },
  { id: "groups", path: "/settings/groups", table: "groups" },
  { id: "sign-in", path: "/settings/sign-in", table: null },
  { id: "notifications", path: "/settings/notifications", table: null },
  { id: "alerts", path: "/alerts", table: "Firing alerts" },
  { id: "metrics", path: `/hosts/${HOST_ID}`, table: null },
] as const

async function open(page: Page, theme: (typeof THEMES)[number]) {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  const reads: Record<string, unknown> = {
    "core.host.list": { hosts: [HOST] },
    "core.user.directory": { users },
    "core.group.list": { groups },
    "notify.channel.list": { channels },
    "metrics.alert.list": { alerts },
    "metrics.alert.list-rules": { rules },
    "auth.settings.get": { oidc: { issuer: "https://id.example.com", clientId: "darkpanel", displayName: "Acme SSO", redirectBase: "https://panel.example.com", requireTotp: true, clientSecretSet: true }, tailscale: { enabled: true, requireTotp: false } },
    "metrics.history.get": { hostId: HOST_ID, range: "1h", resolution: "1m", points, status: { lastOk: "2026-11-24T08:59:00Z", lastErrorCode: null, lastErrorAt: null, clockSkewS: 0 } },
  }
  for (const [name, output] of Object.entries(reads)) {
    await page.route(new RegExp(`/actions/${name.replace(/\./g, "\\.")}/v1/read$`), (route) => route.fulfill(ok(output)))
  }
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    test(`UX-10 M10 pages at ${vp.width} in ${theme}`, async ({ page }) => {
      await page.setViewportSize(vp)
      await open(page, theme)
      for (const p of PAGES) {
        await page.goto(url(p.path))
        await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible()
        if (p.table !== null) {
          const table = page.getByRole("table", { name: p.table }).or(page.locator("table").first())
          const row = table.first().locator("tbody tr").first()
          await expect(row).toBeVisible()
          const display = await row.evaluate((el) => getComputedStyle(el).display)
          const head = await table.first().locator("thead").evaluate((el) => el.getBoundingClientRect().height)
          if (vp.width === 375) {
            expect(display, `${p.id} rows stack as cards`).not.toBe("table-row")
            expect(head, `${p.id} header row is hidden`).toBeLessThanOrEqual(1)
          }
          else expect(display, `${p.id} rows stay table rows`).toBe("table-row")
        }
        if (p.id === "metrics") {
          const charts = page.locator('[role="img"][aria-label*="last hour"]')
          await expect(charts).toHaveCount(5)
          const tops = await charts.evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().top)))
          const perRow = 5 / new Set(tops).size
          expect(perRow, "charts per row").toBe(vp.width === 1440 ? 5 / 3 : 1)
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${p.id} has no sideways scroll`).toBe(true)
        await page.screenshot({ path: `${EVIDENCE}/${p.id}-${vp.width}-${theme}.png`, fullPage: true })
      }
    })
  }
}

const problem = (status: number, code: string, detail: string) => ({
  status, contentType: "application/json", body: JSON.stringify({ type: "about:blank", title: code, status, detail, code }),
})

// The signed-out views of UI-7 and UX-2/UX-3, and the M10 dialogs, for the
// proof report: each must also keep the page from scrolling sideways.
for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    test(`UI-7 sign-in views at ${vp.width} in ${theme}`, async ({ page }) => {
      await page.setViewportSize(vp)
      await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
      await mockApi(page, { signedIn: false })
      await page.route(/\/api\/v1\/auth\/methods$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ oidc: { displayName: "Acme SSO" }, tailscale: true }) }))
      await page.route(/\/api\/v1\/auth\/tailscale$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ username: "mia", loginName: "mia@github", nodeName: "laptop" }) }))
      await page.route(/\/api\/v1\/enroll\/begin$/, (route) => route.fulfill(problem(410, "auth.enrollment_expired", "This link has expired.")))
      await page.goto(url("/login"))
      await expect(page.getByRole("link", { name: "Sign in with Acme SSO" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Continue as mia (mia@github, from laptop)" })).toBeVisible()
      await page.screenshot({ path: `${EVIDENCE}/login-${vp.width}-${theme}.png`, fullPage: true })
      await page.goto(url("/?signin=no_linked_user"))
      await expect(page.getByText("No Darkpanel user is linked to this account. Ask an admin to add you.")).toBeVisible()
      await page.screenshot({ path: `${EVIDENCE}/login-${vp.width}-${theme}-no-linked-user.png`, fullPage: true })
      await page.goto(url("/?signin=totp"))
      await expect(page.getByLabel("Authentication code")).toBeVisible()
      await page.screenshot({ path: `${EVIDENCE}/login-${vp.width}-${theme}-code-step.png`, fullPage: true })
      await page.goto(url("/enroll#token-1"))
      await expect(page.getByText("This link has expired. Ask an admin for a new one.")).toBeVisible()
      await page.screenshot({ path: `${EVIDENCE}/enroll-${vp.width}-${theme}-expired.png`, fullPage: true })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    })

    test(`M10 dialogs at ${vp.width} in ${theme}`, async ({ page }) => {
      await page.setViewportSize(vp)
      await open(page, theme)
      const shots: [string, string, (p: Page) => Promise<void>][] = [
        ["users", "/settings/users", (p) => p.getByRole("button", { name: "Invite user" }).click()],
        ["groups", "/settings/groups", async (p) => {
          await p.getByRole("button", { name: `Actions for ${LONG}` }).click()
          await p.getByRole("menuitem", { name: "Edit members" }).click()
        }],
        ["notifications", "/settings/notifications", async (p) => {
          await p.getByRole("button", { name: "Add channel" }).click()
          await p.getByRole("dialog").getByRole("button", { name: "Email" }).click()
        }],
        ["alerts", "/alerts", (p) => p.getByRole("button", { name: "New rule" }).click()],
      ]
      for (const [id, path, act] of shots) {
        await page.goto(url(path))
        await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible()
        await act(page)
        await expect(page.getByRole("dialog")).toBeVisible()
        await page.screenshot({ path: `${EVIDENCE}/${id}-${vp.width}-${theme}-dialog.png` })
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${id} dialog has no sideways scroll`).toBe(true)
      }
    })
  }
}
