import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerDetail, EndpointView } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import { npInstance } from "./fixtures"
import type { InstanceStatus } from "./types"

const CA_HOST = "/api/v1/hosts/h1"
const CA_STATUS = `POST ${CA_HOST}/actions/npm.instance.status/v1/read`
const CA_INSPECT = `POST ${CA_HOST}/actions/docker.container.inspect/v1/read`
const CA_NETWORKS = `GET ${CA_HOST}/resources/docker/networks`
const CA_PLAN = "POST /api/v1/actions/npm.expose.container/v1/plan"
const CA_UNSUPPORTED =
  "This container is not on a user-defined bridge network, so Nginx Proxy Manager cannot reach it by name: connect the container to a user-defined network."

const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function caPlan(call: FetchCall) {
  return {
    body: {
      planId: "p1", hostId: null, action: "npm.expose.container", version: 1, inputHash: "h",
      diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null,
    },
  }
}

function caStatus(over: Partial<InstanceStatus> = {}): InstanceStatus {
  return npInstance({ deployed: true, containerId: "c0ffee", running: true, apiReady: true, public: "confirmed", adminEmail: "admin@example.com", ...over })
}

function caEndpoint(network: string): EndpointView {
  return { network, networkId: "id-" + network, container: "web" }
}

function caDetail(networks: string[] = ["bridge", "backend", "alpha", "mesh"]): ContainerDetail {
  return dkDetail({
    id: "c1",
    ports: [{ privatePort: 9000, proto: "tcp" }, { privatePort: 80, proto: "tcp" }],
    networks: networks.map(caEndpoint),
  })
}

function caNetworks() {
  return [
    dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }),
    dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" }),
    dkNetwork({ id: "id-alpha", name: "alpha", driver: "bridge" }),
    dkNetwork({ id: "id-mesh", name: "mesh", driver: "overlay" }),
    dkNetwork({ id: "id-host", name: "host", driver: "host" }),
  ]
}

function caRender(routes: Record<string, StubHandler> = {}, container: ContainerRef = caContainer, onDone: () => void = () => {}) {
  const calls = stubFetch({
    [CA_STATUS]: { body: { output: caStatus() } },
    [CA_INSPECT]: { body: { output: caDetail() } },
    [CA_NETWORKS]: { body: { networks: caNetworks() } },
    [CA_PLAN]: caPlan,
    ...routes,
  })
  const result = renderWithProviders(<ExposeAction container={container} onDone={onDone} />)
  return { calls, ...result }
}

function caPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

async function caForm() {
  return screen.findByRole("form", { name: "Expose container" })
}

test("says so when the host has no deployed Nginx Proxy Manager", async () => {
  caRender({ [CA_STATUS]: { body: { output: caStatus({ deployed: false, containerId: "", running: false, apiReady: false, public: "no", adminEmail: "" }) } } })
  expect((await screen.findByText("Nginx Proxy Manager is not deployed on this host. Deploy it first.")).textContent).toBe(
    "Nginx Proxy Manager is not deployed on this host. Deploy it first.",
  )
  expect(screen.queryByRole("form")).toBeNull()
})

test("shows the reason when the NPM status cannot be read", async () => {
  caRender({ [CA_STATUS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the NPM module is unavailable")
  expect(screen.queryByRole("form")).toBeNull()
})

test("shows the reason when the container cannot be read", async () => {
  caRender({ [CA_INSPECT]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
})

test("offers only the user-defined bridge networks, selects the first in name order and defaults to the lowest port", async () => {
  const { calls } = caRender()
  const user = userEvent.setup()
  const form = await caForm()
  expect(within(form).getAllByRole("radio")).toHaveLength(2)
  expect(within(form).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
  expect(within(form).getByRole("radio", { name: "backend", checked: false })).toBeTruthy()
  for (const hidden of ["bridge", "mesh", "host"]) expect(within(form).queryByRole("radio", { name: hidden })).toBeNull()
  expect((within(form).getByLabelText("Port") as HTMLInputElement).value).toBe("80")
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Domains"), "App.Example.com, www.example.com")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com, www.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/actions/npm.expose.container/v1/plan")
  expect(caPlanBodies(calls)).toEqual([
    { input: { hostId: "h1", container: "web", port: 80, network: "alpha", domains: ["app.example.com", "www.example.com"] } },
  ])
})

test("another network, websockets and an access list reach the input", async () => {
  const { calls } = caRender()
  const user = userEvent.setup()
  const form = await caForm()
  await user.click(within(form).getByRole("radio", { name: "backend" }))
  const port = within(form).getByLabelText("Port")
  await user.clear(port)
  await user.type(port, "9000")
  await user.type(within(form).getByLabelText("Domains"), "api.example.com")
  await user.click(within(form).getByRole("checkbox", { name: "Websockets" }))
  await user.type(within(form).getByLabelText("Access list ID (optional)"), "3")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await screen.findByRole("dialog", { name: "Expose web at api.example.com" })
  await waitFor(() => expect(caPlanBodies(calls)).toHaveLength(1))
  expect(caPlanBodies(calls)).toEqual([
    { input: { hostId: "h1", container: "web", port: 9000, network: "backend", domains: ["api.example.com"], websockets: true, accessListId: 3 } },
  ])
})

test("the container name is sent without Docker's leading slash", async () => {
  const { calls } = caRender({}, { ...caContainer, name: "/web" })
  const user = userEvent.setup()
  const form = await caForm()
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
  expect((caPlanBodies(calls)[0] as { input: { container: string } }).input.container).toBe("web")
})

test.each([
  ["Port", "0"],
  ["Port", "65536"],
  ["Port", ""],
  ["Port", "http"],
  ["Access list ID (optional)", "abc"],
])("%s %j keeps Plan off", async (label, typed) => {
  caRender()
  const user = userEvent.setup()
  const form = await caForm()
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(false)
  const field = within(form).getByLabelText(label)
  await user.clear(field)
  if (typed !== "") await user.type(field, typed)
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("a container with no user-defined network is warned about and its refusal in the preview is shown", async () => {
  const { calls } = caRender({
    [CA_INSPECT]: { body: { output: caDetail(["mesh", "bridge"]) } },
    [CA_PLAN]: {
      status: 409,
      body: {
        type: "about:blank", title: "Unsupported target", status: 409,
        detail: "web is not on a user-defined bridge network: connect the container to a user-defined network", code: "npm.unsupported_target",
      },
    },
  })
  const user = userEvent.setup()
  const form = await caForm()
  expect(within(form).getByText(CA_UNSUPPORTED)).toBeTruthy()
  expect(within(form).queryAllByRole("radio")).toHaveLength(0)
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe(
    "web is not on a user-defined bridge network: connect the container to a user-defined network",
  )
  expect(caPlanBodies(calls)).toEqual([{ input: { hostId: "h1", container: "web", port: 80, network: "bridge", domains: ["app.example.com"] } }])
})

test("closing the change dialog calls onDone", async () => {
  const onDone = vi.fn()
  caRender({}, caContainer, onDone)
  const user = userEvent.setup()
  const form = await caForm()
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(onDone).not.toHaveBeenCalled()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(onDone).toHaveBeenCalledTimes(1)
})

test("the form has no accessibility violations", async () => {
  caRender()
  const form = await caForm()
  expect(await axe(form)).toHaveNoViolations()
})
