import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { AlertRule, FiredAlert, HostGroup } from "@/api/types"
import { makeChange, makePlan, memberSession, renderWithProviders, stubFetch } from "@/test/utils"
import { AlertsPage } from "./alerts"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

function alert(over: Partial<FiredAlert>): FiredAlert {
  return { ruleId: "r1", ruleName: "Disk almost full", metric: "disk", severity: "warning", hostId: "h1", hostName: "web-2", subject: "/", state: "firing", since: "2026-11-24T08:51:00Z", value: 93.4, ...over }
}

function rule(over: Partial<AlertRule>): AlertRule {
  return {
    id: "r1", name: "Disk almost full", metric: "disk", comparator: ">", threshold: 90, durationMin: 10, severity: "warning", target: "*", targetName: "", enabled: true,
    createdAt: "2026-11-20T08:00:00Z", updatedAt: "2026-11-20T08:00:00Z", ...over,
  }
}

const groups: HostGroup[] = [{ id: "g1", name: "web", hostIds: [], hostCount: 0, grantCount: 0, createdAt: "2026-11-20T08:00:00Z" }]

const alerts = [
  alert({ ruleId: "r9", ruleName: "Disk almost full", state: "resolved", hostId: "h3", hostName: "web-3", subject: "/srv", since: "2026-11-24T07:00:00Z", value: 91.2 }),
  alert({ ruleId: "r2", ruleName: "Host offline", metric: "offline", severity: "critical", hostId: "h2", hostName: "db-1", subject: "", since: "2026-11-24T09:28:00Z", value: 12 }),
  alert({}),
]

const rules = [
  rule({ id: "r2", name: "Host offline", metric: "offline", durationMin: 5, severity: "critical" }),
  rule({ id: "r1" }),
  rule({ id: "r3", name: "Busy CPU", metric: "cpu", threshold: 85, durationMin: 15, target: "g1", targetName: "web", enabled: false }),
  rule({ id: "r4", name: "Idle load", metric: "load", comparator: "<", threshold: 0.5, durationMin: 1 }),
]

function stub(extra: Parameters<typeof stubFetch>[0] = {}) {
  return stubFetch({
    [route("metrics.alert.list", "read")]: { body: { output: { alerts } } },
    [route("metrics.alert.list-rules", "read")]: { body: { output: { rules } } },
    [route("core.group.list", "read")]: { body: { output: { groups } } },
    ...extra,
  })
}

const rows = (table: HTMLElement) => within(table).getAllByRole("row").slice(1)

test("UI-5 firing alerts come first with severity as text and icon, host, alert, value and since", async () => {
  stub()
  renderWithProviders(<AlertsPage />)
  const firing = await screen.findByRole("table", { name: "Firing alerts" })
  const resolved = screen.getByRole("table", { name: "Resolved alerts" })
  expect(firing.compareDocumentPosition(resolved) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  const [offline, disk] = rows(firing)
  expect(rows(firing)).toHaveLength(2)
  expect(within(offline!).getByText("Critical")).toBeTruthy()
  expect(within(offline!).getByRole("link", { name: "db-1" }).getAttribute("href")).toBe("/hosts/h2")
  expect(within(offline!).getByText("Host offline")).toBeTruthy()
  expect(within(offline!).getByText("No connection for 12 minutes")).toBeTruthy()
  expect(within(disk!).getByText("Warning")).toBeTruthy()
  expect(within(disk!).getByText("Disk almost full on /")).toBeTruthy()
  expect(within(disk!).getByText("93.4 %")).toBeTruthy()
  expect(within(disk!).getAllByRole("cell")[4]!.textContent).not.toBe("")
  expect(within(resolved).getByText("Disk almost full on /srv")).toBeTruthy()

  const table = screen.getByRole("table", { name: "Alert rules" })
  const [off, full, cpu, load] = rows(table)
  expect(within(off!).getByText("Offline")).toBeTruthy()
  expect(within(off!).getByText("5 minutes")).toBeTruthy()
  expect(within(full!).getByText("Disk above 90 %")).toBeTruthy()
  expect(within(full!).getByText("All hosts")).toBeTruthy()
  expect(within(cpu!).getByText("Group web")).toBeTruthy()
  expect(within(cpu!).getByText("Off")).toBeTruthy()
  expect(within(cpu!).getByRole("switch", { name: "Busy CPU enabled" }).getAttribute("aria-checked")).toBe("false")
  expect(within(load!).getByText("Load below 0.5 per CPU")).toBeTruthy()
  expect(within(load!).getByText("1 minute")).toBeTruthy()
})

test("no firing alert says so, and no rule shows the empty state", async () => {
  stubFetch({
    [route("metrics.alert.list", "read")]: { body: { output: { alerts: [] } } },
    [route("metrics.alert.list-rules", "read")]: { body: { output: { rules: [] } } },
  })
  renderWithProviders(<AlertsPage />)
  expect(await screen.findByText("No alerts are firing.")).toBeTruthy()
  expect(await screen.findByText("No alert rules")).toBeTruthy()
  expect(screen.queryByRole("heading", { name: "Resolved in the last 24 hours" })).toBeNull()
})

test("UX-5 the rule's switch applies through the change dialog", async () => {
  const calls = stub({
    [route("metrics.alert.update", "plan")]: { body: makePlan("metrics.alert.update", { confirmation: null }) },
    [route("metrics.alert.update", "apply")]: { body: makeChange("metrics.alert.update", rules[0]) },
  })
  renderWithProviders(<AlertsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("switch", { name: "Host offline enabled" }))
  const change = await screen.findByRole("dialog", { name: "Turn off Host offline" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  expect(calls.find((c) => c.path.endsWith("metrics.alert.update/v1/plan"))?.body).toEqual({
    input: { id: "r2", name: "Host offline", metric: "offline", durationMin: 5, severity: "critical", target: "*", enabled: false },
  })
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("metrics.alert.list-rules/v1/read")).length).toBeGreaterThan(1))
})

