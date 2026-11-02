import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders } from "@/test/utils"
import { serversFixture } from "./fixtures"
import { ServersTable } from "./servers-table"
import type { DerpServers, DerpServerView } from "./types"

const stIP: DerpServerView = {
  hostId: "h2",
  host: "derp-1",
  mode: "ip",
  hostName: "203.0.113.10",
  regionId: 900,
  regionCode: "fra-x",
  derperVersion: "v1.104.1",
  certName: "sha256-raw:ab",
  certNotAfter: "2027-06-01T00:00:00Z",
  unitActive: true,
  health: "ok",
  reasons: [],
  checkedAt: "2026-11-02T11:59:00Z",
  error: "",
}

const stLE: DerpServerView = {
  hostId: "h3",
  host: "derp-2",
  mode: "letsencrypt",
  hostName: "derp.example.com",
  regionId: 0,
  regionCode: "",
  derperVersion: "v1.103.0",
  certName: "",
  certNotAfter: null,
  unitActive: true,
  health: "degraded",
  reasons: ["The server was last checked 12 minutes ago."],
  checkedAt: "2026-11-02T11:48:00Z",
  error: "",
}

const stServers: DerpServers = { servers: [stIP, stLE], availableVersion: "v1.104.1" }

function stRow(host: string): HTMLElement {
  const row = screen.getByText(host).closest("tr")
  if (row === null) throw new Error(`no row for ${host}`)
  return row
}

test("renders a row for every server of the shared fixture, with its health", () => {
  renderWithProviders(<ServersTable servers={serversFixture} onAction={() => {}} />)
  const table = screen.getByRole("table", { name: "DERP servers" })
  expect(within(table).getAllByRole("row")).toHaveLength(serversFixture.servers.length + 1)
  for (const s of serversFixture.servers) {
    expect(within(stRow(s.host)).getByText(s.health)).toBeTruthy()
  }
})

test("shows each server's mode, region, version, certificate and health", () => {
  renderWithProviders(<ServersTable servers={stServers} onAction={() => {}} />)
  const headers = within(screen.getByRole("table", { name: "DERP servers" })).getAllByRole("columnheader")
  expect(headers.map((h) => h.textContent)).toEqual(["Host", "Mode", "Server name", "Region", "Version", "Certificate", "Health", "Actions"])

  const ip = within(stRow("derp-1"))
  expect(ip.getByText("IP certificate")).toBeTruthy()
  expect(ip.getByText("203.0.113.10")).toBeTruthy()
  expect(ip.getByText("fra-x (900)")).toBeTruthy()
  expect(ip.getByText("v1.104.1")).toBeTruthy()
  expect(ip.queryByText(/available/)).toBeNull()
  expect(ip.getByText(`Expires ${formatTime("2027-06-01T00:00:00Z")}`)).toBeTruthy()
  expect(ip.getByText("ok")).toBeTruthy()

  const le = within(stRow("derp-2"))
  expect(le.getByText("Let's Encrypt")).toBeTruthy()
  expect(le.getByText("derp.example.com")).toBeTruthy()
  expect(le.getByText("Not published")).toBeTruthy()
  expect(le.getByText("v1.103.0")).toBeTruthy()
  expect(le.getByText("v1.104.1 available")).toBeTruthy()
  expect(le.getByText("Renewed by derper")).toBeTruthy()
  expect(le.getByText("degraded")).toBeTruthy()
  const reasons = le.getByRole("list", { name: "Reasons for derp-2" })
  expect(within(reasons).getAllByRole("listitem").map((i) => i.textContent)).toEqual(["The server was last checked 12 minutes ago."])
  expect(ip.queryByRole("list")).toBeNull()
})

test("a broken server lists every reason and the failed check", () => {
  const broken: DerpServerView = {
    ...stIP,
    health: "broken",
    unitActive: false,
    reasons: ["The derper unit is not active.", "The certificate expired."],
    error: "agent unreachable",
  }
  renderWithProviders(<ServersTable servers={{ servers: [broken], availableVersion: "v1.104.1" }} onAction={() => {}} />)
  const row = within(stRow("derp-1"))
  expect(row.getByText("broken")).toBeTruthy()
  expect(within(row.getByRole("list", { name: "Reasons for derp-1" })).getAllByRole("listitem").map((i) => i.textContent)).toEqual([
    "The derper unit is not active.",
    "The certificate expired.",
  ])
  expect(row.getByText("Last check failed: agent unreachable")).toBeTruthy()
})

test("offers an upgrade only for an old version and a rotation only for an IP certificate", () => {
  renderWithProviders(<ServersTable servers={stServers} onAction={() => {}} />)
  const ip = within(stRow("derp-1"))
  expect(ip.getByRole("button", { name: "Probe derp-1" })).toBeTruthy()
  expect(ip.getByRole("button", { name: "Region of derp-1" })).toBeTruthy()
  expect(ip.getByRole("button", { name: "Rotate certificate of derp-1" })).toBeTruthy()
  expect(ip.getByRole("button", { name: "Remove derp-1" })).toBeTruthy()
  expect(ip.queryByRole("button", { name: "Upgrade derp-1" })).toBeNull()
  const le = within(stRow("derp-2"))
  expect(le.getByRole("button", { name: "Upgrade derp-2" })).toBeTruthy()
  expect(le.queryByRole("button", { name: "Rotate certificate of derp-2" })).toBeNull()
})

const stActions = [
  { button: "Probe derp-1", action: "probe", server: stIP },
  { button: "Region of derp-1", action: "region", server: stIP },
  { button: "Rotate certificate of derp-1", action: "rotate", server: stIP },
  { button: "Remove derp-1", action: "remove", server: stIP },
  { button: "Upgrade derp-2", action: "upgrade", server: stLE },
]

test.each(stActions)("$button reports $action with its server", async ({ button, action, server }) => {
  const onAction = vi.fn()
  renderWithProviders(<ServersTable servers={stServers} onAction={onAction} />)
  await userEvent.setup().click(screen.getByRole("button", { name: button }))
  expect(onAction).toHaveBeenCalledTimes(1)
  expect(onAction).toHaveBeenCalledWith(action, server)
})

test("no server is an empty state", () => {
  renderWithProviders(<ServersTable servers={{ servers: [], availableVersion: "v1.104.1" }} onAction={() => {}} />)
  expect(screen.getByText("No DERP servers")).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("the table has no accessibility violations", async () => {
  const { container } = renderWithProviders(<ServersTable servers={stServers} onAction={() => {}} />)
  expect(await axe(container)).toHaveNoViolations()
})
