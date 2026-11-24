import type { ReactElement } from "react"
import { QueryClient, type QueryKey } from "@tanstack/react-query"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import type { AlertRule, DirectoryUser, FiredAlert, HostGroup, MetricsPoint } from "@/api/types"
import { connectEvents } from "@/api/events"
import { ALERT_RULES_QUERY_KEY } from "@/lib/alerts"
import { charts } from "@/lib/metrics"
import { CHANNELS_QUERY_KEY } from "@/lib/notify"
import { EvtSource } from "@/test/evt-source"
import { makeHost, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { AlertsPage } from "./alerts"
import { GroupsPage } from "./groups"
import { NotificationsPage } from "./notifications"
import { SignInSettingsPage } from "./sign-in-settings"
import { UsersPage } from "./users"

const read = (action: string) => `POST /api/v1/actions/${action}/v1/read`
const out = (output: unknown) => ({ body: { output } })
const never: StubHandler = () => new Promise(() => {})
const fail: StubHandler = { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } }

const LONG = `ops-${"g".repeat(44)}`
const groups: HostGroup[] = Array.from({ length: 32 }, (_, i) => ({
  id: `g${i}`, name: i === 0 ? LONG : `group-${i}`, hostIds: [], hostCount: i, grantCount: 0, createdAt: "2026-11-20T08:00:00Z",
}))
const users: DirectoryUser[] = Array.from({ length: 50 }, (_, i) => ({
  id: `u${i}`, username: i === 0 ? "admin" : `user-${String(i).padStart(2, "0")}`, role: i === 0 ? "admin" : "member", state: "active", createdAt: "2026-11-20T08:00:00Z",
  disabled: false, grants: i === 0 ? [] : [{ role: "viewer", target: "g0" }], email: "", tailnetLogin: "", signIn: ["password"], inviteExpiresAt: null,
}))
const firing: FiredAlert[] = Array.from({ length: 20 }, (_, i) => ({
  ruleId: "r1", ruleName: "Disk almost full", metric: "disk", severity: i % 4 === 0 ? "critical" : "warning", hostId: `h${i}`, hostName: `web-${String(i).padStart(3, "0")}`,
  subject: `/srv/data-${i}`, state: "firing", since: "2026-11-24T08:00:00Z", value: 90 + (i % 9),
}))
const rule: AlertRule = {
  id: "r1", name: "Disk almost full", metric: "disk", comparator: ">", threshold: 90, durationMin: 10, severity: "warning", target: "g0", targetName: LONG, enabled: true,
  createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z",
}

interface PageCase {
  name: string
  page: ReactElement
  routes: (state: "pending" | "empty" | "error") => Record<string, StubHandler>
  loading: string
  error: string
  empty: { text: string; create: string } | null
}

const cases: PageCase[] = [
  {
    name: "users", page: <UsersPage />, loading: "Loading the users", error: "Couldn't load users: boom", empty: null,
    routes: (s) => ({ [read("core.user.directory")]: s === "pending" ? never : fail, [read("core.group.list")]: out({ groups: [] }) }),
  },
  {
    name: "host groups", page: <GroupsPage />, loading: "Loading the host groups", error: "Couldn't load host groups: boom",
    empty: { text: "Group hosts to grant access to them together.", create: "Create group" },
    routes: (s) => ({ [read("core.group.list")]: s === "pending" ? never : s === "error" ? fail : out({ groups: [] }) }),
  },
  {
    name: "notification channels", page: <NotificationsPage />, loading: "Loading the channels", error: "Couldn't load notification channels: boom",
    empty: { text: "Add one to get notified of alerts and failed changes.", create: "Add channel" },
    routes: (s) => ({ [read("notify.channel.list")]: s === "pending" ? never : s === "error" ? fail : out({ channels: [] }), [read("core.group.list")]: out({ groups: [] }) }),
  },
  {
    name: "alert rules", page: <AlertsPage />, loading: "Loading the alert rules", error: "Couldn't load alert rules: boom",
    empty: { text: "Add one to hear about busy or full hosts.", create: "New rule" },
    routes: (s) => ({ [read("metrics.alert.list-rules")]: s === "pending" ? never : s === "error" ? fail : out({ rules: [] }), [read("metrics.alert.list")]: out({ alerts: [] }) }),
  },
  {
    name: "firing alerts", page: <AlertsPage />, loading: "Loading the alerts", error: "Couldn't load alerts: boom", empty: null,
    routes: (s) => ({ [read("metrics.alert.list")]: s === "pending" ? never : fail, [read("metrics.alert.list-rules")]: out({ rules: [rule] }) }),
  },
  {
    name: "sign-in settings", page: <SignInSettingsPage />, loading: "Loading the sign-in settings", error: "Couldn't load the sign-in settings: boom", empty: null,
    routes: (s) => ({ [read("auth.settings.get")]: s === "pending" ? never : fail }),
  },
]

