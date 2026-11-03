import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Host, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ConnectorsPage } from "./connectors-page"
import { cfConnector, cfTunnel } from "./fixtures"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const LIST_SUFFIX = "cloudflare.connector.list/v1/read"
const cnList = (host: string) => `POST /api/v1/hosts/${host}/actions/${LIST_SUFFIX}`
const cnAvailability = (host: string) => `GET /api/v1/hosts/${host}/modules/cloudflare/availability`

function cnHost(id: string, name: string, enabled = true): Host {
  return {
    id,
    name,
    isLocal: false,
    online: true,
    modules: [{ id: "cloudflare", version: 1, enabled, available: true, reason: null, checkedAt: null }],
    ingestion: null,
  }
}

function cnTunnels() {
  return [cfTunnel({ id: "t1", name: "darkpanel-c1" }), cfTunnel({ id: "t2", name: "darkpanel-c2", status: "inactive", connections: [] })]
}

function cnPlan(action: string, hostId: string | null): Plan {
  return {
    planId: "p1", hostId, action, version: 1, inputHash: "h", diff: [{ op: "~", text: "connector" }], guards: [],
    expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
  }
}

const cnChange: Change = {
  id: "c9", hostId: "h1", action: "cloudflare.connector.stop", version: 1, class: "compensable", state: "completed", actor: "user:u1",
  createdAt: "2026-11-03T12:00:00Z", updatedAt: "2026-11-03T12:00:00Z", revertDeadline: null, output: { tunnel: "t1", removed: true }, error: null,
  compensatedBy: "cloudflare.connector.run", parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function cnRender(hosts: Host[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts } } },
    [TUNNELS]: { body: { output: { tunnels: cnTunnels() } } },
    [cnList("h1")]: { body: { output: { connectors: [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })] } } },
    [cnAvailability("h1")]: { body: { module: "cloudflare", available: true, reason: null, actions: [] } },
    ...routes,
  })
  const result = renderWithProviders(<ConnectorsPage />)
  return { calls, ...result }
}

function cnListCalls(calls: FetchCall[]): FetchCall[] {
  return calls.filter((c) => c.path.endsWith(LIST_SUFFIX))
}

function cnPlanCall(calls: FetchCall[], path: string): FetchCall | undefined {
  return calls.find((c) => c.method === "POST" && c.path === path)
}

async function cnTable() {
  return screen.findByRole("table", { name: "Connectors" })
}

