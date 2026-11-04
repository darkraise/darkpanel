import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { paExposure, paStatus } from "./fixtures"
import { PanelPage } from "./panel-page"
import type { StatusOutput } from "./types"

const PP_STATUS = "POST /api/v1/actions/panel.exposure.status/v1/read"
const PP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PP_ADD_PLAN = "POST /api/v1/actions/panel.exposure.add/v1/plan"
const PP_REMOVE_PLAN = "POST /api/v1/actions/panel.exposure.remove/v1/plan"
const PP_REMOVE_APPLY = "POST /api/v1/actions/panel.exposure.remove/v1/apply"
const PP_TUNNEL = "0123abcd-0123-4abc-8def-0123456789ab"

function ppHost(id: string, isLocal: boolean): Host {
  return { id, name: id === "h1" ? "controller" : "web-2", isLocal, online: true, modules: [], ingestion: null }
}

function ppPlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1", hostId: null, action: parts[parts.length - 3] ?? "", version: 1, inputHash: "h",
      diff: [{ op: "+", text: "change" }], guards: [], expiresAt: "2026-11-04T13:00:00Z", confirmation: null,
    },
  }
}

function ppRender(hostId: string, status: StatusOutput, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [PP_STATUS]: { body: { output: status } },
    [PP_HOSTS]: { body: { output: { hosts: [ppHost("h1", true), ppHost("h2", false)] } } },
    [PP_ADD_PLAN]: ppPlan,
    [PP_REMOVE_PLAN]: ppPlan,
    [PP_REMOVE_APPLY]: { body: { id: "c1", hostId: null, state: "applying", class: "composite", error: null, output: null, revertDeadline: null } },
    ...routes,
  })
  const result = renderWithProviders(<PanelPage hostId={hostId} />)
  return { calls, ...result }
}

function ppPlanBodies(calls: FetchCall[], suffix: string): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith(suffix)).map((c) => c.body)
}

function ppReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.path === "/api/v1/actions/panel.exposure.status/v1/read").length
}

async function ppForm() {
  return await screen.findByRole("form", { name: "Expose the panel" })
}

test("the controller's host with no exposure offers the form, and the route defaults to NPM", async () => {
  ppRender("h1", paStatus())
  const form = await ppForm()
  expect(within(form).getByRole("radio", { name: "Nginx Proxy Manager", checked: true })).toBeTruthy()
  expect(within(form).getByRole("radio", { name: "Cloudflare tunnel", checked: false })).toBeTruthy()
  expect(within(form).getByLabelText("Public hostname")).toBeTruthy()
  expect(within(form).queryByLabelText("Tunnel id")).toBeNull()
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
})

test("an NPM exposure plans panel.exposure.add with the trimmed lower-case hostname and the host, and nothing else", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "  Panel.Example.org ")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel at panel.example.org" })
  expect(await within(dialog).findByLabelText("Planned changes")).toBeTruthy()
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toEqual([{ input: { via: "npm", hostname: "panel.example.org", hostId: "h1" } }])
})

test("a tunnel exposure asks for the tunnel and sends it with the port when one is typed", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.click(within(form).getByRole("radio", { name: "Cloudflare tunnel" }))
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Tunnel id"), PP_TUNNEL.toUpperCase())
  await user.type(within(form).getByLabelText("Port (optional)"), "9500")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Expose the panel at panel.example.org" })
  await waitFor(() => expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toHaveLength(1))
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")[0]).toEqual({
    input: { via: "tunnel", hostname: "panel.example.org", hostId: "h1", tunnel: PP_TUNNEL, port: 9500 },
  })
})

test("switching back to NPM drops the tunnel from the request", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.click(within(form).getByRole("radio", { name: "Cloudflare tunnel" }))
  await user.type(within(form).getByLabelText("Tunnel id"), PP_TUNNEL)
  await user.click(within(form).getByRole("radio", { name: "Nginx Proxy Manager" }))
  expect(within(form).queryByLabelText("Tunnel id")).toBeNull()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog")
  await waitFor(() => expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toHaveLength(1))
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")[0]).toEqual({ input: { via: "npm", hostname: "panel.example.org", hostId: "h1" } })
})

