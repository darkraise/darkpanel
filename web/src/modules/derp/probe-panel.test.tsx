import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { probeFixture } from "./fixtures"
import { ProbePanel } from "./probe-panel"
import type { ServerProbe } from "./types"

const PP_PROBE = "POST /api/v1/actions/derp.server.probe/v1/read"

const ppProbe: ServerProbe = {
  ...probeFixture,
  hostId: "h2",
  regionId: 900,
  regionCode: "fra-x",
  server: { ...probeFixture.server, hostName: "203.0.113.10", derperVersion: "v1.104.1", unitActive: true },
  sources: [
    {
      source: "controller",
      name: "controller",
      tls: { ok: true, latencyMs: 12.34, error: "" },
      stun: { ok: true, latencyMs: 8.5, error: "" },
      region: null,
      error: "",
    },
    {
      source: "h3",
      name: "web-1",
      tls: { ok: true, latencyMs: 40, error: "" },
      stun: { ok: false, latencyMs: 0, error: "timeout" },
      region: { info: ["DERP server answered"], warnings: ["no STUN reply"], errors: ["TLS name mismatch"] },
      error: "",
    },
  ],
  nodes: [
    { name: "web-1", address: "100.64.0.7", hostId: "h3", visible: true, preferred: "fra-x", observed: true },
    { name: "laptop", address: "100.64.0.8", hostId: "", visible: false, preferred: "", observed: false },
  ],
  ready: true,
  warnings: [],
}

function ppCells(row: HTMLElement | undefined): (string | null)[] {
  return within(row as HTMLElement).getAllByRole("cell").map((c) => c.textContent)
}

test("reads the probe of the host and says whether the region can be published", async () => {
  const calls = stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  expect(await screen.findByText("Ready to publish")).toBeTruthy()
  expect(calls.filter((c) => c.method === "POST").map((c) => c.body)).toEqual([{ input: { hostId: "h2" } }])
  expect(screen.getByText("Region: fra-x (900)")).toBeTruthy()
  expect(screen.getByText("203.0.113.10, derper v1.104.1, unit active")).toBeTruthy()
  expect(screen.queryByRole("list", { name: "Warnings" })).toBeNull()
})

test("lists the probe's warnings and says when the server is not ready", async () => {
  const warning = "no agent other than the DERP host probed 203.0.113.10; only the controller's probe backs the region"
  stubFetch({ [PP_PROBE]: { body: { output: { ...ppProbe, ready: false, regionId: 0, regionCode: "", warnings: [warning] } } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  expect(await screen.findByText("Not ready")).toBeTruthy()
  expect(screen.getByText("Region: not published")).toBeTruthy()
  expect(within(screen.getByRole("list", { name: "Warnings" })).getAllByRole("listitem").map((i) => i.textContent)).toEqual([warning])
  expect(screen.queryByText("Ready to publish")).toBeNull()
})

test("shows the TLS and STUN result of each source", async () => {
  stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  const table = await screen.findByRole("table", { name: "Probe sources" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Source", "TLS", "STUN"])
  const rows = within(table).getAllByRole("row")
  expect(ppCells(rows[1])).toEqual(["controller", "OK, 12.3 ms", "OK, 8.5 ms"])
  expect(ppCells(rows[2])).toEqual(["web-1", "OK, 40.0 ms", "Failed: timeout"])
})

test("labels the region report as connectivity diagnostics and shows it only for the sources that ran it", async () => {
  stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  const report = await screen.findByRole("region", { name: "Connectivity diagnostics from web-1" })
  expect(within(report).getByText("DERP server answered")).toBeTruthy()
  expect(within(report).getByText("no STUN reply")).toBeTruthy()
  expect(within(report).getByText("TLS name mismatch")).toBeTruthy()
  expect(screen.queryByRole("region", { name: "Connectivity diagnostics from controller" })).toBeNull()
  expect(screen.getByText(/do not prove that two nodes can relay through it/)).toBeTruthy()
})

test("shows whether each node is visible, its preferred region and whether it is relayed through this one", async () => {
  stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  const table = await screen.findByRole("table", { name: "Nodes" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
    "Node",
    "Address",
    "Visible",
    "Preferred region",
    "Relayed through this region",
  ])
  const rows = within(table).getAllByRole("row")
  expect(ppCells(rows[1])).toEqual(["web-1", "100.64.0.7", "Yes", "fra-x", "Yes"])
  expect(ppCells(rows[2])).toEqual(["laptop", "100.64.0.8", "No", "Unknown", "No"])
})

test("Probe again reads the probe once more", async () => {
  const calls = stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  await screen.findByText("Ready to publish")
  expect(calls.filter((c) => c.path.endsWith("/read"))).toHaveLength(1)
  await userEvent.setup().click(screen.getByRole("button", { name: "Probe again" }))
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/read"))).toHaveLength(2))
})

test("a refused read and an answer that is not a probe are errors", async () => {
  stubFetch({
    [PP_PROBE]: {
      status: 409,
      body: { type: "about:blank", title: "Not ready", status: 409, detail: "no DERP server is installed on web-1", code: "derp.not_ready" },
    },
  })
  const { unmount } = renderWithProviders(<ProbePanel hostId="h2" />)
  expect((await screen.findByRole("alert")).textContent).toBe("no DERP server is installed on web-1")
  expect(screen.getByRole("button", { name: "Probe again" })).toBeTruthy()
  unmount()

  stubFetch({ [PP_PROBE]: { body: { output: { sources: "none" } } } })
  renderWithProviders(<ProbePanel hostId="h2" />)
  expect((await screen.findByRole("alert")).textContent).toBe("The controller answered with an unexpected probe result.")
})

test("the panel has no accessibility violations", async () => {
  stubFetch({ [PP_PROBE]: { body: { output: ppProbe } } })
  const { container } = renderWithProviders(<ProbePanel hostId="h2" />)
  await screen.findByText("Ready to publish")
  expect(await axe(container)).toHaveNoViolations()
})
