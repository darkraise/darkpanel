import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { DerpPage } from "./derp-page"
import { probeFixture, serversFixture } from "./fixtures"

const DP_LIST = "POST /api/v1/actions/derp.server.list/v1/read"
const DP_PROBE = "POST /api/v1/actions/derp.server.probe/v1/read"
const DP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const DP_ONE = "01JD0000000000000000000011"
const DP_TWO = "01JD0000000000000000000012"

const dpServers: StubResponse = { body: { output: serversFixture } }
const dpProbe: StubResponse = { body: { output: probeFixture } }

const dpHosts: StubResponse = {
  body: { output: { hosts: [{ id: "h2", name: "web-1", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null }] } },
}

function dpPlan(action: string): StubResponse {
  const plan: Plan = {
    planId: "p1",
    hostId: null,
    action,
    version: 1,
    inputHash: "h",
    diff: [{ op: "~", text: "change the DERP setup" }],
    guards: [],
    expiresAt: "2026-11-02T13:00:00Z",
    confirmation: null,
  }
  return { body: plan }
}

const dpApplied = { id: "c1", hostId: null, state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function dpBodies(calls: FetchCall[], suffix: string): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith(suffix)).map((c) => c.body)
}

function dpRow(host: string): HTMLElement {
  const row = screen.getByText(host).closest("tr")
  if (row === null) throw new Error(`no row for ${host}`)
  return row
}

test("reads derp.server.list on the controller and lists the servers with their health", async () => {
  const calls = stubFetch({ [DP_LIST]: dpServers })
  renderWithProviders(<DerpPage />)
  expect(screen.getByLabelText("Loading the DERP servers")).toBeTruthy()
  await screen.findByRole("table", { name: "DERP servers" })
  expect(dpBodies(calls, "derp.server.list/v1/read")).toEqual([{ input: {} }])
  expect(calls.some((c) => c.path.includes("/hosts/"))).toBe(false)
  expect(within(dpRow("derp-1")).getByText("ok")).toBeTruthy()
  expect(within(dpRow("derp-2")).getByText("degraded")).toBeTruthy()
})

