import type { ReactElement } from "react"
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test } from "vitest"
import type { AlertRule, FiredAlert, HostGroup, MetricsPoint, NotifyChannel } from "@/api/types"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { makeHost, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { AlertsPage } from "./alerts"
import { GroupsPage } from "./groups"
import { MetricsSection } from "./host-metrics"
import { NotificationsPage } from "./notifications"
import { SignInSettingsPage } from "./sign-in-settings"
import { UsersPage } from "./users"

const read = (action: string) => `POST /api/v1/actions/${action}/v1/read`
const out = (output: unknown) => ({ body: { output } })

const groups: HostGroup[] = [{ id: "g1", name: "web", hostIds: ["h1"], hostCount: 1, grantCount: 1, createdAt: "2026-11-20T08:00:00Z" }]
const channel: NotifyChannel = {
  id: "c1", kind: "ntfy", name: "ops-phone", minSeverity: "warning", kinds: [], groupIds: [], enabled: true, createdAt: "2026-11-20T08:00:00Z",
  ntfy: { server: "https://ntfy.sh", topic: "ops", tokenSet: false }, lastDelivery: { state: "failed", at: "2026-11-24T09:00:00Z", error: "403" },
}
const alert: FiredAlert = { ruleId: "r1", ruleName: "Disk almost full", metric: "disk", severity: "critical", hostId: "h1", hostName: "web-1", subject: "/", state: "firing", since: "2026-11-24T08:00:00Z", value: 93 }
const rule: AlertRule = {
  id: "r1", name: "Disk almost full", metric: "disk", comparator: ">", threshold: 90, durationMin: 10, severity: "warning", target: "*", targetName: "", enabled: true,
  createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z",
}
const point = (m: number): MetricsPoint => ({
  ts: new Date(Date.UTC(2026, 10, 24, 9, m)).toISOString(), cpu: 20, memUsed: 1, memTotal: 2, swapUsed: 0, swapTotal: 0, load1: 1, load5: 1, load15: 1,
  fs: [{ mount: "/", used: 1, total: 2 }], net: [{ iface: "eth0", rxBps: 1, txBps: 1 }],
})
const metricsHost = makeHost({ id: "h1", modules: [{ id: "metrics", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })

const routes: Record<string, StubHandler> = {
  [read("core.user.directory")]: out({
    users: [{ id: "u1", username: "admin", role: "admin", state: "active", createdAt: "2026-11-20T08:00:00Z", disabled: false, grants: [], email: "", tailnetLogin: "", signIn: ["password"], inviteExpiresAt: null }],
  }),
  [read("core.group.list")]: out({ groups }),
  [read("core.host.list")]: out({ hosts: [makeHost({ id: "h1" })] }),
  [read("notify.channel.list")]: out({ channels: [channel] }),
  [read("metrics.alert.list")]: out({ alerts: [alert] }),
  [read("metrics.alert.list-rules")]: out({ rules: [rule] }),
  [read("auth.settings.get")]: out({ oidc: null, tailscale: { enabled: false, requireTotp: false } }),
  [read("metrics.history.get")]: out({ hostId: "h1", range: "1h", resolution: "1m", points: [point(0), point(1)], status: null }),
  "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "" } },
}

interface PageCase {
  name: string
  page: ReactElement
  ready: string
  trigger: string | null
}

const pages: PageCase[] = [
  { name: "users", page: <UsersPage />, ready: "admin", trigger: "Invite user" },
  { name: "host groups", page: <GroupsPage />, ready: "web", trigger: "Create group" },
  { name: "sign-in settings", page: <SignInSettingsPage />, ready: "OIDC", trigger: null },
  { name: "notifications", page: <NotificationsPage />, ready: "ops-phone", trigger: "Add channel" },
  { name: "alerts", page: <AlertsPage />, ready: "Disk almost full on /", trigger: "New rule" },
  { name: "host metrics", page: <MetricsSection host={metricsHost} />, ready: "CPU (%)", trigger: null },
]

const THEMES = ["light", "dark"] as const

afterEach(() => {
  document.documentElement.classList.remove("dark")
})

describe("A44 axe", () => {
  test.each(pages.flatMap((p) => THEMES.map((theme) => ({ ...p, theme }))))("$name in $theme has no violation", async ({ page, ready, theme }) => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    stubFetch(routes)
    const { container } = renderWithProviders(page)
    expect((await screen.findAllByText(ready)).length).toBeGreaterThan(0)
    await expectNoAxeViolations(container)
  })
})

describe("A42 dialogs", () => {
  test.each(pages.filter((p) => p.trigger !== null))("$name: $trigger traps focus, and Escape returns it to the trigger", async ({ page, ready, trigger }) => {
    stubFetch(routes)
    renderWithProviders(page)
    await screen.findAllByText(ready)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: trigger! }))
    const dialog = await screen.findByRole("dialog")
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
    for (let i = 0; i < 25; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement), `tab ${i + 1} stays in the dialog`).toBe(true)
    }
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(focusedName()).toBe(trigger)
  })
})

describe("OP-3 selected segment", () => {
  const segmentCases = [
    { name: "host metrics range", page: <MetricsSection host={metricsHost} />, ready: "CPU (%)", trigger: null },
    ...pages.filter((p) => p.name === "users" || p.name === "alerts"),
  ]
  test.each(segmentCases)("$name marks the selected option with an indicator, not colour alone", async ({ page, ready, trigger }) => {
    stubFetch(routes)
    renderWithProviders(page)
    await screen.findAllByText(ready)
    if (trigger !== null) await userEvent.setup().click(screen.getByRole("button", { name: trigger }))
    const root = trigger === null ? document.body : await screen.findByRole("dialog")
    const groups = await waitFor(() => {
      const found = root.querySelectorAll(".dr-segment-group")
      expect(found.length).toBeGreaterThan(0)
      return Array.from(found)
    })
    for (const group of groups) {
      const name = group.getAttribute("aria-label") ?? group.getAttribute("aria-labelledby")
      await waitFor(() => expect(group.querySelector('.dr-segment-group-indicator[data-active="true"]'), `${name} has an active indicator`).not.toBeNull())
    }
  })
})

describe("UX-11 keyboard", () => {
  test("charts take focus and are named by their summary", async () => {
    stubFetch(routes)
    renderWithProviders(<MetricsSection host={metricsHost} />)
    const chart = await screen.findByRole("img", { name: /^CPU \(%\), last hour\. CPU: latest 20\.0 %/ })
    expect(chart.getAttribute("tabindex")).toBe("0")
  })
})
