import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerDetail } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import type { Connector } from "./connector-types"
import { cfTunnel } from "./fixtures"

const CT_HOST = "/api/v1/hosts/h1"
const CT_TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const CT_CONNECTORS = `POST ${CT_HOST}/actions/cloudflare.connector.list/v1/read`
const CT_INSPECT = `POST ${CT_HOST}/actions/docker.container.inspect/v1/read`
const CT_NETWORKS = `GET ${CT_HOST}/resources/docker/networks`
const CT_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"

const ctContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function ctTunnels() {
  return [
    cfTunnel({ id: "t1", name: "darkpanel-a" }),
    cfTunnel({ id: "t2", name: "darkpanel-b" }),
    cfTunnel({ id: "t3", name: "darkpanel-c" }),
  ]
}

function ctConnector(tunnel: string, state: string): Connector {
  return { tunnel, container: "darkpanel-cloudflared-" + tunnel, containerId: "id-" + tunnel, state }
}

function ctDetail(): ContainerDetail {
  return dkDetail({
    id: "c1",
    ports: [{ privatePort: 9000, proto: "tcp" }, { privatePort: 80, proto: "tcp" }],
    networks: ["bridge", "backend", "alpha"].map((network) => ({ network, networkId: "id-" + network, container: "web" })),
  })
}

function ctRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CT_TUNNELS]: { body: { output: { tunnels: ctTunnels() } } },
    [CT_CONNECTORS]: { body: { output: { connectors: [ctConnector("t2", "running"), ctConnector("t3", "missing")] } } },
    [CT_INSPECT]: { body: { output: ctDetail() } },
    [CT_NETWORKS]: {
      body: {
        networks: [
          dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }),
          dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" }),
          dkNetwork({ id: "id-alpha", name: "alpha", driver: "bridge" }),
        ],
      },
    },
    [CT_PLAN]: (call: FetchCall) => ({
      body: {
        planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h",
        diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
      },
    }),
    ...routes,
  })
  const result = renderWithProviders(<ExposeAction container={ctContainer} onDone={() => {}} />)
  return { calls, ...result }
}

function ctProblem(status: number, detail: string, code: string) {
  return { status, body: { type: "about:blank", title: "Problem", status, detail, code } }
}

describe("ExposeAction", () => {
  test("offers only the tunnels whose connector runs on this host and plans the exposure with the chosen one", async () => {
    const { calls } = ctRender()
    const user = userEvent.setup()
    const form = await screen.findByRole("form", { name: "Expose container via tunnel" })
    const tunnels = within(form).getByRole("radiogroup", { name: "Tunnel" })
    expect(within(tunnels).getAllByRole("radio")).toHaveLength(1)
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-b", checked: true })).toBeTruthy()
    expect(within(form).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
    expect((within(form).getByLabelText("Port") as HTMLInputElement).value).toBe("80")
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(calls.filter((c) => c.path.endsWith("/plan")).map((c) => c.body)).toEqual([
      { input: { hostId: "h1", tunnel: "t2", container: "web", port: 80, network: "alpha", hostname: "app.example.com" } },
    ])
    expect(calls.some((c) => c.method === "POST" && c.path === `${CT_HOST}/actions/cloudflare.connector.list/v1/read`)).toBe(true)
  })

  test("says so when no tunnel exists and points to the tunnels page", async () => {
    ctRender({ [CT_TUNNELS]: { body: { output: { tunnels: [] } } } })
    expect((await screen.findByText("No Cloudflare tunnel exists yet. Create a tunnel first.")).textContent).toBe(
      "No Cloudflare tunnel exists yet. Create a tunnel first.",
    )
    expect(screen.getByRole("link", { name: "Open the tunnels page" }).getAttribute("href")).toBe("/cloudflare/tunnels")
    expect(screen.queryByRole("form")).toBeNull()
  })

  test.each([
    ["the host runs no connector", []],
    ["the only connector's container is missing", [ctConnector("t1", "missing")]],
    ["the host runs the connector of a tunnel that is not listed", [ctConnector("t9", "running")]],
  ])("says so when %s and points to the connectors page", async (_name, connectors) => {
    ctRender({ [CT_CONNECTORS]: { body: { output: { connectors } } } })
    expect((await screen.findByText("No Cloudflare connector runs on this host. Deploy one first.")).textContent).toBe(
      "No Cloudflare connector runs on this host. Deploy one first.",
    )
    expect(screen.getByRole("link", { name: "Open the connectors page" }).getAttribute("href")).toBe("/cloudflare/connectors")
    expect(screen.queryByRole("form")).toBeNull()
  })

  test.each([
    ["the tunnels", CT_TUNNELS, "the Cloudflare account is not connected", "cloud.not_connected"],
    ["the connectors", CT_CONNECTORS, "the Cloudflare module is unavailable", "module.unavailable"],
    ["the container", CT_INSPECT, "the Docker daemon is not reachable", "docker.engine_unavailable"],
  ])("shows the reason when %s cannot be read", async (_name, route, detail, code) => {
    ctRender({ [route]: ctProblem(503, detail, code) })
    expect((await screen.findByRole("alert")).textContent).toBe(detail)
    expect(screen.queryByRole("form")).toBeNull()
  })
})
