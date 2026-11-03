import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { dkNetwork } from "@/modules/docker/fixtures"
import type { DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeTunnelForm } from "./expose-dialog"

const ED_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"
const ED_UNSUPPORTED =
  "This container is not on a user-defined bridge network, so the Cloudflare connector cannot reach it by name: connect the container to a user-defined network."

const edContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }
const edTunnels = [
  { id: "t1", name: "darkpanel-a" },
  { id: "t2", name: "darkpanel-b" },
]

function edPlan(call: FetchCall) {
  return {
    body: {
      planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h",
      diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
    },
  }
}

function edNetworks(): DockerNetwork[] {
  return [
    dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }),
    dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" }),
    dkNetwork({ id: "id-alpha", name: "alpha", driver: "bridge" }),
    dkNetwork({ id: "id-mesh", name: "mesh", driver: "overlay" }),
    dkNetwork({ id: "id-host", name: "host", driver: "host" }),
  ]
}

interface EdOptions {
  container?: ContainerRef
  endpoints?: string[]
  routes?: Record<string, StubHandler>
  onDone?: () => void
}

function edRender(options: EdOptions = {}) {
  const calls = stubFetch({ [ED_PLAN]: edPlan, ...options.routes })
  renderWithProviders(
    <ExposeTunnelForm
      container={options.container ?? edContainer}
      tunnels={edTunnels}
      endpoints={options.endpoints ?? ["bridge", "backend", "alpha", "mesh"]}
      networks={edNetworks()}
      ports={[9000, 80]}
      onDone={options.onDone ?? (() => {})}
    />,
  )
  return calls
}

function edForm(): HTMLElement {
  return screen.getByRole("form", { name: "Expose container via tunnel" })
}

function edPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

describe("ExposeTunnelForm", () => {
  test("offers the tunnels and only the user-defined bridge networks, selects the first of each and defaults to the lowest port", async () => {
    const user = userEvent.setup()
    const calls = edRender()
    const form = edForm()
    const tunnels = within(form).getByRole("radiogroup", { name: "Tunnel" })
    expect(within(tunnels).getAllByRole("radio")).toHaveLength(2)
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-a", checked: true })).toBeTruthy()
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-b", checked: false })).toBeTruthy()
    const networks = within(form).getByRole("radiogroup", { name: "Network" })
    expect(within(networks).getAllByRole("radio")).toHaveLength(2)
    expect(within(networks).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
    expect(within(networks).getByRole("radio", { name: "backend", checked: false })).toBeTruthy()
    for (const hidden of ["bridge", "mesh", "host"]) expect(within(networks).queryByRole("radio", { name: hidden })).toBeNull()
    expect((within(form).getByLabelText("Port") as HTMLInputElement).value).toBe("80")
    const plan = within(form).getByRole("button", { name: "Plan" })
    expect(plan.hasAttribute("disabled")).toBe(true)
    await user.type(within(form).getByLabelText("Hostname"), " App.Example.com ")
    expect(plan.hasAttribute("disabled")).toBe(false)
    await user.click(plan)
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/actions/cloudflare.expose.container/v1/plan")
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "alpha", hostname: "app.example.com" } },
    ])
  })

  test("another tunnel, network and port reach the input", async () => {
    const user = userEvent.setup()
    const calls = edRender()
    const form = edForm()
    await user.click(within(form).getByRole("radio", { name: "darkpanel-b" }))
    await user.click(within(form).getByRole("radio", { name: "backend" }))
    const port = within(form).getByLabelText("Port")
    await user.clear(port)
    await user.type(port, "9000")
    await user.type(within(form).getByLabelText("Hostname"), "api.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    await screen.findByRole("dialog", { name: "Expose web at api.example.com" })
    await waitFor(() => expect(edPlanBodies(calls)).toHaveLength(1))
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t2", container: "web", port: 9000, network: "backend", hostname: "api.example.com" } },
    ])
  })

  test("the container name is sent without Docker's leading slash", async () => {
    const user = userEvent.setup()
    const calls = edRender({ container: { ...edContainer, name: "/web" } })
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((edPlanBodies(calls)[0] as { input: { container: string } }).input.container).toBe("web")
  })

  test.each([
    ["Port", "0"],
    ["Port", "65536"],
    ["Port", ""],
    ["Port", "http"],
    ["Hostname", ""],
  ])("%s %j keeps Plan off", async (label, typed) => {
    edRender()
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(false)
    const field = within(form).getByLabelText(label)
    await user.clear(field)
    if (typed !== "") await user.type(field, typed)
    expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
  })

  test("a container with no user-defined network is warned about and the controller's refusal in the preview is shown", async () => {
    const calls = edRender({
      endpoints: ["mesh", "bridge"],
      routes: {
        [ED_PLAN]: {
          status: 422,
          body: {
            type: "about:blank", title: "Unsupported tunnel target", status: 422,
            detail: "web is not on a user-defined bridge network: connect the container to a user-defined network", code: "cloudflare.unsupported_target",
          },
        },
      },
    })
    const user = userEvent.setup()
    const form = edForm()
    expect(within(form).getByText(ED_UNSUPPORTED)).toBeTruthy()
    expect(within(form).queryByRole("radiogroup", { name: "Network" })).toBeNull()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((await within(dialog).findByRole("alert")).textContent).toBe(
      "web is not on a user-defined bridge network: connect the container to a user-defined network",
    )
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "bridge", hostname: "app.example.com" } },
    ])
  })

  test("the controller's refusal of a tunnel without a connector on the host is shown", async () => {
    edRender({
      routes: {
        [ED_PLAN]: {
          status: 409,
          body: {
            type: "about:blank", title: "No connector for the tunnel on this host", status: 409,
            detail: "tunnel t1 has no connector on this host", code: "cloudflare.connector_missing",
          },
        },
      },
    })
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((await within(dialog).findByRole("alert")).textContent).toBe("tunnel t1 has no connector on this host")
  })

  test("closing the change dialog calls onDone", async () => {
    const onDone = vi.fn()
    edRender({ onDone })
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(onDone).not.toHaveBeenCalled()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(onDone).toHaveBeenCalledTimes(1)
  })

  test("the form has no accessibility violations", async () => {
    edRender()
    expect(await axe(edForm())).toHaveNoViolations()
  })
})