test.each(["80", "1023", "65536", "99999", "9443x", "-1", "1e3"])("the port %s is refused before anything is planned", async (port) => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.type(within(form).getByLabelText("Port (optional)"), port)
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
  expect(within(form).getByLabelText("Port (optional)").getAttribute("aria-invalid")).toBe("true")
  expect(within(form).getByText("Enter a whole number from 1024 to 65535.")).toBeTruthy()
  expect(ppPlanBodies(calls, "/plan")).toEqual([])
})

test.each(["1024", "65535"])("the port %s is accepted", async (port) => {
  const user = userEvent.setup()
  ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.type(within(form).getByLabelText("Port (optional)"), port)
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(false)
})

test("a host that is not the controller's gets no form and no plan", async () => {
  const { calls } = ppRender("h2", paStatus())
  expect(await screen.findByText("The panel can be exposed only from the controller's own host. Open the panel page of that host.")).toBeTruthy()
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
  expect(ppPlanBodies(calls, "/plan")).toEqual([])
})

test("an exposure shows its route, hostname, network and state in place of the form", async () => {
  ppRender("h1", paStatus(paExposure()))
  const list = await screen.findByLabelText("Panel exposure")
  const value = (label: string) => within(list).getByText(label).nextElementSibling?.textContent ?? null
  expect(value("Route")).toBe("Nginx Proxy Manager")
  expect(value("Hostname")).toBe("panel.example.org")
  expect(value("Network")).toBe("172.31.255.248/29")
  expect(value("Gateway")).toBe("172.31.255.249")
  expect(value("Proxy")).toBe("172.31.255.250")
  expect(value("Port")).toBe("9443")
  expect(value("State")).toBe("open")
  expect(within(list).queryByText("Tunnel")).toBeNull()
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
})

test("a tunnel exposure shows its tunnel, and an exposure that needs repair can still be removed", async () => {
  ppRender("h1", paStatus(paExposure({ via: "tunnel", tunnel: PP_TUNNEL, state: "needs_repair" })))
  const list = await screen.findByLabelText("Panel exposure")
  expect(within(list).getByText("Cloudflare tunnel")).toBeTruthy()
  expect(within(list).getByText("Tunnel").nextElementSibling?.textContent).toBe(PP_TUNNEL)
  expect(within(list).getByText("State").nextElementSibling?.textContent).toBe("needs_repair")
  expect(screen.getByRole("button", { name: "Remove exposure" }).hasAttribute("disabled")).toBe(false)
})

test("an exposure is removed from any host's page, planning panel.exposure.remove with an empty input on the controller", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h2", paStatus(paExposure()))
  await user.click(await screen.findByRole("button", { name: "Remove exposure" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove the panel exposure at panel.example.org" })
  expect(within(dialog).getByText("panel.exposure.remove/v1 on the controller")).toBeTruthy()
  expect(await within(dialog).findByLabelText("Planned changes")).toBeTruthy()
  expect(ppPlanBodies(calls, "/panel.exposure.remove/v1/plan")).toEqual([{ input: {} }])
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/actions/panel.exposure.remove/v1/apply")).toBe(true))
  expect(calls.find((c) => c.path === "/api/v1/actions/panel.exposure.remove/v1/apply")?.body).toEqual({ planId: "p1" })
})

test("closing the removal dialog reads the exposure again", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus(paExposure()))
  await user.click(await screen.findByRole("button", { name: "Remove exposure" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(ppReads(calls)).toBe(1)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(ppReads(calls)).toBe(2))
})

test("a failed status read is said and offers neither the form nor the removal", async () => {
  ppRender("h1", paStatus(), {
    [PP_STATUS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
  expect(screen.queryByRole("button", { name: "Remove exposure" })).toBeNull()
})

test("an answer that is not a status is said", async () => {
  ppRender("h1", paStatus(), { [PP_STATUS]: { body: { output: { exposed: true } } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The controller answered with an unexpected panel exposure status.")
})

test("a failed host list is said when the form would need it", async () => {
  ppRender("h1", paStatus(), {
    [PP_HOSTS]: { status: 500, body: { type: "about:blank", title: "Internal", status: 500, detail: "Internal error", code: "internal" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("Internal error")
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
})

test("the form and the exposure have no accessibility violations", async () => {
  const form = ppRender("h1", paStatus())
  await ppForm()
  expect(await axe(form.container)).toHaveNoViolations()
  form.unmount()
  const exposed = ppRender("h1", paStatus(paExposure()))
  await screen.findByRole("button", { name: "Remove exposure" })
  expect(await axe(exposed.container)).toHaveNoViolations()
})
