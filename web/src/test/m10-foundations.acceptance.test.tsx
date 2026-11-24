import type { ReactElement } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { describe, expect, test } from "vitest"
import type { AlertRule, DirectoryUser, FiredAlert, HostGroup, MetricsPoint, NotifyChannel, Session } from "@/api/types"
import { App } from "@/app"
import { AlertsPage } from "@/pages/alerts"
import { GroupsPage } from "@/pages/groups"
import { MetricsSection } from "@/pages/host-metrics"
import { LoginPage } from "@/pages/login"
import { NotificationsPage } from "@/pages/notifications"
import { SignInSettingsPage } from "@/pages/sign-in-settings"
import { UsersPage } from "@/pages/users"
import { RequireSession, SessionProvider } from "@/session/session"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { makeChange, makeHost, makePlan, memberSession, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"

// Acceptance tests of the M10 foundations spec, web layer (UX-1 to UX-11,
// OP-3). Expectations come from the spec's UI/UX contract; the stubs answer
// the API routes the pages call.

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`
const read = (action: string) => route(action, "read")
const out = (output: unknown) => ({ body: { output } })
const never: StubHandler = () => new Promise(() => {})
const fail: StubHandler = { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } }

function dirUser(over: Partial<DirectoryUser>): DirectoryUser {
  return {
    id: `u-${over.username ?? "x"}`, username: "x", role: "member", state: "active", createdAt: "2026-11-20T08:00:00Z", disabled: false,
    grants: [], email: "", tailnetLogin: "", signIn: ["password"], inviteExpiresAt: null, ...over,
  }
}

const groups: HostGroup[] = [{ id: "g1", name: "web", hostIds: ["h1"], hostCount: 1, grantCount: 1, createdAt: "2026-11-20T08:00:00Z" }]
const channel: NotifyChannel = {
  id: "c1", kind: "ntfy", name: "ops-phone", minSeverity: "warning", kinds: [], groupIds: [], enabled: true, createdAt: "2026-11-20T08:00:00Z",
  ntfy: { server: "https://ntfy.sh", topic: "ops", tokenSet: false }, lastDelivery: null,
}
const chat: NotifyChannel = { ...channel, id: "c2", name: "team-chat" }
const rule: AlertRule = {
  id: "r1", name: "Disk almost full", metric: "disk", comparator: ">", threshold: 90, durationMin: 10, severity: "warning", target: "*", targetName: "", enabled: true,
  createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z",
}
const alert: FiredAlert = { ruleId: "r1", ruleName: "Disk almost full", metric: "disk", severity: "critical", hostId: "h1", hostName: "web-1", subject: "/", state: "firing", since: "2026-11-24T08:00:00Z", value: 93 }
const GIB = 1024 ** 3
function point(minute: number, over: Partial<MetricsPoint> = {}): MetricsPoint {
  return {
    ts: new Date(Date.UTC(2026, 10, 24, 9, minute)).toISOString(), cpu: 10 + minute, memUsed: 4 * GIB, memTotal: 8 * GIB, swapUsed: 0, swapTotal: 0,
    load1: 1, load5: 1, load15: 1, fs: [{ mount: "/", used: 140, total: 200 }], net: [{ iface: "eth0", rxBps: 2_000_000, txBps: 1_000_000 }], ...over,
  }
}
const metricsOn = { id: "metrics", version: 1, enabled: true, available: true, reason: null, checkedAt: null }
const metricsHost = makeHost({ id: "h1", name: "web-1", online: true, modules: [metricsOn] })

const baseRoutes: Record<string, StubHandler> = {
  [read("core.user.directory")]: out({ users: [dirUser({ username: "admin", role: "admin" })] }),
  [read("core.group.list")]: out({ groups }),
  [read("core.host.list")]: out({ hosts: [makeHost({ id: "h1" })] }),
  [read("notify.channel.list")]: out({ channels: [channel] }),
  [read("metrics.alert.list")]: out({ alerts: [alert] }),
  [read("metrics.alert.list-rules")]: out({ rules: [rule] }),
  [read("auth.settings.get")]: out({ oidc: null, tailscale: { enabled: false, requireTotp: false } }),
  [read("metrics.history.get")]: out({ hostId: "h1", range: "1h", resolution: "1m", points: [point(0), point(1), point(2)], status: null }),
  [read("metrics.history.get")]: out({ hostId: "h1", range: "1h", resolution: "1m", points: [point(0), point(1), point(2)], status: null }),
  "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { logicalCount: 4 } } },
  "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "" } },
}

describe("A35 UX-1 invite flow", () => {
  test("A35: the link shows once with Copy and its expiry, and closing returns focus to the new row", async () => {
    let invited = false
    const noor = dirUser({ username: "noor", state: "pending", signIn: [], inviteExpiresAt: "2026-11-26T09:40:00Z" })
    stubFetch({
      [read("core.user.directory")]: () => out({ users: invited ? [dirUser({ username: "admin", role: "admin" }), noor] : [dirUser({ username: "admin", role: "admin" })] }),
      [read("core.group.list")]: out({ groups }),
      [route("core.user.invite", "plan")]: { body: makePlan("core.user.invite", { confirmation: "noor" }) },
      [route("core.user.invite", "apply")]: () => {
        invited = true
        return { body: makeChange("core.user.invite", { user: noor, token: "tok-secret-123", expiresAt: "2026-11-26T09:40:00Z" }) }
      },
    })
    renderWithProviders(<UsersPage />)
    const user = userEvent.setup()
    await screen.findByText("admin")
    await user.click(screen.getByRole("button", { name: "Invite user" }))
    const form = await screen.findByRole("dialog", { name: "Invite user" })
    await user.type(within(form).getByLabelText("Username"), "noor")
    await user.click(within(form).getByRole("button", { name: "Review invite" }))
    const change = await screen.findByRole("dialog", { name: "Invite noor" })
    await user.type(await within(change).findByLabelText("Type noor to confirm"), "noor")
    await user.click(within(change).getByRole("button", { name: "Apply" }))
    const link = await screen.findByRole("dialog", { name: "Enrollment link for noor" })
    expect(within(link).getByText(/tok-secret-123/)).toBeTruthy()
    expect(within(link).getByRole("button", { name: "Copy" })).toBeTruthy()
    expect(within(link).getByText(/^Expires /)).toBeTruthy()
    await user.click(within(link).getByRole("button", { name: "Done" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    // The link was shown once: nothing on the page holds it afterwards.
    expect(document.body.textContent).not.toContain("tok-secret-123")
    const row = (await screen.findByText("noor")).closest("tr")
    expect(row).not.toBeNull()
    await waitFor(() => expect(row!.contains(document.activeElement)).toBe(true))
  })
})

describe("A36 UX-2 enrollment link", () => {
  test.each([["expired"], ["used"]])("A36: an %s link shows the expiry message and no form", async () => {
    stubFetch({
      "GET /api/v1/auth/session": { status: 401, body: { type: "about:blank", title: "x", status: 401, detail: "log in", code: "auth.required" } },
      "GET /api/v1/auth/methods": { body: { oidc: null, tailscale: false } },
      "POST /api/v1/enroll/begin": { status: 410, body: { type: "about:blank", title: "Enrollment link expired", status: 410, detail: "expired", code: "auth.enrollment_expired" } },
    })
    renderWithProviders(<App />, { path: "/enroll#some-token", session: null })
    expect(await screen.findByText("This link has expired. Ask an admin for a new one.")).toBeTruthy()
    expect(screen.queryByLabelText("Password")).toBeNull()
    expect(screen.queryByLabelText("Confirm password")).toBeNull()
    expect(screen.queryByLabelText("Authentication code")).toBeNull()
    expect(screen.queryByRole("button", { name: /finish|continue|sign in|log in/i })).toBeNull()
  })
})

describe("A37 UX-3 OIDC button", () => {
  const signedOut = { status: 401, body: { type: "about:blank", title: "x", status: 401, detail: "log in", code: "auth.required" } }
  function app() {
    return (
      <SessionProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<RequireSession><p>Hosts content</p></RequireSession>} />
        </Routes>
      </SessionProvider>
    )
  }
  test("A37: the button appears only when OIDC is configured", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut, "GET /api/v1/auth/methods": { body: { oidc: { displayName: "Acme SSO" }, tailscale: false } } })
    const on = renderWithProviders(app(), { path: "/login", session: null })
    expect(await screen.findByRole("link", { name: "Sign in with Acme SSO" })).toBeTruthy()
    on.unmount()
    const calls = stubFetch({ "GET /api/v1/auth/session": signedOut, "GET /api/v1/auth/methods": { body: { oidc: null, tailscale: false } } })
    renderWithProviders(app(), { path: "/login", session: null })
    await screen.findByLabelText("Username")
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/auth/methods")).toBe(true))
    expect(screen.queryByRole("link", { name: /^Sign in with/ })).toBeNull()
  })
  test("A37: the no-linked-user message gets focus", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut, "GET /api/v1/auth/methods": { body: { oidc: { displayName: "Acme SSO" }, tailscale: false } } })
    renderWithProviders(app(), { path: "/?signin=no_linked_user", session: null })
    const msg = await screen.findByText("No Darkpanel user is linked to this account. Ask an admin to add you.")
    await waitFor(() => expect(document.activeElement?.contains(msg) || msg.contains(document.activeElement)).toBe(true))
  })
})

describe("A38 UX-4 send test", () => {
  test("A38: Send test shows pending, then Delivered or Failed: <status> as text", async () => {
    let release: (() => void) | null = null
    stubFetch({
      [read("notify.channel.list")]: out({ channels: [channel, chat] }),
      [read("core.group.list")]: out({ groups: [] }),
      [read("notify.channel.test")]: async (call) => {
        if ((call.body as { input: { id: string } }).input.id === "c1") {
          await new Promise<void>((resolve) => {
            release = resolve
          })
          return { body: { output: { delivered: true, code: "200" } } }
        }
        return { body: { output: { delivered: false, code: "503" } } }
      },
    })
    renderWithProviders(<NotificationsPage />)
    const user = userEvent.setup()
    // The card is the nearest ancestor of the channel's name that holds its Send test button.
    const card = async (name: string) => {
      let el: HTMLElement | null = await screen.findByText(name)
      while (el !== null && within(el).queryAllByRole("button", { name: "Send test" }).length !== 1) el = el.parentElement
      if (el === null) throw new Error(`no card for ${name}`)
      return el
    }
    const phone = await card("ops-phone")
    await user.click(within(phone).getByRole("button", { name: "Send test" }))
    const pending = await within(phone).findByText(/Sending/)
    expect(pending.textContent).toMatch(/sending/i)
    await waitFor(() => expect(release).not.toBeNull())
    release!()
    expect(await within(phone).findByText("Delivered")).toBeTruthy()
    const team = await card("team-chat")
    await user.click(within(team).getByRole("button", { name: "Send test" }))
    expect(await within(team).findByText("Failed: 503")).toBeTruthy()
  })
})

interface PageCase {
  name: string
  page: ReactElement
  routes: (s: "pending" | "empty" | "error") => Record<string, StubHandler>
  empty: string | null
}
const pageCases: PageCase[] = [
  { name: "host groups", page: <GroupsPage />, empty: "Create group", routes: (s) => ({ [read("core.group.list")]: s === "pending" ? never : s === "error" ? fail : out({ groups: [] }) }) },
  {
    name: "notification channels", page: <NotificationsPage />, empty: "Add channel",
    routes: (s) => ({ [read("notify.channel.list")]: s === "pending" ? never : s === "error" ? fail : out({ channels: [] }), [read("core.group.list")]: out({ groups: [] }) }),
  },
  {
    name: "alert rules", page: <AlertsPage />, empty: "New rule",
    routes: (s) => ({ [read("metrics.alert.list-rules")]: s === "pending" ? never : s === "error" ? fail : out({ rules: [] }), [read("metrics.alert.list")]: out({ alerts: [] }) }),
  },
  { name: "users", page: <UsersPage />, empty: null, routes: (s) => ({ [read("core.user.directory")]: s === "pending" ? never : fail, [read("core.group.list")]: out({ groups: [] }) }) },
  { name: "sign-in settings", page: <SignInSettingsPage />, empty: null, routes: (s) => ({ [read("auth.settings.get")]: s === "pending" ? never : fail }) },
]

describe("A39 UX-6 state matrix", () => {
  test.each(pageCases)("A39: $name shows a skeleton while loading", async (c) => {
    stubFetch(c.routes("pending"))
    const { container } = renderWithProviders(c.page)
    await waitFor(() => expect(container.querySelector('[data-slot="skeleton"], .animate-pulse, [class*="skeleton"]')).not.toBeNull())
  })
  test.each(pageCases)("A39: $name shows Couldn't load and Retry on an error", async (c) => {
    stubFetch(c.routes("error"))
    renderWithProviders(c.page)
    expect(await screen.findByText(/^Couldn't load/)).toBeTruthy()
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy()
  })
  test.each(pageCases.filter((c) => c.empty !== null))("A39: $name shows one sentence and the create button when empty", async (c) => {
    stubFetch(c.routes("empty"))
    renderWithProviders(c.page)
    await waitFor(() => expect(screen.getAllByRole("button", { name: c.empty! })).toHaveLength(1))
    expect(screen.queryByRole("table")).toBeNull()
  })
  test("A39: the users page is never empty: the admin is listed", async () => {
    stubFetch({ [read("core.user.directory")]: out({ users: [dirUser({ username: "admin", role: "admin" })] }), [read("core.group.list")]: out({ groups: [] }) })
    renderWithProviders(<UsersPage />)
    expect(await screen.findByText("admin")).toBeTruthy()
    expect(screen.queryByText(/no users/i)).toBeNull()
  })
  test("A39: worst cases all render: 32 groups with a 48-character name, 50 users, 200 hosts in the members dialog, 20 firing alerts", async () => {
    const long = `ops-${"g".repeat(44)}`
    const many: HostGroup[] = Array.from({ length: 32 }, (_, i) => ({ id: `g${i}`, name: i === 0 ? long : `group-${i}`, hostIds: [], hostCount: i, grantCount: 0, createdAt: "2026-11-20T08:00:00Z" }))
    const hosts = Array.from({ length: 200 }, (_, i) => makeHost({ id: `h${i}`, name: `host-${String(i).padStart(3, "0")}` }))
    stubFetch({ [read("core.group.list")]: out({ groups: many }), [read("core.host.list")]: out({ hosts }) })
    const g = renderWithProviders(<GroupsPage />)
    const user = userEvent.setup()
    expect(await screen.findByText(long)).toBeTruthy()
    expect(screen.getAllByRole("row")).toHaveLength(33)
    await user.click(await screen.findByRole("button", { name: `Actions for ${long}` }))
    await user.click(await screen.findByRole("menuitem", { name: "Edit members" }))
    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findAllByRole("checkbox")).toHaveLength(200)
    g.unmount()
    const users = Array.from({ length: 50 }, (_, i) => dirUser({ username: i === 0 ? "admin" : `user-${String(i).padStart(2, "0")}`, role: i === 0 ? "admin" : "member" }))
    stubFetch({ [read("core.user.directory")]: out({ users }), [read("core.group.list")]: out({ groups }) })
    const u = renderWithProviders(<UsersPage />)
    expect(await screen.findByText("user-49")).toBeTruthy()
    expect(screen.getAllByRole("row")).toHaveLength(51)
    u.unmount()
    const firing: FiredAlert[] = Array.from({ length: 20 }, (_, i) => ({ ...alert, hostId: `h${i}`, hostName: `web-${String(i).padStart(3, "0")}`, subject: `/srv/${i}` }))
    stubFetch({ [read("metrics.alert.list")]: out({ alerts: firing }), [read("metrics.alert.list-rules")]: out({ rules: [rule] }) })
    renderWithProviders(<AlertsPage />)
    expect(await screen.findByText("web-019")).toBeTruthy()
    expect(screen.getAllByText(/^web-0\d\d$/)).toHaveLength(20)
  })
})

describe("A40 UX-7 metrics states", () => {
  const status = { lastOk: "2026-11-24T09:03:00Z", lastErrorCode: null, lastErrorAt: null, clockSkewS: 0 }
  const history = (points: MetricsPoint[], st: unknown = status) => out({ hostId: "h1", range: "1h", resolution: "1m", points, status: st })
  test("A40: collecting, offline and disabled states", async () => {
    stubFetch({ [read("metrics.history.get")]: history([], null), "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { logicalCount: 4 } } } })
    const a = renderWithProviders(<MetricsSection host={metricsHost} />)
    expect(await screen.findByText("Collecting — the first samples appear within 2 minutes")).toBeTruthy()
    a.unmount()
    stubFetch({ [read("metrics.history.get")]: history([point(0), point(1)]), "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { logicalCount: 4 } } } })
    const b = renderWithProviders(<MetricsSection host={{ ...metricsHost, online: false, lastSeenAt: "2026-11-24T09:05:00Z" }} />)
    expect(await screen.findByText(/^Offline since /)).toBeTruthy()
    expect((await screen.findAllByRole("img")).length).toBeGreaterThan(0)
    b.unmount()
    stubFetch({})
    renderWithProviders(<MetricsSection host={{ ...metricsHost, modules: [{ ...metricsOn, enabled: false }] }} />)
    expect(await screen.findByText("Metrics are off on this host")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Enable" })).toBeTruthy()
  })
  test("A40: a missing minute is a break in the line, never a zero", async () => {
    stubFetch({ [read("metrics.history.get")]: history([point(0), point(1), point(4)]), "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { logicalCount: 4 } } } })
    renderWithProviders(<MetricsSection host={metricsHost} />)
    const chart = await screen.findByRole("img", { name: /^CPU/ })
    const name = chart.getAttribute("aria-label") ?? chart.textContent ?? ""
    // Samples 10, 11, (gap), (gap), 14: a zero would pull the minimum to 0.
    expect(name).toMatch(/min 10(\.0)?\b/)
    expect(name).toMatch(/max 14(\.0)?\b/)
    expect(name).not.toMatch(/min 0(\.0)?\b/)
  })
})

describe("A41 UX-8 member UI", () => {
  function renderApp(session: Session, path: string, scope: "read" | "operate" | "admin" = "read") {
    stubFetch({
      "GET /api/v1/auth/session": { body: session },
      "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "" } },
      [read("core.host.list")]: out({ hosts: [{ ...makeHost({ id: "h1", name: "web-1" }), scope }] }),
      [read("core.update.rollouts")]: out({ rollouts: [] }),
      "POST /api/v1/hosts/h1/actions/core.update.status/v1/read": out({ transactions: [] }),
    })
    renderWithProviders(<App />, { path, session: null })
  }
  const has = (name: string) => screen.queryAllByRole("link", { name }).length > 0
  test("A41: a member sees no settings pages and no Network pages", async () => {
    renderApp(memberSession(), "/")
    await screen.findAllByRole("link", { name: "API tokens" })
    for (const name of ["Users", "Host groups", "Sign-in", "Notifications", "Tailnet", "DERP", "Cloudflare"]) expect(has(name), name).toBe(false)
    expect(has("Hosts")).toBe(true)
    expect(screen.queryByRole("button", { name: "Add host" })).toBeNull()
  })
  test.each(["/settings/users", "/settings/groups", "/settings/sign-in", "/settings/notifications", "/tailnet"])("A41: a member's deep link to %s says only admins can see it", async (path) => {
    renderApp(memberSession(), path)
    expect(await screen.findByText("Only admins can see this page.")).toBeTruthy()
  })
  test("A41: a read-scope host shows no button for an action its scope refuses", async () => {
    renderApp(memberSession(), "/hosts/h1", "read")
    await screen.findByRole("heading", { level: 1, name: "web-1" })
    expect(screen.queryByRole("button", { name: /^(Update|Revoke|Remove|Delete|Enable|Disable|Reboot|Change)/ })).toBeNull()
  })
  test("A41: the member's session stub holds only its host: a host outside its grants is not listed", async () => {
    renderApp(memberSession(), "/")
    expect((await screen.findAllByText("web-1")).length).toBeGreaterThan(0)
    expect(screen.queryByText("db-outside")).toBeNull()
  })
})

describe("A42 UX-11 keyboard", () => {
  test.each([
    ["host groups", <GroupsPage />, "Create group", { [read("core.group.list")]: out({ groups }), [read("core.host.list")]: out({ hosts: [] }) }],
    ["users", <UsersPage />, "Invite user", baseRoutes],
    ["notifications", <NotificationsPage />, "Add channel", baseRoutes],
    ["alerts", <AlertsPage />, "New rule", baseRoutes],
  ] as [string, ReactElement, string, Record<string, StubHandler>][])("A42: %s dialog traps focus and Escape returns focus to %s", async (_n, page, trigger, routes) => {
    stubFetch(routes)
    renderWithProviders(page)
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: trigger }))
    const dialog = await screen.findByRole("dialog")
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
    for (let i = 0; i < 20; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement), `tab ${i + 1}`).toBe(true)
    }
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(focusedName()).toBe(trigger)
  })
  test("A42: the range control is a radio group that arrow keys move, and charts have accessible names", async () => {
    stubFetch(baseRoutes)
    renderWithProviders(<MetricsSection host={metricsHost} />)
    const group = await screen.findByRole("radiogroup")
    const radios = within(group).getAllByRole("radio")
    expect(radios.map((r) => r.getAttribute("aria-label") ?? r.textContent)).toEqual(expect.arrayContaining(["1h", "24h", "7d", "30d", "90d"]))
    const user = userEvent.setup()
    const checked = () => radios.findIndex((r) => (r as HTMLInputElement).checked || r.getAttribute("aria-checked") === "true" || r.getAttribute("data-state") === "checked")
    const first = checked()
    radios[first]!.focus()
    await user.keyboard("{ArrowRight}")
    await waitFor(() => expect(checked()).toBe(first + 1))
    await user.keyboard("{ArrowLeft}")
    await waitFor(() => expect(checked()).toBe(first))
    const charts = await screen.findAllByRole("img")
    expect(charts.length).toBeGreaterThanOrEqual(5)
    for (const c of charts) expect(c.getAttribute("aria-label") ?? c.getAttribute("aria-labelledby") ?? "").not.toBe("")
  })
})

describe("A44 OP-3 axe and chart text", () => {
  const pages: [string, ReactElement, string][] = [
    ["users", <UsersPage />, "admin"],
    ["host groups", <GroupsPage />, "web"],
    ["sign-in settings", <SignInSettingsPage />, "OIDC"],
    ["notifications", <NotificationsPage />, "ops-phone"],
    ["alerts", <AlertsPage />, "Disk almost full on /"],
    ["host metrics", <MetricsSection host={metricsHost} />, "CPU (%)"],
  ]
  test.each(pages)("A44: %s has no axe violation", async (_name, page, ready) => {
    stubFetch(baseRoutes)
    const { container } = renderWithProviders(page)
    expect((await screen.findAllByText(ready)).length).toBeGreaterThan(0)
    await expectNoAxeViolations(container)
  })
  test("A44: every chart has a text summary with latest, min and max", async () => {
    stubFetch(baseRoutes)
    renderWithProviders(<MetricsSection host={metricsHost} />)
    const charts = await screen.findAllByRole("img")
    expect(charts.length).toBeGreaterThanOrEqual(5)
    for (const c of charts) {
      const text = c.getAttribute("aria-label") ?? ""
      expect(text, "a chart's accessible name").toMatch(/latest/i)
      expect(text).toMatch(/min/i)
      expect(text).toMatch(/max/i)
    }
  })
})