describe("UX-6 state matrix", () => {
  test.each(cases)("$name: loading shows a skeleton in the content's place", async (c) => {
    stubFetch(c.routes("pending"))
    renderWithProviders(c.page)
    const skeleton = await screen.findByLabelText(c.loading)
    expect(skeleton.children.length).toBeGreaterThan(0)
  })

  test.each(cases)("$name: an error says Couldn't load and retries", async (c) => {
    const calls = stubFetch(c.routes("error"))
    renderWithProviders(c.page)
    expect(await screen.findByText(c.error)).toBeTruthy()
    const before = calls.length
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
    await waitFor(() => expect(calls.length).toBeGreaterThan(before))
  })

  test.each(cases.filter((c) => c.empty !== null))("$name: empty shows one sentence and the create button", async (c) => {
    stubFetch(c.routes("empty"))
    renderWithProviders(c.page)
    expect(await screen.findByText(c.empty!.text)).toBeTruthy()
    expect(screen.getAllByRole("button", { name: c.empty!.create })).toHaveLength(1)
  })
})

describe("UX-6 worst case", () => {
  test("32 groups with a 48-character name all list, the long name wrapping", async () => {
    stubFetch({ [read("core.group.list")]: out({ groups }) })
    renderWithProviders(<GroupsPage />)
    const cell = await screen.findByText(LONG)
    expect(LONG).toHaveLength(48)
    expect(cell.className).toContain("wrap-anywhere")
    expect(screen.getAllByRole("row")).toHaveLength(33)
  })

  test("50 users all list", async () => {
    stubFetch({ [read("core.user.directory")]: out({ users }), [read("core.group.list")]: out({ groups }) })
    renderWithProviders(<UsersPage />)
    expect(await screen.findByText("user-49")).toBeTruthy()
    expect(screen.getAllByRole("row")).toHaveLength(51)
  })

  test("200 hosts all list in the members dialog", async () => {
    const hosts = Array.from({ length: 200 }, (_, i) => makeHost({ id: `h${i}`, name: `host-${String(i).padStart(3, "0")}` }))
    stubFetch({ [read("core.group.list")]: out({ groups }), [read("core.host.list")]: out({ hosts }) })
    renderWithProviders(<GroupsPage />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: `Actions for ${LONG}` }))
    await user.click(await screen.findByRole("menuitem", { name: "Edit members" }))
    const dialog = await screen.findByRole("dialog", { name: `Members of ${LONG}` })
    expect(await within(dialog).findAllByRole("checkbox")).toHaveLength(200)
    expect(within(dialog).getByText("0 of 200 hosts are members.")).toBeTruthy()
  })

  test("20 firing alerts all list, and a rule naming the long group wraps", async () => {
    stubFetch({ [read("metrics.alert.list")]: out({ alerts: firing }), [read("metrics.alert.list-rules")]: out({ rules: [rule] }) })
    renderWithProviders(<AlertsPage />)
    const table = await screen.findByRole("table", { name: "Firing alerts" })
    expect(within(table).getAllByRole("row")).toHaveLength(21)
    expect((await screen.findByText(`Group ${LONG}`)).className).toContain("wrap-anywhere")
  })

  test("16 mounts chart the 8 fullest", () => {
    const fs = Array.from({ length: 16 }, (_, i) => ({ mount: `/srv/data-${i}`, used: i, total: 100 }))
    const point: MetricsPoint = { ts: "2026-11-24T09:00:00Z", cpu: 1, memUsed: 1, memTotal: 2, swapUsed: 0, swapTotal: 0, load1: 1, load5: 1, load15: 1, fs, net: [] }
    const c = charts({ hostId: "h1", range: "1h", resolution: "1m", points: [point], status: null }, 2)
    expect(c.disk.series).toHaveLength(8)
    expect(c.disk.hidden).toEqual({ shown: 8, of: 16, noun: "mounts" })
  })
})

describe("A39 stale", () => {
  test("a change applied elsewhere refreshes users, groups, sign-in, channels and rules", () => {
    const client = new QueryClient()
    const keys: QueryKey[] = [["users"], ["groups"], ["sign-in-settings"], CHANNELS_QUERY_KEY, ALERT_RULES_QUERY_KEY]
    for (const key of keys) client.setQueryData(key, {})
    let source: EvtSource | undefined
    const close = connectEvents(client, (url) => {
      source = new EvtSource(url)
      return source as unknown as EventSource
    })
    source!.emit("change", { hostId: null, changeId: "c9", action: "core.group.rename", state: "completed", updatedAt: "2026-11-24T09:00:00Z" })
    expect(keys.filter((key) => client.getQueryState(key)?.isInvalidated === true)).toEqual(keys)
    close()
  })
})