test("a refused list read shows its message and keeps the deploy button", async () => {
  stubFetch({
    [DP_LIST]: {
      status: 503,
      body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the controller cannot read the servers", code: "unavailable" },
    },
  })
  renderWithProviders(<DerpPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("the controller cannot read the servers")
  expect(screen.getByRole("button", { name: "Deploy a DERP server" })).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("an answer that is not a server list is an error", async () => {
  stubFetch({ [DP_LIST]: { body: { output: { servers: "none" } } } })
  renderWithProviders(<DerpPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("The controller answered with an unexpected server list.")
})

test("no server shows the empty state, the deploy button and no region section", async () => {
  stubFetch({ [DP_LIST]: { body: { output: { servers: [], availableVersion: "v1.104.1" } } } })
  renderWithProviders(<DerpPage />)
  expect(await screen.findByText("No DERP servers")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Deploy a DERP server" })).toBeTruthy()
  expect(screen.queryByRole("region", { name: "Published regions" })).toBeNull()
})

test("the deploy button opens the deploy dialog and Cancel closes it", async () => {
  stubFetch({ [DP_LIST]: dpServers, [DP_HOSTS]: dpHosts })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "DERP servers" })
  await user.click(screen.getByRole("button", { name: "Deploy a DERP server" }))
  const dialog = await screen.findByRole("dialog", { name: "Deploy a DERP server" })
  await within(dialog).findByRole("radio", { name: "web-1" })
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})

test("lists the published regions with a way to remove each, and no section without one", async () => {
  stubFetch({ [DP_LIST]: dpServers })
  const first = renderWithProviders(<DerpPage />)
  const section = await screen.findByRole("region", { name: "Published regions" })
  expect(within(section).getByText("mydc (900) on derp-1")).toBeTruthy()
  expect(within(section).getAllByRole("listitem")).toHaveLength(1)
  expect(within(section).getByRole("button", { name: "Remove region mydc (900)" })).toBeTruthy()
  first.unmount()

  stubFetch({ [DP_LIST]: { body: { output: { ...serversFixture, servers: [serversFixture.servers[1]] } } } })
  renderWithProviders(<DerpPage />)
  await screen.findByRole("table", { name: "DERP servers" })
  expect(screen.queryByRole("region", { name: "Published regions" })).toBeNull()
})

test("Probe shows the probe panel of that host and Close probe hides it", async () => {
  const calls = stubFetch({ [DP_LIST]: dpServers, [DP_PROBE]: dpProbe })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Probe derp-1" }))
  const panel = await screen.findByRole("region", { name: "Probe of derp-1" })
  expect(await within(panel).findByText("Ready to publish")).toBeTruthy()
  expect(dpBodies(calls, "derp.server.probe/v1/read")).toEqual([{ input: { hostId: DP_ONE } }])
  await user.click(within(panel).getByRole("button", { name: "Close probe" }))
  expect(screen.queryByRole("region", { name: "Probe of derp-1" })).toBeNull()
  expect(screen.getByRole("table", { name: "DERP servers" })).toBeTruthy()
})

test("probing another server replaces the panel", async () => {
  const calls = stubFetch({ [DP_LIST]: dpServers, [DP_PROBE]: dpProbe })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Probe derp-1" }))
  await screen.findByRole("region", { name: "Probe of derp-1" })
  await user.click(screen.getByRole("button", { name: "Probe derp-2" }))
  await screen.findByRole("region", { name: "Probe of derp-2" })
  expect(screen.queryByRole("region", { name: "Probe of derp-1" })).toBeNull()
  await waitFor(() => expect(dpBodies(calls, "derp.server.probe/v1/read")).toEqual([{ input: { hostId: DP_ONE } }, { input: { hostId: DP_TWO } }]))
})

test("Region reads the probe of the host and opens the region dialog with it", async () => {
  const warning = "no agent other than the DERP host probed 203.0.113.7; only the controller's probe backs the region"
  const calls = stubFetch({ [DP_LIST]: dpServers, [DP_PROBE]: { body: { output: { ...probeFixture, warnings: [warning] } } } })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Region of derp-1" }))
  const dialog = await screen.findByRole("dialog", { name: "Publish a DERP region on derp-1" })
  expect(dpBodies(calls, "derp.server.probe/v1/read")).toEqual([{ input: { hostId: DP_ONE } }])
  expect((within(dialog).getByLabelText("Region code") as HTMLInputElement).value).toBe("mydc")
  expect(within(dialog).getByText(warning)).toBeTruthy()
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})

test("each opening of the region dialog reads the probe again", async () => {
  let reads = 0
  stubFetch({
    [DP_LIST]: dpServers,
    [DP_PROBE]: () => {
      reads += 1
      return { body: { output: { ...probeFixture, warnings: reads === 1 ? [] : ["a warning of the second probe"] } } }
    },
  })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Region of derp-1" }))
  const first = await screen.findByRole("dialog", { name: "Publish a DERP region on derp-1" })
  expect(within(first).queryByText("a warning of the second probe")).toBeNull()
  await user.click(within(first).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Region of derp-1" }))
  const second = await screen.findByRole("dialog", { name: "Publish a DERP region on derp-1" })
  expect(await within(second).findByText("a warning of the second probe")).toBeTruthy()
  expect(reads).toBe(2)
})

test("a refused probe read shows its message instead of the region dialog, and Close dismisses it", async () => {
  stubFetch({
    [DP_LIST]: dpServers,
    [DP_PROBE]: {
      status: 409,
      body: { type: "about:blank", title: "Not ready", status: 409, detail: "no DERP server is installed on derp-1", code: "derp.not_ready" },
    },
  })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Region of derp-1" }))
  expect((await screen.findByRole("alert")).textContent).toBe("no DERP server is installed on derp-1")
  expect(screen.queryByRole("dialog")).toBeNull()
  await user.click(screen.getByRole("button", { name: "Close" }))
  expect(screen.queryByRole("alert")).toBeNull()
})

const dpChanges = [
  { button: "Upgrade derp-2", action: "derp.server.upgrade", title: "Upgrade derper on derp-2", input: { hostId: DP_TWO } },
  { button: "Rotate certificate of derp-1", action: "derp.cert.rotate", title: "Rotate the certificate of derp-1", input: { hostId: DP_ONE } },
  { button: "Remove derp-1", action: "derp.server.remove", title: "Remove the DERP server on derp-1", input: { hostId: DP_ONE } },
  { button: "Remove region mydc (900)", action: "derp.region.remove", title: "Remove DERP region mydc (900)", input: { regionId: 900 } },
]

test.each(dpChanges)("$button plans $action on the controller with its input", async ({ button, action, title, input }) => {
  const calls = stubFetch({ [DP_LIST]: dpServers, [`POST /api/v1/actions/${action}/v1/plan`]: dpPlan(action) })
  renderWithProviders(<DerpPage />)
  await userEvent.setup().click(await screen.findByRole("button", { name: button }))
  const dialog = await screen.findByRole("dialog", { name: title })
  expect(within(dialog).getByText(`${action}/v1 on the controller`)).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ change the DERP setup")
  expect(dpBodies(calls, `${action}/v1/plan`)).toEqual([{ input }])
})

test("a closed change reads the list again, and the probe panel of a server that is gone closes with it", async () => {
  let listReads = 0
  stubFetch({
    [DP_LIST]: () => {
      listReads += 1
      return { body: { output: listReads === 1 ? serversFixture : { ...serversFixture, servers: [serversFixture.servers[1]] } } }
    },
    [DP_PROBE]: dpProbe,
    "POST /api/v1/actions/derp.server.remove/v1/plan": dpPlan("derp.server.remove"),
    "POST /api/v1/actions/derp.server.remove/v1/apply": { body: dpApplied },
    "GET /api/v1/changes/c1": { body: dpApplied },
  })
  renderWithProviders(<DerpPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Probe derp-1" }))
  await screen.findByRole("region", { name: "Probe of derp-1" })
  await user.click(screen.getByRole("button", { name: "Remove derp-1" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove the DERP server on derp-1" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(listReads).toBe(2))
  await waitFor(() => expect(screen.queryByText("derp-1")).toBeNull())
  expect(screen.queryByRole("region", { name: "Probe of derp-1" })).toBeNull()
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(screen.getByText("derp-2")).toBeTruthy()
})

test("the page has no accessibility violations", async () => {
  stubFetch({ [DP_LIST]: dpServers, [DP_PROBE]: dpProbe })
  const { container } = renderWithProviders(<DerpPage />)
  await screen.findByRole("region", { name: "Published regions" })
  expect(await axe(container)).toHaveNoViolations()
  await userEvent.setup().click(screen.getByRole("button", { name: "Probe derp-1" }))
  const panel = await screen.findByRole("region", { name: "Probe of derp-1" })
  await within(panel).findByText("Ready to publish")
  expect(await axe(container)).toHaveNoViolations()
})
