import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import TailscaleRoutes from "./routes"
import type { TailscalePeer, TailscaleStatus } from "./types"

// The panels have their own tests. Here each one shows the props the page
// gives it.
vi.mock("./status-panel", () => ({
  StatusPanel: (props: { hostId: string }) => <p data-testid="status-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("./peers-table", () => ({
  PeersTable: (props: { peers: unknown[] }) => <p data-testid="peers-table">{JSON.stringify(props)}</p>,
}))
vi.mock("./latency-panel", () => ({
  LatencyPanel: (props: { hostId: string }) => <p data-testid="latency-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("./prefs-form", () => ({
  PrefsForm: (props: { hostId: string; status: unknown; prefs: unknown }) => <p data-testid="prefs-form">{JSON.stringify(props)}</p>,
}))
vi.mock("./node-controls", () => ({
  NodeControls: (props: { hostId: string; status: unknown }) => <p data-testid="node-controls">{JSON.stringify(props)}</p>,
}))
vi.mock("./package-panel", () => ({
  PackagePanel: (props: { hostId: string; available: boolean }) => <p data-testid="package-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("@/modules/tailnet/tailnet-change", () => ({
  TailnetChange: ({ action, title, input, onClose }: { action: string; title: string; input: Record<string, unknown> | null; onClose: () => void }) =>
    input === null ? null : (
      <div role="dialog" aria-label={title}>
        <p data-testid="join-action">{action}</p>
        <p data-testid="join-input">{JSON.stringify(input)}</p>
        <button onClick={onClose}>Close the dialog</button>
      </div>
    ),
}))

const TP_STATUS = "GET /api/v1/hosts/h1/resources/tailscale/status"

function tpPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n-peer", hostName: "db", dnsName: "db.tail1234.ts.net", os: "linux", addresses: ["100.64.0.7"], tags: [],
    online: true, connection: "direct", endpoint: "203.0.113.9:41641", region: "", exitNode: false, exitNodeOption: false,
    lastHandshake: null, ...over,
  }
}

function tpStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1", backendState: "Running",
    self: { id: "n-self", hostName: "web", dnsName: "web.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: ["tag:server"] },
    tailnet: "example.com", magicDNSSuffix: "tail1234.ts.net", preferredDerp: "fra", runSSH: true,
    peers: [tpPeer()], health: [], ...over,
  }
}

const tpUnavailable: StubHandler = {
  status: 503,
  body: { type: "about:blank", title: "Tailscale unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
}

function tpRender(path: string, status: StubHandler = { body: tpStatus() }) {
  const calls = stubFetch({ [TP_STATUS]: status })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/tailscale/*" element={<TailscaleRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return { calls, ...result }
}

function tpProps(testId: string): Record<string, unknown> {
  return JSON.parse(screen.getByTestId(testId).textContent ?? "") as Record<string, unknown>
}

test("the page opens on the status tab with the status panel and the peers", async () => {
  tpRender("/hosts/h1/tailscale")
  expect(await screen.findByTestId("status-panel")).toBeTruthy()
  expect(tpProps("status-panel")).toEqual({ hostId: "h1" })
  expect(tpProps("peers-table")).toEqual({ peers: tpStatus().peers })
  expect(within(screen.getByRole("tablist")).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Status", "Latency", "Preferences", "Package"])
  expect(screen.getByRole("tab", { name: "Status", selected: true })).toBeTruthy()
  expect(screen.queryByTestId("latency-panel")).toBeNull()
  expect(screen.queryByRole("button", { name: "Join tailnet" })).toBeNull()
})

test("the Latency tab mounts the latency panel for the host", async () => {
  tpRender("/hosts/h1/tailscale")
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Latency" }))
  expect(tpProps("latency-panel")).toEqual({ hostId: "h1" })
  expect(screen.queryByTestId("status-panel")).toBeNull()
})

test("the Package tab tells the panel that tailscaled answers", async () => {
  tpRender("/hosts/h1/tailscale")
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Package" }))
  expect(tpProps("package-panel")).toEqual({ hostId: "h1", available: true })
})

test.each([
  ["the node's preferences", tpStatus({ prefs: { wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "n-exit", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true } }),
    { wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "n-exit", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true }],
  ["no preferences from an older agent", tpStatus({ backendState: "NeedsLogin", self: null, peers: [] }), {}],
])("the Preferences tab gives the form and the controls %s", async (_name, status, prefs) => {
  tpRender("/hosts/h1/tailscale", { body: status })
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Preferences" }))
  expect(tpProps("prefs-form")).toEqual({ hostId: "h1", status, prefs })
  expect(tpProps("node-controls")).toEqual({ hostId: "h1", status })
})

test("a node that is logged out offers to join the tailnet and reads its status again when the dialog closes", async () => {
  const { calls } = tpRender("/hosts/h1/tailscale", { body: tpStatus({ backendState: "NeedsLogin", self: null, peers: [] }) })
  const user = userEvent.setup()
  const join = await screen.findByRole("button", { name: "Join tailnet" })
  const tags = screen.getByRole("textbox", { name: "Tailscale tags" })
  expect(join).toHaveProperty("disabled", true)
  await user.type(tags, "server")
  expect(join).toHaveProperty("disabled", true)
  await user.clear(tags)
  await user.type(tags, "tag:server, tag:web")
  expect(join).toHaveProperty("disabled", false)
  await user.click(join)
  expect(screen.getByTestId("join-action").textContent).toBe("tailnet.node.join")
  expect(JSON.parse(screen.getByTestId("join-input").textContent ?? "")).toEqual({ hostId: "h1", tags: ["tag:server", "tag:web"] })
  const reads = () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/tailscale/status").length
  expect(reads()).toBe(1)
  await user.click(screen.getByRole("button", { name: "Close the dialog" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  await waitFor(() => expect(reads()).toBe(2))
})

test("a node whose tailscaled does not answer shows why, offers the install and joins with the package", async () => {
  tpRender("/hosts/h1/tailscale", tpUnavailable)
  expect(await screen.findByText("Tailscale is not available on this host: tailscaled does not answer on its socket")).toBeTruthy()
  expect(tpProps("package-panel")).toEqual({ hostId: "h1", available: false })
  expect(screen.queryByRole("tablist")).toBeNull()
  const user = userEvent.setup()
  await user.type(screen.getByRole("textbox", { name: "Tailscale tags" }), "tag:server")
  await user.click(screen.getByRole("button", { name: "Join tailnet" }))
  expect(JSON.parse(screen.getByTestId("join-input").textContent ?? "")).toEqual({ hostId: "h1", tags: ["tag:server"], install: true })
})

test("any other failed status read shows the server's reason and nothing else", async () => {
  tpRender("/hosts/h1/tailscale", {
    status: 409,
    body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the tailscale module is unavailable on this host", code: "module.unavailable" },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the tailscale module is unavailable on this host")
  expect(screen.queryByTestId("package-panel")).toBeNull()
  expect(screen.queryByRole("button", { name: "Join tailnet" })).toBeNull()
})

test("an unknown subpage is answered", () => {
  tpRender("/hosts/h1/tailscale/nope")
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})

test("the Tailscale page has no accessibility violations", async () => {
  const { container } = tpRender("/hosts/h1/tailscale", { body: tpStatus({ self: null }) })
  await screen.findByTestId("status-panel")
  expect(await axe(container)).toHaveNoViolations()
})
