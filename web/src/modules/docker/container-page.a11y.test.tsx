import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import type { Host, ModuleState } from "@/api/types"
import type { ExposureView } from "@/modules/firewall/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { dkDetail } from "./fixtures"
import DockerRoutes from "./routes"

// The registry is replaced by one entry with one action, so this file tests the page's own markup and the
// slot's place in the keyboard order, not the real modules' forms (each has its own file).
vi.mock("@/modules/registry", async () => {
  const { lazy } = await import("react")
  const { Server } = await import("lucide-react")
  return {
    moduleRegistry: [
      {
        id: "alpha",
        label: "Alpha",
        icon: Server,
        containerActions: [{ id: "alpha.probe", label: "Alpha probe", render: lazy(async () => ({ default: () => <button type="button">Alpha action</button> })) }],
      },
    ],
  }
})
vi.mock("./logs-panel", () => ({ LogsPanel: () => <p>logs</p> }))
vi.mock("./stats-panel", () => ({ StatsPanel: () => <p>stats</p> }))

const CP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const CP_INSPECT = "POST /api/v1/hosts/h1/actions/docker.container.inspect/v1/read"
const CP_EXPOSURE = "GET /api/v1/hosts/h1/resources/firewall/exposure"
const CP_DOWN = { status: 500, body: { type: "about:blank", title: "Error", status: 500, detail: "the controller failed", code: "internal" } }

function cpState(id: string, enabled: boolean, available = true, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

function cpHost(modules: ModuleState[]): Host {
  return { id: "h1", name: "web-1", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules, ingestion: null }
}

const cpPorts = [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" as const }]
const cpOn = [cpState("docker", true), cpState("alpha", true), cpState("firewall", true)]
const cpExposure: ExposureView = {
  rows: [{ proto: "tcp", addr: "0.0.0.0", port: 8080, family: "v4", container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false }, classes: [{ class: "internet", verdict: "open" }] }],
}

function cpRender(routes: Record<string, StubHandler> = {}, modules: ModuleState[] = cpOn, ports: typeof cpPorts = cpPorts) {
  stubFetch({
    [CP_HOSTS]: { body: { output: { hosts: [cpHost(modules)] } } },
    [CP_INSPECT]: { body: { output: dkDetail({ id: "c1", name: "/web", running: true, ports }) } },
    [CP_EXPOSURE]: { body: cpExposure },
    ...routes,
  })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<DockerRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker/containers/web" },
  )
}

const cpViews: { state: string; routes: Record<string, StubHandler>; modules: ModuleState[]; ports: typeof cpPorts; ready: () => Promise<unknown> }[] = [
  { state: "the page with its exposure and its action", routes: {}, modules: cpOn, ports: cpPorts, ready: () => screen.findByRole("list", { name: "Published ports" }) },
  { state: "a container that publishes no ports", routes: {}, modules: cpOn, ports: [], ready: () => screen.findByText("This container publishes no ports.") },
  { state: "a host without the firewall module", routes: {}, modules: [cpState("docker", true), cpState("alpha", true)], ports: cpPorts, ready: () => screen.findByText("The firewall module is not enabled on this host.") },
  {
    state: "an unavailable firewall module",
    routes: {},
    modules: [cpState("docker", true), cpState("alpha", true), cpState("firewall", true, false, "ufw is not installed")],
    ports: cpPorts,
    ready: () => screen.findByText("The firewall module is unavailable: ufw is not installed."),
  },
  { state: "a failed exposure read", routes: { [CP_EXPOSURE]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findByText("the controller failed") },
  { state: "a failed container read", routes: { [CP_INSPECT]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findByRole("alert") },
  { state: "a failed host list", routes: { [CP_HOSTS]: CP_DOWN }, modules: cpOn, ports: cpPorts, ready: () => screen.findAllByRole("alert") },
]

test.each(cpViews)("$state has no axe violations", async ({ routes, modules, ports, ready }) => {
  const { container } = cpRender(routes, modules, ports)
  await ready()
  await expectNoAxeViolations(container)
})

test("the loading state has no axe violations", async () => {
  const { container } = cpRender({ [CP_INSPECT]: () => new Promise(() => {}) })
  await screen.findByLabelText("Loading the container")
  await expectNoAxeViolations(container)
})

test("Tab reaches the way back first and the module's action next, each by its name", async () => {
  cpRender()
  await screen.findByRole("button", { name: "Alpha action" })
  expect(await tabSequence(userEvent.setup(), 2)).toEqual(["Back to Docker", "Alpha action"])
})
