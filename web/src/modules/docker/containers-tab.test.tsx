import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler, type StubResponse } from "@/test/utils"
import { ContainersTab } from "./containers-tab"
import { dkContainer } from "./fixtures"
import type { DockerContainer } from "./types"

const CT_LIST = "GET /api/v1/hosts/h1/resources/docker/containers"
const CT_ACTIONS = ["docker.container.start", "docker.container.stop", "docker.container.restart", "docker.container.remove"]

function ctPlan(call: FetchCall): StubResponse {
  const action = call.path.split("/")[6] ?? ""
  const confirmation = action === "docker.container.remove" ? "web" : null
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

function ctRender(containers: DockerContainer[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CT_LIST]: { body: { containers } },
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    ...Object.fromEntries(CT_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/plan`, ctPlan])),
    ...routes,
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<ContainersTab hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker" },
  )
  return { calls, ...result }
}

function ctPlanCall(calls: FetchCall[]): { path: string; body: unknown } {
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

const ctWeb = dkContainer({
  id: "c1",
  name: "web",
  image: "nginx:1.27",
  state: "running",
  status: "Up 2 hours",
  ports: [
    { ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" },
    { ip: "::", privatePort: 80, publicPort: 8080, proto: "tcp" },
    { privatePort: 443, proto: "tcp" },
  ],
  project: "shop",
  service: "web",
  owner: undefined,
  usage: { cpuPercent: 12.34, memUsage: 47185920, memLimit: 1073741824 },
})
const ctDb = dkContainer({ id: "c2", name: "db", image: "postgres:17", state: "exited", status: "Exited (0) 3 days ago", ports: [], project: undefined, service: undefined, owner: undefined, usage: undefined })

test("the table shows each container's image, state, ports, project and usage and links to its page", async () => {
  ctRender([ctWeb, ctDb])
  const rows = within(await screen.findByRole("table", { name: "Docker containers" })).getAllByRole("row")
  expect(rows).toHaveLength(3)
  const cells = (row: HTMLElement | undefined) => within(row as HTMLElement).getAllByRole("cell").slice(0, 6).map((c) => c.textContent)
  expect(cells(rows[1])).toEqual(["web", "nginx:1.27", "running", "8080→80/tcp, 443/tcp", "shop / web", "12.3% · 45.0 MiB / 1.0 GiB"])
  expect(cells(rows[2])).toEqual(["db", "postgres:17", "exited", "—", "—", "—"])
  expect(within(rows[1] as HTMLElement).getByRole("link", { name: "web" }).getAttribute("href")).toBe("/hosts/h1/docker/containers/c1")
})

test("a running container offers Stop and Restart and a stopped one offers Start, and only a stopped one can be removed", async () => {
  ctRender([ctWeb, ctDb])
  await screen.findByRole("table", { name: "Docker containers" })
  const names = screen.getAllByRole("button").map((b) => b.getAttribute("aria-label"))
  expect(names).toEqual(["Stop web", "Restart web", "Remove web", "Start db", "Remove db"])
  expect(screen.getByRole("button", { name: "Remove web" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Remove db" }).hasAttribute("disabled")).toBe(false)
})

test.each([
  { label: "Stop web", action: "docker.container.stop", container: ctWeb },
  { label: "Restart web", action: "docker.container.restart", container: ctWeb },
  { label: "Start db", action: "docker.container.start", container: ctDb },
  { label: "Remove db", action: "docker.container.remove", container: ctDb },
])("$label plans $action for the container's id", async ({ label, action, container }) => {
  const { calls } = ctRender([ctWeb, ctDb])
  await userEvent.setup().click(await screen.findByRole("button", { name: label }))
  await within(await screen.findByRole("dialog", { name: label })).findByLabelText("Planned changes")
  expect(ctPlanCall(calls)).toEqual({ path: `/api/v1/hosts/h1/actions/${action}/v1/plan`, body: { input: { container: container.id } } })
})

test("a container a module owns is marked and cannot be changed", async () => {
  const { calls } = ctRender([dkContainer({ id: "c3", name: "cloudflared", state: "exited", owner: "cloudflare", ports: [], usage: undefined })])
  const row = within((await screen.findByRole("table", { name: "Docker containers" })).querySelectorAll("tr")[1] as HTMLElement)
  expect(row.getByText("owned by cloudflare").textContent).toBe("owned by cloudflare")
  for (const name of ["Start cloudflared", "Remove cloudflared"]) {
    expect(row.getByRole("button", { name }).hasAttribute("disabled")).toBe(true)
  }
  await userEvent.setup().click(row.getByRole("button", { name: "Start cloudflared" }))
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
})

test("a host without containers says so", async () => {
  ctRender([])
  expect((await screen.findByText("No containers")).textContent).toBe("No containers")
})

test("a failed read shows the server's reason", async () => {
  ctRender([], {
    [CT_LIST]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the docker module is unavailable on this host", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the docker module is unavailable on this host")
})

test("the containers table has no accessibility violations", async () => {
  const { container } = ctRender([ctWeb, ctDb])
  await screen.findByRole("table", { name: "Docker containers" })
  expect(await axe(container)).toHaveNoViolations()
})
