import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { DevicesPage } from "./devices-page"
import { tnDevice } from "./fixtures"

const DV_LIST = "POST /api/v1/actions/tailnet.device.list/v1/read"

const dvWeb = tnDevice({
  id: "n1",
  name: "web-1.example.ts.net",
  addresses: ["100.64.0.1"],
  tags: ["tag:server"],
  authorized: true,
  advertisedRoutes: ["10.0.0.0/24", "192.168.1.0/24", "0.0.0.0/0", "::/0"],
  enabledRoutes: ["10.0.0.0/24"],
  exitNode: true,
  exitNodeEnabled: false,
})
const dvLaptop = tnDevice({ id: "n2", name: "laptop.example.ts.net", addresses: ["100.64.0.2"], tags: [], authorized: false, advertisedRoutes: [], enabledRoutes: [], exitNode: false, exitNodeEnabled: false })

const dvDevices: StubResponse = { body: { output: { devices: [dvWeb, dvLaptop] } } }

function dvPlan(action: string, over: Partial<Plan> = {}): StubResponse {
  return { body: { planId: "p1", hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "~", text: "change the device" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null, ...over } }
}

const dvApplied: StubResponse = { body: { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null } }

function dvBodies(calls: FetchCall[], suffix: string): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith(suffix)).map((c) => c.body)
}

function dvReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.path.endsWith("tailnet.device.list/v1/read")).length
}

function dvRow(name: string): HTMLElement {
  const row = screen.getByText(name).closest("tr")
  if (row === null) throw new Error(`no row for ${name}`)
  return row
}

test("lists the devices with their status, routes and the actions each allows", async () => {
  stubFetch({ [DV_LIST]: dvDevices })
  renderWithProviders(<DevicesPage />)
  await screen.findByRole("table", { name: "Tailnet devices" })
  const web = within(dvRow("web-1.example.ts.net"))
  expect(web.getByText("100.64.0.1")).toBeTruthy()
  expect(web.getByText("tag:server")).toBeTruthy()
  expect(web.getByText("Authorized")).toBeTruthy()
  expect(web.getByText("1 of 2 approved, exit node not approved")).toBeTruthy()
  expect(web.queryByRole("button", { name: "Authorize web-1.example.ts.net" })).toBeNull()
  expect(web.getByRole("button", { name: "Routes of web-1.example.ts.net" })).toBeTruthy()
  const laptop = within(dvRow("laptop.example.ts.net"))
  expect(laptop.getByText("Pending approval")).toBeTruthy()
  expect(laptop.getByText("none")).toBeTruthy()
  expect(laptop.getByRole("button", { name: "Authorize laptop.example.ts.net" })).toBeTruthy()
  expect(laptop.queryByRole("button", { name: "Routes of laptop.example.ts.net" })).toBeNull()
})

const dvActions = [
  { button: "Authorize laptop.example.ts.net", action: "tailnet.device.authorize", device: "n2" },
  { button: "Expire web-1.example.ts.net", action: "tailnet.device.expire", device: "n1" },
  { button: "Delete laptop.example.ts.net", action: "tailnet.device.delete", device: "n2" },
]

test.each(dvActions)("$button plans $action for the device id", async ({ button, action, device }) => {
  const calls = stubFetch({ [DV_LIST]: dvDevices, [`POST /api/v1/actions/${action}/v1/plan`]: dvPlan(action) })
  renderWithProviders(<DevicesPage />)
  await userEvent.setup().click(await screen.findByRole("button", { name: button }))
  const dialog = await screen.findByRole("dialog", { name: button })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ change the device")
  expect(dvBodies(calls, `${action}/v1/plan`)).toEqual([{ input: { device } }])
})

test("a typed delete applies after the device name is typed and the list is read again", async () => {
  const calls = stubFetch({
    [DV_LIST]: dvDevices,
    "POST /api/v1/actions/tailnet.device.delete/v1/plan": dvPlan("tailnet.device.delete", { planId: "p9", confirmation: "laptop.example.ts.net" }),
    "POST /api/v1/actions/tailnet.device.delete/v1/apply": dvApplied,
  })
  renderWithProviders(<DevicesPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Delete laptop.example.ts.net" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete laptop.example.ts.net" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  expect(apply.hasAttribute("disabled")).toBe(true)
  await user.type(within(dialog).getByLabelText("Type laptop.example.ts.net to confirm"), "laptop.example.ts.net")
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(dvReads(calls)).toBe(2))
  expect(dvBodies(calls, "tailnet.device.delete/v1/apply")).toEqual([{ planId: "p9", confirmation: "laptop.example.ts.net" }])
})

