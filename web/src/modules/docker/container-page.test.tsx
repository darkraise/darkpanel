import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Host, ModuleState } from "@/api/types"
import type { ExposureRow, ExposureView } from "@/modules/firewall/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { dkContainer, dkDetail } from "./fixtures"
import DockerRoutes from "./routes"
import type { ContainerDetail } from "./types"

// The registry is replaced by three entries so the page's slot logic is
// tested without the real modules: alpha is enabled on the host, beta is
// listed but disabled, gamma is not listed at all.
vi.mock("@/modules/registry", async () => {
  const { lazy } = await import("react")
  const { Server } = await import("lucide-react")
  const entry = (id: string) => ({
    id,
    label: id.toUpperCase(),
    icon: Server,
    containerActions: [
      {
        id: `${id}.probe`,
        label: `${id} probe`,
        render: lazy(async () => ({
          default: ({ container, onDone }: { container: unknown; onDone: () => void }) => (
            <div>
              <output aria-label={`${id} container`}>{JSON.stringify(container)}</output>
              <button type="button" onClick={onDone}>{`${id} done`}</button>
            </div>
          ),
        })),
      },
    ],
  })
  return { moduleRegistry: [entry("alpha"), entry("beta"), entry("gamma")] }
})
vi.mock("./logs-panel", () => ({
  LogsPanel: ({ hostId, containerId }: { hostId: string; containerId: string }) => <p>{`logs of ${containerId} on ${hostId}`}</p>,
}))
vi.mock("./stats-panel", () => ({
  StatsPanel: ({ hostId, containerId }: { hostId: string; containerId: string }) => <p>{`stats of ${containerId} on ${hostId}`}</p>,
}))

const CP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const CP_INSPECT = "POST /api/v1/hosts/h1/actions/docker.container.inspect/v1/read"
const CP_EXPOSURE = "GET /api/v1/hosts/h1/resources/firewall/exposure"

function cpState(id: string, enabled: boolean, available = true, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

const cpFirewallOk = cpState("firewall", true)

function cpHost(firewall: ModuleState | null = cpFirewallOk): Host {
  const modules = [cpState("docker", true), cpState("alpha", true), cpState("beta", false)]
  if (firewall !== null) modules.push(firewall)
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules, ingestion: null }
}

function cpRow(over: Partial<ExposureRow> & { name: string; port: number }): ExposureRow {
  const { name, port, ...rest } = over
  return {
    proto: "tcp",
    addr: "0.0.0.0",
    port,
    family: "v4",
    container: { name, binding: { selector: name, hostPort: port, proto: "tcp" }, containerPort: 80, proxied: false },
    classes: [{ class: "internet", verdict: "open" }],
    ...rest,
  }
}