test("UX-5 New rule checks its fields inline, then plans the rule", async () => {
  const calls = stub({
    [route("metrics.alert.create", "plan")]: { body: makePlan("metrics.alert.create", { confirmation: null }) },
  })
  renderWithProviders(<AlertsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "New rule" }))
  const form = await screen.findByRole("dialog", { name: "New rule" })
  await user.click(within(form).getByRole("radio", { name: "CPU" }))
  await user.clear(within(form).getByLabelText("Above (%)"))
  await user.type(within(form).getByLabelText("Above (%)"), "120")
  await user.clear(within(form).getByLabelText("For (minutes)"))
  await user.type(within(form).getByLabelText("For (minutes)"), "1441")
  await user.click(within(form).getByRole("button", { name: "Review rule" }))
  expect(within(form).getByText("Enter 0 to 100.")).toBeTruthy()
  expect(within(form).getByText("Enter whole minutes from 1 to 1440.")).toBeTruthy()
  expect(within(form).getByText(/^A name is 1 to 64/)).toBeTruthy()
  expect(within(form).getByLabelText("For (minutes)").getAttribute("aria-invalid")).toBe("true")
  expect(calls.some((c) => c.path.includes("metrics.alert.create"))).toBe(false)

  await user.type(within(form).getByLabelText("Name"), "Busy CPU")
  await user.click(within(form).getByRole("radio", { name: "Below" }))
  await user.clear(within(form).getByLabelText("Below (%)"))
  await user.type(within(form).getByLabelText("Below (%)"), "12.5")
  await user.clear(within(form).getByLabelText("For (minutes)"))
  await user.type(within(form).getByLabelText("For (minutes)"), "1440")
  await user.click(within(form).getByRole("radio", { name: "Critical" }))
  await within(form).findByRole("option", { name: "Group web" })
  await user.selectOptions(within(form).getByLabelText("Hosts"), "g1")
  await user.click(within(form).getByRole("button", { name: "Review rule" }))
  expect(await screen.findByRole("dialog", { name: "Create the rule Busy CPU" })).toBeTruthy()
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("metrics.alert.create/v1/plan"))?.body).toEqual({
      input: { name: "Busy CPU", metric: "cpu", comparator: "<", threshold: 12.5, durationMin: 1440, severity: "critical", target: "g1" },
    }),
  )
})

test("an offline rule sends no threshold, and load allows up to 1000", async () => {
  const calls = stub({ [route("metrics.alert.create", "plan")]: { body: makePlan("metrics.alert.create", { confirmation: null }) } })
  renderWithProviders(<AlertsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "New rule" }))
  const form = await screen.findByRole("dialog", { name: "New rule" })
  await user.click(within(form).getByRole("radio", { name: "Load" }))
  await user.clear(within(form).getByLabelText("Above (per CPU)"))
  await user.type(within(form).getByLabelText("Above (per CPU)"), "1001")
  await user.click(within(form).getByRole("button", { name: "Review rule" }))
  expect(within(form).getByText("Enter 0 to 1000.")).toBeTruthy()
  await user.type(within(form).getByLabelText("Name"), "Down")
  await user.click(within(form).getByRole("radio", { name: "Offline" }))
  expect(within(form).queryByLabelText(/^Above/)).toBeNull()
  await user.click(within(form).getByRole("button", { name: "Review rule" }))
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("metrics.alert.create/v1/plan"))?.body).toEqual({
      input: { name: "Down", metric: "offline", durationMin: 10, severity: "warning", target: "*" },
    }),
  )
})

test("editing a rule keeps its id and enabled state", async () => {
  const calls = stub({ [route("metrics.alert.update", "plan")]: { body: makePlan("metrics.alert.update", { confirmation: null }) } })
  renderWithProviders(<AlertsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for Busy CPU" }))
  await user.click(await screen.findByRole("menuitem", { name: "Edit" }))
  const form = await screen.findByRole("dialog", { name: "Edit Busy CPU" })
  expect((within(form).getByLabelText("Above (%)") as HTMLInputElement).value).toBe("85")
  await user.click(within(form).getByRole("button", { name: "Review change" }))
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("metrics.alert.update/v1/plan"))?.body).toEqual({
      input: { id: "r3", name: "Busy CPU", metric: "cpu", durationMin: 15, severity: "warning", target: "g1", comparator: ">", threshold: 85, enabled: false },
    }),
  )
})

test("UX-8 a member sees the firing alerts of its hosts and no rule controls", async () => {
  const calls = stub()
  renderWithProviders(<AlertsPage />, { session: memberSession(), hostScope: "read" })
  expect(await screen.findByRole("table", { name: "Firing alerts" })).toBeTruthy()
  expect(screen.queryByRole("button", { name: "New rule" })).toBeNull()
  expect(screen.queryByRole("heading", { name: "Rules" })).toBeNull()
  expect(screen.queryByRole("switch")).toBeNull()
  expect(calls.some((c) => c.path.includes("metrics.alert.list-rules"))).toBe(false)
})

test("a failed load says so and retries", async () => {
  let fail = true
  stubFetch({
    [route("metrics.alert.list", "read")]: () =>
      fail ? { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } } : { body: { output: { alerts } } },
    [route("metrics.alert.list-rules", "read")]: { body: { output: { rules } } },
  })
  renderWithProviders(<AlertsPage />)
  expect(await screen.findByText("Couldn't load alerts: boom")).toBeTruthy()
  fail = false
  await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByRole("table", { name: "Firing alerts" })).toBeTruthy()
})
