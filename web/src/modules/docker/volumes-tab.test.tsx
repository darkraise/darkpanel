import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { dkVolume } from "./fixtures"
import type { DockerVolume } from "./types"
import { VolumesTab } from "./volumes-tab"

const VT_LIST = "GET /api/v1/hosts/h1/resources/docker/volumes"
const VT_CREATED = "2026-10-01T08:30:00Z"

function vtPlan(call: FetchCall) {
  const action = call.path.split("/")[6] ?? ""
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null } }
}

function vtVolumes(): DockerVolume[] {
  return [
    dkVolume({ name: "pgdata", driver: "local", mountpoint: "/var/lib/docker/volumes/pgdata/_data", created: VT_CREATED, containers: ["db"], owner: undefined }),
    dkVolume({ name: "scratch", driver: "local", mountpoint: "/var/lib/docker/volumes/scratch/_data", created: VT_CREATED, containers: [], owner: undefined }),
    dkVolume({ name: "darkpanel-npm-data", driver: "local", mountpoint: "/var/lib/docker/volumes/darkpanel-npm-data/_data", created: VT_CREATED, containers: ["npm"], owner: "npm" }),
  ]
}

function vtRender(volumes: DockerVolume[] = vtVolumes(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [VT_LIST]: { body: { volumes } },
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/docker.volume.create/v1/plan": vtPlan,
    "POST /api/v1/hosts/h1/actions/docker.volume.remove/v1/plan": vtPlan,
    ...routes,
  })
  renderWithProviders(<VolumesTab hostId="h1" />)
  return calls
}

function vtCells(n: number): (string | null)[] {
  const row = within(screen.getByRole("table", { name: "Docker volumes" })).getAllByRole("row")[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return within(row).getAllByRole("cell").map((c) => c.textContent)
}

function vtPlanned(calls: FetchCall[]): { path: string; body: unknown }[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => ({ path: c.path, body: c.body }))
}

test("lists each volume with its mountpoint, creation time, containers and owner", async () => {
  vtRender()
  await screen.findByRole("table", { name: "Docker volumes" })
  expect(vtCells(1)).toEqual(["pgdata", "local", "/var/lib/docker/volumes/pgdata/_data", formatTime(VT_CREATED), "db", "", "Remove"])
  expect(vtCells(2)).toEqual(["scratch", "local", "/var/lib/docker/volumes/scratch/_data", formatTime(VT_CREATED), "", "", "Remove"])
  expect(vtCells(3)).toEqual(["darkpanel-npm-data", "local", "/var/lib/docker/volumes/darkpanel-npm-data/_data", formatTime(VT_CREATED), "npm", "npm", "Remove"])
})

test("says so when the host has no volumes", async () => {
  vtRender([])
  expect((await screen.findByText("No volumes")).textContent).toBe("No volumes")
})

test("shows the reason when the volumes cannot be read", async () => {
  vtRender([], {
    [VT_LIST]: { status: 503, body: { type: "about:blank", title: "Docker unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
})

test("removing a volume plans docker.volume.remove with its name, and an owned volume cannot be removed", async () => {
  const calls = vtRender()
  await screen.findByRole("table", { name: "Docker volumes" })
  expect(screen.getByRole("button", { name: "Remove darkpanel-npm-data" }).hasAttribute("disabled")).toBe(true)
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove scratch" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove volume scratch" })
  await within(dialog).findByLabelText("Planned changes")
  expect(vtPlanned(calls)).toEqual([{ path: "/api/v1/hosts/h1/actions/docker.volume.remove/v1/plan", body: { input: { volume: "scratch" } } }])
})

test("the create form needs a name and plans docker.volume.create with only the fields set", async () => {
  const calls = vtRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Create volume" }))
  const form = await screen.findByRole("form", { name: "Volume" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Name"), " cache ")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Create volume cache" })
  await within(dialog).findByLabelText("Planned changes")
  expect(vtPlanned(calls)).toEqual([{ path: "/api/v1/hosts/h1/actions/docker.volume.create/v1/plan", body: { input: { name: "cache" } } }])
})

test("a driver typed in the create form reaches the input", async () => {
  const calls = vtRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Create volume" }))
  const form = await screen.findByRole("form", { name: "Volume" })
  await user.type(within(form).getByLabelText("Name"), "nfs-share")
  await user.type(within(form).getByLabelText("Driver"), "local-persist")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Create volume nfs-share" })).findByLabelText("Planned changes")
  expect(vtPlanned(calls).map((c) => c.body)).toEqual([{ input: { name: "nfs-share", driver: "local-persist" } }])
})

test("the table has no accessibility violations", async () => {
  vtRender()
  expect(await axe(await screen.findByRole("table", { name: "Docker volumes" }))).toHaveNoViolations()
})
