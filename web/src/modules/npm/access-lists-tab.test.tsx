import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AccessListsTab } from "./access-lists-tab"
import { npAccessList } from "./fixtures"
import type { AccessListView } from "./types"

const AL_HOST = "/api/v1/hosts/h1"
const AL_LIST = `POST ${AL_HOST}/actions/npm.accesslist.list/v1/read`

function alPlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1", hostId: "h1", action: parts[parts.length - 3] ?? "", version: 1, inputHash: "h",
      diff: [{ op: "~", text: "change" }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null,
    },
  }
}

function alLists(): AccessListView[] {
  return [
    npAccessList({ id: 2, name: "darkpanel-01jd-1", satisfyAny: true, clients: [{ address: "10.0.0.0/8", directive: "allow" }, { address: "192.0.2.1", directive: "deny" }], reserved: "", unclaimed: false }),
    npAccessList({ id: 3, name: "darkpanel-01jd-2", satisfyAny: false, clients: [{ address: "203.0.113.0/24", directive: "allow" }], reserved: "npm.admin.expose", unclaimed: false }),
    npAccessList({ id: 8, name: "darkpanel-01jd-9", satisfyAny: false, clients: [{ address: "198.51.100.7", directive: "allow" }], reserved: "", unclaimed: true }),
  ]
}

function alRender(items: AccessListView[] = alLists(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [AL_LIST]: { body: { output: { items } } },
    [`GET ${AL_HOST}/modules/npm/availability`]: { body: { module: "npm", available: true, reason: null, actions: [] } },
    [`POST ${AL_HOST}/actions/npm.accesslist.add/v1/plan`]: alPlan,
    [`POST ${AL_HOST}/actions/npm.accesslist.remove/v1/plan`]: alPlan,
    ...routes,
  })
  const result = renderWithProviders(<AccessListsTab hostId="h1" />)
  return { calls, ...result }
}

function alCells(n: number): (string | null)[] {
  const row = within(screen.getByRole("table", { name: "Access lists" })).getAllByRole("row")[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return within(row).getAllByRole("cell").map((c) => c.textContent)
}

function alPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

test("lists each access list with how it combines its rules, the rules and the marks", async () => {
  alRender()
  await screen.findByRole("table", { name: "Access lists" })
  expect(alCells(1).slice(0, 5)).toEqual(["2", "darkpanel-01jd-1", "any rule", "allow 10.0.0.0/8, deny 192.0.2.1", ""])
  expect(alCells(2).slice(0, 5)).toEqual(["3", "darkpanel-01jd-2", "all rules", "allow 203.0.113.0/24", "Reserved: npm.admin.expose"])
  expect(alCells(3).slice(0, 5)).toEqual(["8", "darkpanel-01jd-9", "all rules", "allow 198.51.100.7", "Unclaimed Darkpanel object"])
})

test("says so when there are no access lists", async () => {
  alRender([])
  expect((await screen.findByText("No access lists")).textContent).toBe("No access lists")
})

test("shows the reason when the access lists cannot be read", async () => {
  alRender([], {
    [AL_LIST]: { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
})

test.each([
  ["no items", {}],
  ["an item without clients", { items: [{ id: 2, name: "office" }] }],
])("shows an error and no table when the reply has %s", async (_, output) => {
  alRender([], { [AL_LIST]: { body: { output } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected list of NPM access lists.")
  expect(screen.queryByRole("table")).toBeNull()
})

test("a reserved access list cannot be removed here", async () => {
  alRender()
  await screen.findByRole("table", { name: "Access lists" })
  expect(screen.getByRole("button", { name: "Remove darkpanel-01jd-2" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Remove darkpanel-01jd-1" }).hasAttribute("disabled")).toBe(false)
})

test("removing an access list plans npm.accesslist.remove with its id", async () => {
  const { calls } = alRender()
  await screen.findByRole("table", { name: "Access lists" })
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove darkpanel-01jd-9" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove access list darkpanel-01jd-9" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe(`${AL_HOST}/actions/npm.accesslist.remove/v1/plan`)
  expect(alPlanBodies(calls)).toEqual([{ input: { id: 8 } }])
})

test("the add form reads one rule per line and plans npm.accesslist.add", async () => {
  const { calls } = alRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add access list" }))
  const form = await screen.findByRole("form", { name: "Access list" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Rules"), "allow 10.0.0.0/8{Enter}{Enter}Deny 192.0.2.1")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(within(form).getByRole("checkbox", { name: "Satisfy any rule" }))
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Add an access list with 2 rules" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe(`${AL_HOST}/actions/npm.accesslist.add/v1/plan`)
  expect(alPlanBodies(calls)).toEqual([
    { input: { satisfyAny: true, clients: [{ address: "10.0.0.0/8", directive: "allow" }, { address: "192.0.2.1", directive: "deny" }] } },
  ])
})

test("a line that is not a rule is named and keeps Plan off", async () => {
  alRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add access list" }))
  const form = await screen.findByRole("form", { name: "Access list" })
  await user.type(within(form).getByLabelText("Rules"), "allow 10.0.0.1{Enter}permit 10.0.0.2")
  expect(within(form).getByText("Line 2: start with allow or deny, then an address.")).toBeTruthy()
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("the table has no accessibility violations", async () => {
  alRender()
  const table = await screen.findByRole("table", { name: "Access lists" })
  expect(await axe(table)).toHaveNoViolations()
})