describe("ConnectorsPage", () => {
  test("lists the connectors of the only host with the tunnel's name, the container and the state", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: {
        body: {
          output: {
            connectors: [
              cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1", state: "running" }),
              cfConnector({ tunnel: "t-gone", container: "darkpanel-cloudflared-t-gone", state: "missing" }),
            ],
          },
        },
      },
    })
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 3).map((c) => c.textContent))).toEqual([
      ["darkpanel-c1", "darkpanel-cloudflared-t1", "running"],
      ["t-gone", "darkpanel-cloudflared-t-gone", "missing"],
    ])
    expect(screen.getByText("Connectors on web-1")).toBeTruthy()
    expect(screen.queryByRole("radiogroup", { name: "Host" })).toBeNull()
    const read = cnListCalls(calls)
    expect(read.map((c) => [c.path, c.body])).toEqual([["/api/v1/hosts/h1/actions/cloudflare.connector.list/v1/read", { input: {} }]])
  })

  test("offers only the hosts that run the module and reads none until one is chosen", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1"), cnHost("h2", "web-2"), cnHost("h3", "db-1", false)], {
      [cnList("h2")]: { body: { output: { connectors: [cfConnector({ tunnel: "t2", container: "darkpanel-cloudflared-t2" })] } } },
    })
    const group = await screen.findByRole("radiogroup", { name: "Host" })
    expect(within(group).getAllByRole("radio")).toHaveLength(2)
    expect(within(group).getByRole("radio", { name: "web-1" })).toBeTruthy()
    expect(within(group).getByRole("radio", { name: "web-2" })).toBeTruthy()
    expect(within(group).queryByRole("radio", { name: "db-1" })).toBeNull()
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
    expect(cnListCalls(calls)).toHaveLength(0)
    await userEvent.setup().click(within(group).getByRole("radio", { name: "web-2" }))
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["darkpanel-c2"])
    expect(cnListCalls(calls).map((c) => c.path)).toEqual(["/api/v1/hosts/h2/actions/cloudflare.connector.list/v1/read"])
  })

  test("without a host that runs the module the page says so", async () => {
    cnRender([cnHost("h3", "db-1", false)])
    expect((await screen.findByText("No host runs the Cloudflare module")).textContent).toBe("No host runs the Cloudflare module")
    expect(screen.queryByRole("button", { name: "Deploy connector" })).toBeNull()
  })

  test("a host without connectors says so and still offers to deploy one", async () => {
    cnRender([cnHost("h1", "web-1")], { [cnList("h1")]: { body: { output: { connectors: [] } } } })
    expect((await screen.findByText("No connectors on this host")).textContent).toBe("No connectors on this host")
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
    expect(screen.getByRole("button", { name: "Deploy connector" })).toBeTruthy()
  })

  test("a failed read of the connectors shows the reason", async () => {
    cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: {
        status: 503,
        body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker Engine is not reachable", code: "docker.engine_unavailable" },
      },
    })
    expect((await screen.findByRole("alert")).textContent).toBe("the Docker Engine is not reachable")
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
  })

  test("stopping a connector plans the stop on its host by tunnel id and refreshes the list when the dialog closes", async () => {
    let connectors = [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })]
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: () => ({ body: { output: { connectors } } }),
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/plan": { body: cnPlan("cloudflare.connector.stop", "h1") },
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/apply": () => {
        connectors = []
        return { body: cnChange }
      },
    })
    const user = userEvent.setup()
    await cnTable()
    await user.click(screen.getByRole("button", { name: "Stop the connector of darkpanel-c1" }))
    const dialog = await screen.findByRole("dialog", { name: "Stop the connector of darkpanel-c1" })
    expect(within(dialog).getByText("cloudflare.connector.stop/v1 on host h1")).toBeTruthy()
    await within(dialog).findByLabelText("Planned changes")
    expect(cnPlanCall(calls, "/api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/plan")?.body).toEqual({ input: { tunnel: "t1" } })
    expect(calls.some((c) => c.path === "/api/v1/hosts/h1/modules/cloudflare/availability")).toBe(true)
    const apply = within(dialog).getByRole("button", { name: "Apply" })
    await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
    await user.click(apply)
    await within(dialog).findByText("completed")
    // The dialog's corner button has the same name; the footer's is the last.
    await user.click(within(dialog).getAllByRole("button", { name: "Close" }).slice(-1)[0] as HTMLElement)
    expect((await screen.findByText("No connectors on this host")).textContent).toBe("No connectors on this host")
    expect(cnListCalls(calls)).toHaveLength(2)
  })

  test("deploying plans the composite on the controller with the host alone, which has it create a tunnel", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      "POST /api/v1/actions/cloudflare.connector.deploy/v1/plan": { body: cnPlan("cloudflare.connector.deploy", null) },
    })
    const user = userEvent.setup()
    await cnTable()
    expect(screen.getByRole("radio", { name: "Create a new tunnel" }).getAttribute("aria-checked")).toBe("true")
    await user.click(screen.getByRole("button", { name: "Deploy connector" }))
    const dialog = await screen.findByRole("dialog", { name: "Deploy a connector on web-1" })
    await within(dialog).findByLabelText("Planned changes")
    expect(within(dialog).getByText("cloudflare.connector.deploy/v1 on the controller")).toBeTruthy()
    expect(cnPlanCall(calls, "/api/v1/actions/cloudflare.connector.deploy/v1/plan")?.body).toEqual({ input: { hostId: "h1" } })
  })

  test("deploying for an existing tunnel names it, and a tunnel that already has a connector on the host is not offered", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      "POST /api/v1/actions/cloudflare.connector.deploy/v1/plan": { body: cnPlan("cloudflare.connector.deploy", null) },
    })
    const user = userEvent.setup()
    await cnTable()
    const form = screen.getByRole("form", { name: "Deploy a connector" })
    expect(within(form).getAllByRole("radio")).toHaveLength(2)
    expect(within(form).queryByRole("radio", { name: "darkpanel-c1" })).toBeNull()
    await user.click(within(form).getByRole("radio", { name: "darkpanel-c2" }))
    await user.click(within(form).getByRole("button", { name: "Deploy connector" }))
    const dialog = await screen.findByRole("dialog", { name: "Deploy a connector on web-1" })
    await within(dialog).findByLabelText("Planned changes")
    expect(cnPlanCall(calls, "/api/v1/actions/cloudflare.connector.deploy/v1/plan")?.body).toEqual({ input: { hostId: "h1", tunnel: "t2" } })
  })

  test("a tunnel list that cannot be read shows the reason, still lists the connectors by tunnel id and offers a new tunnel only", async () => {
    cnRender([cnHost("h1", "web-1")], {
      [TUNNELS]: {
        status: 409,
        body: { type: "about:blank", title: "Not connected", status: 409, detail: "no Cloudflare account is connected", code: "cloud.not_connected" },
      },
    })
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["t1"])
    expect((await screen.findByRole("alert")).textContent).toBe("no Cloudflare account is connected")
    const form = screen.getByRole("form", { name: "Deploy a connector" })
    expect(within(form).getAllByRole("radio")).toHaveLength(1)
    expect(within(form).getByRole("radio", { name: "Create a new tunnel" })).toBeTruthy()
  })

  test("a tunnel list that is still pending does not hold back the connectors", async () => {
    cnRender([cnHost("h1", "web-1")], { [TUNNELS]: () => new Promise(() => {}) })
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["t1"])
    expect(screen.getByLabelText("Loading the tunnels")).toBeTruthy()
  })

  test("has no accessibility violations", async () => {
    const { container } = cnRender([cnHost("h1", "web-1"), cnHost("h2", "web-2")])
    await userEvent.setup().click(await screen.findByRole("radio", { name: "web-1" }))
    await cnTable()
    expect(await axe(container)).toHaveNoViolations()
  })
})
