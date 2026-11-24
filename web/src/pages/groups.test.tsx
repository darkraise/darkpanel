import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { HostGroup } from "@/api/types"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { GroupsPage } from "./groups"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

function group(over: Partial<HostGroup>): HostGroup {
  return { id: "g1", name: "web", hostIds: [], hostCount: 0, grantCount: 0, createdAt: "2026-11-20T08:00:00Z", ...over }
}

const web = group({ id: "g1", name: "web", hostIds: ["h001", "h002", "h003", "h004"], hostCount: 4, grantCount: 2 })
const db = group({ id: "g2", name: "db", hostIds: ["h010"], hostCount: 1, grantCount: 0 })
const hosts = Array.from({ length: 200 }, (_, i) => {
  const n = String(i + 1).padStart(3, "0")
  return makeHost({ id: `h${n}`, name: `web-${n}` })
})

function stub(groups: () => HostGroup[], extra: Parameters<typeof stubFetch>[0] = {}) {
  return stubFetch({
    [route("core.group.list", "read")]: () => ({ body: { output: { groups: groups() } } }),
    [route("core.host.list", "read")]: { body: { output: { hosts } } },
    ...extra,
  })
}

test("UI-2 the table shows each group's name, host count and grant count", async () => {
  stub(() => [web, db])
  renderWithProviders(<GroupsPage />)
  const row = (await screen.findByText("web")).closest("tr") as HTMLElement
  expect(within(row).getByText("4")).toBeTruthy()
  expect(within(row).getByText("2")).toBeTruthy()
  const other = screen.getByText("db").closest("tr") as HTMLElement
  expect(within(other).getByText("1")).toBeTruthy()
  expect(within(other).getByText("0")).toBeTruthy()
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Hosts", "Grants", "Actions"])
})

test("Create group plans and applies core.group.create", async () => {
  let created = false
  const staging = group({ id: "g3", name: "staging" })
  const calls = stub(() => (created ? [web, staging] : [web]), {
    [route("core.group.create", "plan")]: { body: makePlan("core.group.create", { confirmation: null }) },
    [route("core.group.create", "apply")]: () => {
      created = true
      return { body: makeChange("core.group.create", staging) }
    },
  })
  renderWithProviders(<GroupsPage />)
  const user = userEvent.setup()
  await screen.findByText("web")
  await user.click(screen.getByRole("button", { name: "Create group" }))
  const form = await screen.findByRole("dialog", { name: "Create group" })
  await user.click(within(form).getByRole("button", { name: "Review group" }))
  expect(within(form).getByText(/^A name is 1 to 48/)).toBeTruthy()
  await user.type(within(form).getByLabelText("Name"), " staging ")
  await user.click(within(form).getByRole("button", { name: "Review group" }))
  const change = await screen.findByRole("dialog", { name: "Create the group staging" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("core.group.create/v1/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("core.group.create/v1/plan"))?.body).toEqual({ input: { name: "staging" } })
  expect(await screen.findByText("staging")).toBeTruthy()
})

test("the members dialog lists 200 hosts with a search and applies core.group.set-members", async () => {
  const calls = stub(() => [web], {
    [route("core.group.set-members", "plan")]: { body: makePlan("core.group.set-members", { confirmation: null }) },
    [route("core.group.set-members", "apply")]: { body: makeChange("core.group.set-members", web) },
  })
  renderWithProviders(<GroupsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for web" }))
  await user.click(await screen.findByRole("menuitem", { name: "Edit members" }))
  const form = await screen.findByRole("dialog", { name: "Members of web" })
  const list = await within(form).findByRole("list", { name: "Hosts" })
  expect(within(list).getAllByRole("checkbox")).toHaveLength(200)
  expect(within(form).getByText("4 of 200 hosts are members.")).toBeTruthy()
  expect(within(form).getByRole("checkbox", { name: "web-001" }).getAttribute("aria-checked")).toBe("true")

  await user.type(within(form).getByRole("searchbox", { name: "Search hosts" }), "web-19")
  expect(within(list).getAllByRole("checkbox").map((c) => c.getAttribute("id"))).toEqual(Array.from({ length: 10 }, (_, i) => `member-h${190 + i}`))
  await user.click(within(form).getByRole("checkbox", { name: "web-195" }))
  expect(within(form).getByText("5 of 200 hosts are members.")).toBeTruthy()
  await user.clear(within(form).getByRole("searchbox", { name: "Search hosts" }))
  await user.type(within(form).getByRole("searchbox", { name: "Search hosts" }), "nothing")
  expect(within(form).getByText('No hosts match "nothing".')).toBeTruthy()
  await user.clear(within(form).getByRole("searchbox", { name: "Search hosts" }))
  await user.click(within(form).getByRole("checkbox", { name: "web-002" }))
  await user.click(within(form).getByRole("checkbox", { name: "web-002" }))
  await user.click(within(form).getByRole("checkbox", { name: "web-003" }))
  expect(within(form).getByText("4 of 200 hosts are members.")).toBeTruthy()
  await user.click(within(form).getByRole("button", { name: "Review members" }))

  const change = await screen.findByRole("dialog", { name: "Change the members of web" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  expect(calls.find((c) => c.path.endsWith("core.group.set-members/v1/plan"))?.body).toEqual({ input: { id: "g1", hostIds: ["h001", "h002", "h004", "h195"] } })
})

test("after a delete removes its row, focus lands on Create group, not the page body", async () => {
  let deleted = false
  stub(() => (deleted ? [web] : [web, db]), {
    [route("core.group.delete", "plan")]: { body: makePlan("core.group.delete", { confirmation: null }) },
    [route("core.group.delete", "apply")]: () => {
      deleted = true
      return { body: makeChange("core.group.delete", {}) }
    },
  })
  renderWithProviders(<GroupsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for db" }))
  await user.click(await screen.findByRole("menuitem", { name: "Delete" }))
  const change = await screen.findByRole("dialog", { name: "Delete db" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  await waitFor(() => expect(screen.queryByRole("button", { name: "Actions for db" })).toBeNull())
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(document.activeElement?.textContent).toBe("Create group"))
})

test("a delete refused with group.in_use shows the names it lists", async () => {
  stub(() => [web], {
    [route("core.group.delete", "plan")]: {
      status: 409,
      body: { type: "about:blank", title: "Host group in use", status: 409, code: "group.in_use", detail: "host group web is in use: grant of mia, alert rule disk full, channel ops" },
    },
  })
  renderWithProviders(<GroupsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for web" }))
  await user.click(await screen.findByRole("menuitem", { name: "Delete" }))
  const change = await screen.findByRole("dialog", { name: "Delete web" })
  expect(await within(change).findByText(/grant of mia, alert rule disk full, channel ops/)).toBeTruthy()
  expect(within(change).queryByRole("button", { name: "Apply" })?.hasAttribute("disabled") ?? true).toBe(true)
})

test("an empty list says how groups help, and a failed load retries", async () => {
  let fail = true
  stubFetch({
    [route("core.group.list", "read")]: () =>
      fail ? { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } } : { body: { output: { groups: [] } } },
  })
  renderWithProviders(<GroupsPage />)
  expect(await screen.findByText("Couldn't load host groups: boom")).toBeTruthy()
  fail = false
  await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByText("No host groups yet")).toBeTruthy()
  expect(screen.getByText("Group hosts to grant access to them together.")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Create group" })).toBeTruthy()
})
