import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { cfTunnel } from "./fixtures"
import { TunnelsPage } from "./tunnels-page"
import type { TunnelView } from "./types"

const LIST = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"

function tpConnection(colo: string, id: string) {
  return { id, colo, originIp: "203.0.113.5", openedAt: "2026-10-30T08:05:00Z", version: "2026.10.0" }
}

function tpTunnels(): TunnelView[] {
  return [
    cfTunnel({ id: "t1", name: "darkpanel-c1", status: "healthy", connections: [tpConnection("ams01", "k1"), tpConnection("fra02", "k2")], hostnames: 2 }),
    cfTunnel({ id: "t2", name: "darkpanel-c2", status: "inactive", drift: true }),
    cfTunnel({ id: "t3", name: "darkpanel-c3", status: "unknown" }),
  ]
}

function tpPlan(action: string, confirmation: string | null = null): Plan {
  return {
    planId: "p1", hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "~", text: "tunnel" }], guards: [],
    expiresAt: "2026-10-30T09:00:00Z", confirmation,
  }
}

const tpChange: Change = {
  id: "c9", hostId: null, action: "cloudflare.tunnel.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
  createdAt: "2026-10-30T08:00:00Z", updatedAt: "2026-10-30T08:00:00Z", revertDeadline: null, output: { id: "t4", name: "darkpanel-c9" }, error: null,
  compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function tpRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({ [LIST]: { body: { output: { tunnels: tpTunnels() } } }, ...routes })
  const result = renderWithProviders(<TunnelsPage />)
  return { calls, ...result }
}

async function tpTable() {
  return screen.findByRole("table", { name: "Tunnels" })
}

function tpPlanCall(calls: { path: string; body: unknown }[], action: string) {
  return calls.find((c) => c.path === `/api/v1/actions/${action}/v1/plan`)
}

test("lists the tunnels with status, connections, hostnames and configuration, in the server's order", async () => {
  tpRender()
  const rows = within(await tpTable()).getAllByRole("row").slice(1)
  expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 5).map((c) => c.textContent))).toEqual([
    ["darkpanel-c1", "healthy", "2 (ams01, fra02)", "2", "In sync"],
    ["darkpanel-c2", "inactive", "0", "0", "Changed outside Darkpanel"],
    ["darkpanel-c3", "unknown", "0", "0", "In sync"],
  ])
})

test("offers to accept the configuration only for a tunnel that drifted", async () => {
  tpRender()
  await tpTable()
  expect(screen.getAllByRole("button", { name: /^Accept the current configuration of / }).map((b) => b.getAttribute("aria-label"))).toEqual([
    "Accept the current configuration of darkpanel-c2",
  ])
})

test("a tunnel is created without input", async () => {
  const { calls } = tpRender({ "POST /api/v1/actions/cloudflare.tunnel.create/v1/plan": { body: tpPlan("cloudflare.tunnel.create") } })
  const user = userEvent.setup()
  await tpTable()
  await user.click(screen.getByRole("button", { name: "Create tunnel" }))
  const dialog = await screen.findByRole("dialog", { name: "Create a tunnel" })
  await within(dialog).findByLabelText("Planned changes")
  expect(tpPlanCall(calls, "cloudflare.tunnel.create")?.body).toEqual({ input: {} })
})

test("a created tunnel appears in the list once the dialog closes", async () => {
  let tunnels = tpTunnels()
  const { calls } = tpRender({
    [LIST]: () => ({ body: { output: { tunnels } } }),
    "POST /api/v1/actions/cloudflare.tunnel.create/v1/plan": { body: tpPlan("cloudflare.tunnel.create") },
    "POST /api/v1/actions/cloudflare.tunnel.create/v1/apply": () => {
      tunnels = [...tunnels, cfTunnel({ id: "t4", name: "darkpanel-c9", status: "inactive" })]
      return { body: tpChange }
    },
  })
  const user = userEvent.setup()
  await tpTable()
  await user.click(screen.getByRole("button", { name: "Create tunnel" }))
  const dialog = await screen.findByRole("dialog", { name: "Create a tunnel" })
  await within(dialog).findByLabelText("Planned changes")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  // The dialog's corner button has the same name; the footer's is the last.
  await user.click(within(dialog).getAllByRole("button", { name: "Close" }).slice(-1)[0] as HTMLElement)
  await waitFor(() => expect(within(screen.getByRole("table", { name: "Tunnels" })).getAllByRole("row")).toHaveLength(5))
  expect(calls.filter((c) => c.path.endsWith("cloudflare.tunnel.list/v1/read"))).toHaveLength(2)
})

test("deleting a tunnel plans it by name and asks for the name to be typed", async () => {
  const { calls } = tpRender({ "POST /api/v1/actions/cloudflare.tunnel.delete/v1/plan": { body: tpPlan("cloudflare.tunnel.delete", "darkpanel-c1") } })
  const user = userEvent.setup()
  await tpTable()
  await user.click(screen.getByRole("button", { name: "Delete tunnel darkpanel-c1" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete tunnel darkpanel-c1" })
  await within(dialog).findByLabelText("Type darkpanel-c1 to confirm")
  expect(tpPlanCall(calls, "cloudflare.tunnel.delete")?.body).toEqual({ input: { tunnel: "darkpanel-c1" } })
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("accepting the current configuration plans the adoption by tunnel id", async () => {
  const { calls } = tpRender({ "POST /api/v1/actions/cloudflare.tunnel.adopt/v1/plan": { body: tpPlan("cloudflare.tunnel.adopt") } })
  const user = userEvent.setup()
  await tpTable()
  await user.click(screen.getByRole("button", { name: "Accept the current configuration of darkpanel-c2" }))
  const dialog = await screen.findByRole("dialog", { name: "Accept the configuration of darkpanel-c2" })
  await within(dialog).findByLabelText("Planned changes")
  expect(tpPlanCall(calls, "cloudflare.tunnel.adopt")?.body).toEqual({ input: { tunnel: "t2" } })
})

test("without tunnels the page says so and still offers to create one", async () => {
  tpRender({ [LIST]: { body: { output: { tunnels: [] } } } })
  expect((await screen.findByText("No tunnels yet")).textContent).toBe("No tunnels yet")
  expect(screen.queryByRole("table", { name: "Tunnels" })).toBeNull()
  expect(screen.getByRole("button", { name: "Create tunnel" })).toBeTruthy()
})

test("an account that is not connected shows the reason", async () => {
  tpRender({ [LIST]: { status: 409, body: { type: "about:blank", title: "Not connected", status: 409, detail: "no Cloudflare account is connected", code: "cloud.not_connected" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("no Cloudflare account is connected")
  expect(screen.queryByRole("button", { name: "Create tunnel" })).toBeNull()
})

test("has no accessibility violations", async () => {
  const { container } = tpRender()
  await tpTable()
  expect(await axe(container)).toHaveNoViolations()
})