test("approving routes sends the full approved list, with the exit node as both of its routes", async () => {
  const calls = stubFetch({
    [DV_LIST]: dvDevices,
    "POST /api/v1/actions/tailnet.device.set-routes/v1/plan": dvPlan("tailnet.device.set-routes"),
  })
  renderWithProviders(<DevicesPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Routes of web-1.example.ts.net" }))
  const routes = await screen.findByRole("dialog", { name: "Routes of web-1.example.ts.net" })
  expect(within(routes).getByRole("checkbox", { name: "10.0.0.0/24", checked: true })).toBeTruthy()
  expect(within(routes).getByRole("checkbox", { name: "192.168.1.0/24", checked: false })).toBeTruthy()
  expect(within(routes).getByRole("checkbox", { name: "Exit node (0.0.0.0/0 and ::/0)", checked: false })).toBeTruthy()
  expect(within(routes).queryByRole("checkbox", { name: "0.0.0.0/0" })).toBeNull()
  await user.click(within(routes).getByRole("checkbox", { name: "10.0.0.0/24" }))
  await user.click(within(routes).getByRole("checkbox", { name: "192.168.1.0/24" }))
  await user.click(within(routes).getByRole("checkbox", { name: "Exit node (0.0.0.0/0 and ::/0)" }))
  await user.click(within(routes).getByRole("button", { name: "Review" }))
  const dialog = await screen.findByRole("dialog", { name: "Approve routes for web-1.example.ts.net" })
  await within(dialog).findByLabelText("Planned changes")
  expect(screen.queryByRole("dialog", { name: "Routes of web-1.example.ts.net" })).toBeNull()
  expect(dvBodies(calls, "tailnet.device.set-routes/v1/plan")).toEqual([{ input: { device: "n1", routes: ["192.168.1.0/24", "0.0.0.0/0", "::/0"] } }])
})

test("unticking every route sends an empty list, and cancelling plans nothing", async () => {
  const calls = stubFetch({
    [DV_LIST]: dvDevices,
    "POST /api/v1/actions/tailnet.device.set-routes/v1/plan": dvPlan("tailnet.device.set-routes"),
  })
  renderWithProviders(<DevicesPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Routes of web-1.example.ts.net" }))
  let routes = await screen.findByRole("dialog", { name: "Routes of web-1.example.ts.net" })
  await user.click(within(routes).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(dvBodies(calls, "/plan")).toEqual([])

  await user.click(screen.getByRole("button", { name: "Routes of web-1.example.ts.net" }))
  routes = await screen.findByRole("dialog", { name: "Routes of web-1.example.ts.net" })
  await user.click(within(routes).getByRole("checkbox", { name: "10.0.0.0/24" }))
  await user.click(within(routes).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Approve routes for web-1.example.ts.net" })
  expect(dvBodies(calls, "tailnet.device.set-routes/v1/plan")).toEqual([{ input: { device: "n1", routes: [] } }])
})

test("an approved exit node starts ticked and stays in the list", async () => {
  const calls = stubFetch({
    [DV_LIST]: { body: { output: { devices: [{ ...dvWeb, enabledRoutes: ["0.0.0.0/0", "::/0"], exitNodeEnabled: true }] } } },
    "POST /api/v1/actions/tailnet.device.set-routes/v1/plan": dvPlan("tailnet.device.set-routes"),
  })
  renderWithProviders(<DevicesPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Routes of web-1.example.ts.net" }))
  const routes = await screen.findByRole("dialog", { name: "Routes of web-1.example.ts.net" })
  expect(within(routes).getByRole("checkbox", { name: "Exit node (0.0.0.0/0 and ::/0)", checked: true })).toBeTruthy()
  await user.click(within(routes).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Approve routes for web-1.example.ts.net" })
  expect(dvBodies(calls, "tailnet.device.set-routes/v1/plan")).toEqual([{ input: { device: "n1", routes: ["0.0.0.0/0", "::/0"] } }])
})

test("without a connected account the page says so and links to the account", async () => {
  stubFetch({ [DV_LIST]: { status: 409, body: { type: "about:blank", title: "Account not connected", status: 409, detail: "no tailnet account is connected", code: "cloud.not_connected" } } })
  renderWithProviders(<DevicesPage />)
  const link = await screen.findByRole("link", { name: "Connect an account" })
  expect(link.getAttribute("href")).toBe("/tailnet")
  expect(link.closest("[role=status]")?.textContent).toBe("No tailnet account is connected. Connect an account first.")
})

test("another failure of the read shows its message", async () => {
  stubFetch({ [DV_LIST]: { status: 503, body: { type: "about:blank", title: "Provider unavailable", status: 503, detail: "Tailscale did not answer", code: "cloud.unavailable" } } })
  renderWithProviders(<DevicesPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("Tailscale did not answer")
})

test("a tailnet with no devices says so", async () => {
  stubFetch({ [DV_LIST]: { body: { output: { devices: [] } } } })
  renderWithProviders(<DevicesPage />)
  expect(await screen.findByText("No devices")).toBeTruthy()
})

test("the table and the routes dialog have no accessibility violations", async () => {
  stubFetch({ [DV_LIST]: dvDevices })
  const { container } = renderWithProviders(<DevicesPage />)
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Tailnet devices" })
  expect(await axe(container)).toHaveNoViolations()
  await user.click(screen.getByRole("button", { name: "Routes of web-1.example.ts.net" }))
  expect(await axe(await screen.findByRole("dialog", { name: "Routes of web-1.example.ts.net" }))).toHaveNoViolations()
})