const cpDetail = (over: Partial<ContainerDetail> = {}) =>
  dkDetail({
    id: "c1",
    name: "/web",
    image: "nginx:1.27",
    state: "running",
    running: true,
    exitCode: 0,
    env: [{ name: "API_KEY", value: "[redacted]" }],
    mounts: [{ type: "bind", source: "/srv/web", destination: "/usr/share/nginx/html", rw: false }],
    networks: [{ network: "bridge", networkId: "n1", container: "web", ipv4: "172.17.0.2" }],
    ports: [
      { ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" },
      { ip: "::", privatePort: 80, publicPort: 8080, proto: "tcp" },
    ],
    ...over,
  })

function cpRender(opts: { detail?: StubHandler; host?: Host; exposure?: StubHandler; id?: string } = {}) {
  const calls = stubFetch({
    [CP_HOSTS]: { body: { output: { hosts: [opts.host ?? cpHost()] } } },
    [CP_INSPECT]: opts.detail ?? { body: { output: cpDetail() } },
    [CP_EXPOSURE]: opts.exposure ?? { body: { rows: [] } satisfies ExposureView },
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<DockerRoutes hostId="h1" />} />
    </Routes>,
    { path: `/hosts/h1/docker/containers/${opts.id ?? "web"}` },
  )
  return { calls, ...result }
}

const cpCalls = (calls: FetchCall[], key: string) => calls.filter((c) => `${c.method} ${c.path.split("?")[0]}` === key)

test("the page inspects the container named in the address and shows its definition", async () => {
  const { calls } = cpRender()
  expect(await screen.findByRole("heading", { name: "web" })).toBeTruthy()
  expect(cpCalls(calls, CP_INSPECT)[0]?.body).toEqual({ input: { container: "web" } })
  const inspect = screen.getByRole("region", { name: "Inspect" })
  expect(within(inspect).getByText("nginx:1.27")).toBeTruthy()
  expect(within(inspect).getByText("API_KEY=[redacted]")).toBeTruthy()
  expect(within(inspect).getByText("/srv/web → /usr/share/nginx/html (read-only)")).toBeTruthy()
  expect(within(inspect).getByText("bridge 172.17.0.2")).toBeTruthy()
})

test("logs and statistics are given the host and the container's id, not the address segment", async () => {
  cpRender({ id: "web" })
  expect(await screen.findByText("logs of c1 on h1")).toBeTruthy()
  expect(screen.getByText("stats of c1 on h1")).toBeTruthy()
})

test("the back link leads to the Docker page of the host", async () => {
  cpRender()
  const link = await screen.findByRole("link", { name: "Back to Docker" })
  expect(link.getAttribute("href")).toBe("/hosts/h1/docker")
})

test("a container the daemon does not know is an error with the way back", async () => {
  cpRender({ detail: { status: 404, body: { type: "about:blank", title: "Not found", status: 404, detail: "no container named web", code: "not_found" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("no container named web")
  expect(screen.getByRole("link", { name: "Back to Docker" })).toBeTruthy()
})

test("the slot renders the actions of enabled modules only, each with a ContainerRef built from the container", async () => {
  cpRender({
    detail: {
      body: {
        output: cpDetail({
          service: "api",
          ports: [
            { ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" },
            { ip: "::", privatePort: 80, publicPort: 8080, proto: "tcp" },
            { ip: "127.0.0.1", privatePort: 53, publicPort: 5353, proto: "udp" },
            { privatePort: 9000, proto: "tcp" },
            { privatePort: 25, publicPort: 2525, proto: "tcp" },
          ],
        }),
      },
    },
  })
  const alpha = await screen.findByRole("region", { name: "alpha probe" })
  expect(JSON.parse(within(alpha).getByLabelText("alpha container").textContent ?? "")).toEqual({
    hostId: "h1",
    containerId: "c1",
    name: "web",
    composeService: "api",
    ports: [
      { privatePort: 80, protocol: "tcp", bindings: [{ hostIp: "0.0.0.0", hostPort: 8080 }, { hostIp: "::", hostPort: 8080 }] },
      { privatePort: 53, protocol: "udp", bindings: [{ hostIp: "127.0.0.1", hostPort: 5353 }] },
      { privatePort: 25, protocol: "tcp", bindings: [{ hostIp: "0.0.0.0", hostPort: 2525 }] },
    ],
  })
  expect(screen.queryByRole("region", { name: "beta probe" })).toBeNull()
  expect(screen.queryByRole("region", { name: "gamma probe" })).toBeNull()
})

test("a container without a Compose service has no composeService in its ref", async () => {
  cpRender()
  const ref = JSON.parse((await screen.findByLabelText("alpha container")).textContent ?? "") as Record<string, unknown>
  expect("composeService" in ref).toBe(false)
})

test("an action's onDone refetches the container", async () => {
  const { calls } = cpRender()
  await userEvent.setup().click(await screen.findByRole("button", { name: "alpha done" }))
  await waitFor(() => expect(cpCalls(calls, CP_INSPECT)).toHaveLength(2))
})

test("exposure lists the firewall rows of this container's published ports only", async () => {
  const view: ExposureView = {
    rows: [
      cpRow({ name: "web", port: 8080 }),
      cpRow({ name: "web", port: 8080, addr: "::", family: "v6", classes: [{ class: "internet", verdict: "closed" }] }),
      cpRow({ name: "db", port: 8080 }),
      cpRow({ name: "web", port: 9999 }),
    ],
  }
  const { calls } = cpRender({ exposure: { body: view } })
  const list = await screen.findByRole("list", { name: "Published ports" })
  expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "tcp 0.0.0.0:8080 internet: open",
    "tcp :::8080 internet: closed",
  ])
  expect(cpCalls(calls, CP_EXPOSURE)).toHaveLength(1)
})

test.each([
  { name: "not listed", firewall: null, text: "The firewall module is not enabled on this host." },
  { name: "disabled", firewall: cpState("firewall", false), text: "The firewall module is not enabled on this host." },
  { name: "unavailable", firewall: cpState("firewall", true, false, "ufw is not installed"), text: "The firewall module is unavailable: ufw is not installed." },
])("without a usable firewall ($name) exposure says why and asks the firewall nothing", async ({ firewall, text }) => {
  const { calls } = cpRender({ host: cpHost(firewall) })
  expect(await screen.findByText(text)).toBeTruthy()
  expect(cpCalls(calls, CP_EXPOSURE)).toHaveLength(0)
})

test("a container that publishes nothing has no exposure to ask for", async () => {
  const { calls } = cpRender({ detail: { body: { output: cpDetail({ ports: [] }) } } })
  expect(await screen.findByText("This container publishes no ports.")).toBeTruthy()
  expect(cpCalls(calls, CP_EXPOSURE)).toHaveLength(0)
})

test("a container's name on the Docker page opens its page", async () => {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/resources/docker/containers": { body: { containers: [dkContainer({ id: "c1", name: "web", owner: undefined })] } },
    [CP_HOSTS]: { body: { output: { hosts: [cpHost()] } } },
    [CP_INSPECT]: { body: { output: cpDetail() } },
    [CP_EXPOSURE]: { body: { rows: [] } satisfies ExposureView },
  })
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<DockerRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker" },
  )
  const table = await screen.findByRole("table", { name: "Docker containers" })
  await userEvent.setup().click(within(table).getByRole("link", { name: "web" }))
  expect(await screen.findByRole("heading", { name: "web" })).toBeTruthy()
  expect(cpCalls(calls, CP_INSPECT)[0]?.body).toEqual({ input: { container: "c1" } })
})

test("the page has no accessibility violations", async () => {
  const { container } = cpRender({ exposure: { body: { rows: [cpRow({ name: "web", port: 8080 })] } } })
  await screen.findByRole("list", { name: "Published ports" })
  expect(await axe(container)).toHaveNoViolations()
})
