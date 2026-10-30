import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { dkNetwork } from "./fixtures"
import { NetworksTab } from "./networks-tab"
import type { DockerNetwork } from "./types"

const NT_LIST = "GET /api/v1/hosts/h1/resources/docker/networks"

function ntPlan(call: FetchCall) {
  const action = call.path.split("/")[6] ?? ""
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null } }
}

function ntNetworks(): DockerNetwork[] {
  return [
    dkNetwork({ id: "n1", name: "bridge", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.17.0.0/16"], containers: ["web", "db"], owner: undefined }),
    dkNetwork({ id: "n2", name: "backend", driver: "bridge", scope: "local", internal: true, ipv6: true, subnets: ["10.9.0.0/24", "fd00::/64"], containers: [], owner: undefined }),
    dkNetwork({ id: "n3", name: "darkpanel-tunnel", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.31.255.0/28"], containers: ["cloudflared"], owner: "cloudflare" }),
  ]
}

function ntRender(networks: DockerNetwork[] = ntNetworks(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [NT_LIST]: { body: { networks } },
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/docker.network.create/v1/plan": ntPlan,
    "POST /api/v1/hosts/h1/actions/docker.network.remove/v1/plan": ntPlan,
    ...routes,
  })
  const result = renderWithProviders(<NetworksTab hostId="h1" />)
  return { calls, ...result }
}

function ntRow(n: number): HTMLElement {
  const row = within(screen.getByRole("table", { name: "Docker networks" })).getAllByRole("row")[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return row
}

function ntCells(n: number): (string | null)[] {
  return within(ntRow(n)).getAllByRole("cell").map((c) => c.textContent)
}

function ntPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

test("lists each network with its flags, subnets, containers and owner", async () => {
  ntRender()
  await screen.findByRole("table", { name: "Docker networks" })
  expect(ntCells(1)).toEqual(["bridge", "bridge", "local", "", "172.17.0.0/16", "web, db", "", "Remove"])
  expect(ntCells(2)).toEqual(["backend", "bridge", "local", "internal, IPv6", "10.9.0.0/24, fd00::/64", "", "", "Remove"])
  expect(ntCells(3)).toEqual(["darkpanel-tunnel", "bridge", "local", "", "172.31.255.0/28", "cloudflared", "cloudflare", "Remove"])
})

test("says so when the host has no networks", async () => {
  ntRender([])
  expect((await screen.findByText("No networks")).textContent).toBe("No networks")
  expect(screen.queryByRole("table")).toBeNull()
})

test("shows the reason when the networks cannot be read", async () => {
  ntRender([], {
    [NT_LIST]: { status: 503, body: { type: "about:blank", title: "Docker unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
})

test("removing a network plans docker.network.remove with its name", async () => {
  const { calls } = ntRender()
  await screen.findByRole("table", { name: "Docker networks" })
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove backend" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove network backend" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/hosts/h1/actions/docker.network.remove/v1/plan")
  expect(ntPlanBodies(calls)).toEqual([{ input: { network: "backend" } }])
})

test("a network a Darkpanel module owns cannot be removed here", async () => {
  ntRender()
  await screen.findByRole("table", { name: "Docker networks" })
  expect(screen.getByRole("button", { name: "Remove darkpanel-tunnel" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Remove bridge" }).hasAttribute("disabled")).toBe(false)
})

test("the create form needs a name and plans docker.network.create with only the fields set", async () => {
  const { calls } = ntRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Create network" }))
  const form = await screen.findByRole("form", { name: "Network" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Name"), " frontend ")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.type(within(form).getByLabelText("Subnet"), "10.20.0.0/24")
  await user.click(within(form).getByRole("checkbox", { name: "Internal" }))
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Create network frontend" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/hosts/h1/actions/docker.network.create/v1/plan")
  expect(ntPlanBodies(calls)).toEqual([{ input: { name: "frontend", subnet: "10.20.0.0/24", internal: true } }])
})

test("every create field reaches the input", async () => {
  const { calls } = ntRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Create network" }))
  const form = await screen.findByRole("form", { name: "Network" })
  await user.type(within(form).getByLabelText("Name"), "mesh")
  await user.type(within(form).getByLabelText("Driver"), "macvlan")
  await user.type(within(form).getByLabelText("Subnet"), "192.168.50.0/24")
  await user.type(within(form).getByLabelText("Gateway"), "192.168.50.1")
  await user.click(within(form).getByRole("checkbox", { name: "Internal" }))
  await user.click(within(form).getByRole("checkbox", { name: "Attachable" }))
  await user.click(within(form).getByRole("checkbox", { name: "IPv6" }))
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await screen.findByRole("dialog", { name: "Create network mesh" })
  await waitFor(() => expect(ntPlanBodies(calls)).toHaveLength(1))
  expect(ntPlanBodies(calls)).toEqual([
    { input: { name: "mesh", driver: "macvlan", internal: true, attachable: true, ipv6: true, subnet: "192.168.50.0/24", gateway: "192.168.50.1" } },
  ])
})

test("the create form starts empty each time it opens", async () => {
  ntRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Create network" }))
  await user.type(within(await screen.findByRole("form", { name: "Network" })).getByLabelText("Name"), "frontend")
  await user.click(screen.getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Create network frontend" })
  await within(dialog).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Create network" }))
  expect((within(await screen.findByRole("form", { name: "Network" })).getByLabelText("Name") as HTMLInputElement).value).toBe("")
})

test("the table has no accessibility violations", async () => {
  ntRender()
  const table = await screen.findByRole("table", { name: "Docker networks" })
  expect(await axe(table)).toHaveNoViolations()
})
