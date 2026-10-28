import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwExposure, fwStatus } from "./fixtures"
import FirewallRoutes from "./routes"
import type { ExposureRow, ExposureVerdict } from "./types"

const EXPOSURE = "GET /api/v1/hosts/h1/resources/firewall/exposure"
const STATUS = "GET /api/v1/hosts/h1/resources/firewall/status"

function epVerdict(cls: ExposureVerdict["class"], verdict: ExposureVerdict["verdict"], over: Partial<ExposureVerdict> = {}): ExposureVerdict {
  return { class: cls, verdict, ...over }
}

function epRows(): ExposureRow[] {
  return [
    {
      proto: "tcp", addr: "0.0.0.0", port: 22, family: "v4", process: "sshd",
      classes: [
        epVerdict("internet", "open", { decidedBy: "ufw-user-input#2 allow 22/tcp" }),
        epVerdict("private", "open", { range: "10.0.0.0/8", decidedBy: "ufw-user-input#2 allow 22/tcp" }),
        epVerdict("tailnet", "open", { note: "host firewall; tailnet policy not evaluated" }),
        epVerdict("localhost", "open"),
      ],
    },
    {
      proto: "tcp", addr: "0.0.0.0", port: 8080, family: "v4",
      container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false },
      classes: [
        epVerdict("internet", "partial", {
          allowed: [
            { sources: ["203.0.113.0/24"], ifaces: ["eth0"], decidedBy: "dp-b-0123456789ab line 2" },
            { sources: ["198.51.100.0/24", "192.0.2.0/24"], srcPorts: ["53"], decidedBy: "dp-b-0123456789ab line 3" },
          ],
        }),
        epVerdict("private", "open", { range: "10.0.0.0/8", decidedBy: "DOCKER-USER line 5" }),
        epVerdict("tailnet", "closed", { decidedBy: "Tailscale shields-up" }),
      ],
    },
    {
      proto: "udp", addr: "::", port: 5353, family: "v6",
      container: { name: "dns", binding: { selector: "dns", hostPort: 5353, proto: "udp" }, containerPort: 53, proxied: true },
      classes: [epVerdict("internet", "closed", { decidedBy: "ufw6-user-input policy" })],
    },
    {
      proto: "tcp", addr: "127.0.0.1", port: 5432, family: "v4", process: "postgres",
      classes: [epVerdict("internet", "closed", { decidedBy: "bound to loopback" }), epVerdict("localhost", "open")],
    },
    {
      proto: "tcp", addr: "0.0.0.0", port: 3000, family: "v4", process: "node",
      classes: [epVerdict("internet", "unknown", { note: "mark set outside netfilter" })],
    },
  ]
}

function epRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({ [EXPOSURE]: { body: fwExposure({ rows: epRows() }) }, [STATUS]: { body: fwStatus() }, ...routes })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/*" element={<FirewallRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/firewall/exposure" },
  )
  return { calls, ...result }
}

async function epTable() {
  return screen.findByRole("table", { name: "Exposure" })
}

// epPorts lists the rows shown as protocol/port, in table order.
function epPorts(table: HTMLElement): string[] {
  return within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => {
      const cells = within(r).getAllByRole("cell")
      return `${cells[0]?.textContent ?? ""}/${cells[2]?.textContent ?? ""}`
    })
}

function epItems(table: HTMLElement, label: string): string[] {
  return within(within(table).getByRole("list", { name: label })).getAllByRole("listitem").map((li) => li.textContent ?? "")
}

test("lists every listening port with its listener, in the server's order", async () => {
  epRender()
  const table = await epTable()
  const rows = within(table).getAllByRole("row").slice(1).map((r) => within(r).getAllByRole("cell").slice(0, 5).map((c) => c.textContent))
  expect(rows).toEqual([
    ["tcp", "0.0.0.0", "22", "v4", "sshd"],
    ["tcp", "0.0.0.0", "8080", "v4", "container web, port 80"],
    ["udp", "::", "5353", "v6", "container dns, port 53 (docker-proxy)"],
    ["tcp", "127.0.0.1", "5432", "v4", "postgres"],
    ["tcp", "0.0.0.0", "3000", "v4", "node"],
  ])
})

test("shows each source class's verdict with what decides it", async () => {
  epRender()
  const table = await epTable()
  expect(epItems(table, "Reachability of tcp 0.0.0.0 port 22")).toEqual([
    "internet: open — decided by ufw-user-input#2 allow 22/tcp",
    "private 10.0.0.0/8: open — decided by ufw-user-input#2 allow 22/tcp",
    "tailnet: open — host firewall; tailnet policy not evaluated",
    "localhost: open",
  ])
  expect(epItems(table, "Reachability of tcp 0.0.0.0 port 8080")).toEqual([
    "internet: partial — allowed from 203.0.113.0/24 on eth0 (dp-b-0123456789ab line 2); allowed from 198.51.100.0/24, 192.0.2.0/24 from source ports 53 (dp-b-0123456789ab line 3)",
    "private 10.0.0.0/8: open — decided by DOCKER-USER line 5",
    "tailnet: closed — decided by Tailscale shields-up",
  ])
  expect(epItems(table, "Reachability of tcp 127.0.0.1 port 5432")).toEqual(["internet: closed — decided by bound to loopback", "localhost: open"])
  expect(epItems(table, "Reachability of tcp 0.0.0.0 port 3000")).toEqual(["internet: unknown — mark set outside netfilter"])
})

test("the protocol and family filters narrow the rows", async () => {
  epRender()
  const user = userEvent.setup()
  await epTable()
  await user.click(screen.getByRole("radio", { name: "udp" }))
  expect(epPorts(await epTable())).toEqual(["udp/5353"])
  await user.click(screen.getByRole("radio", { name: "tcp" }))
  expect(epPorts(await epTable())).toEqual(["tcp/22", "tcp/8080", "tcp/5432", "tcp/3000"])
  await user.click(screen.getByRole("radio", { name: "v6" }))
  expect(screen.getByText("No rows match the filters")).toBeTruthy()
  await user.click(screen.getByRole("radio", { name: "All protocols" }))
  expect(epPorts(await epTable())).toEqual(["udp/5353"])
  await user.click(screen.getByRole("radio", { name: "All families" }))
  expect(epPorts(await epTable())).toHaveLength(5)
})

test("the internet filter keeps the ports that are open, partly open or undecided for the internet", async () => {
  epRender()
  const user = userEvent.setup()
  await epTable()
  await user.click(screen.getByRole("checkbox", { name: "Only ports the internet can reach" }))
  expect(epPorts(await epTable())).toEqual(["tcp/22", "tcp/8080", "tcp/3000"])
  await user.click(screen.getByRole("radio", { name: "udp" }))
  expect(screen.getByText("No rows match the filters")).toBeTruthy()
})

test("the notes are listed above the table", async () => {
  epRender({ [EXPOSURE]: { body: fwExposure({ rows: epRows(), notes: ["UFW's files and the loaded rules are out of sync; verdicts may be stale", "Docker is unavailable"] }) } })
  await epTable()
  const notes = within(screen.getByRole("list", { name: "Exposure notes" })).getAllByRole("listitem")
  expect(notes.map((n) => n.textContent)).toEqual(["UFW's files and the loaded rules are out of sync; verdicts may be stale", "Docker is unavailable"])
})

test("a host with no listening ports shows an empty state and its notes", async () => {
  epRender({ [EXPOSURE]: { body: fwExposure({ rows: [], notes: ["Docker is unavailable"] }) } })
  expect((await screen.findByText("No listening ports")).textContent).toBe("No listening ports")
  expect(screen.getByText("Docker is unavailable")).toBeTruthy()
  expect(screen.queryByRole("radio")).toBeNull()
})

test("a failed read shows the server's reason", async () => {
  epRender({ [EXPOSURE]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})

test("the back link returns to the firewall page", async () => {
  epRender()
  const user = userEvent.setup()
  await epTable()
  await user.click(screen.getByRole("link", { name: "Back to the firewall" }))
  await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/hosts/h1/firewall"))
  expect(await screen.findByLabelText("Configured state")).toBeTruthy()
})

test("the exposure table has no accessibility violations", async () => {
  const { container } = epRender()
  await epTable()
  expect(await axe(container)).toHaveNoViolations()
})
