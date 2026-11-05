import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { dkNetwork } from "@/modules/docker/fixtures"
import type { DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { ExposeTunnelForm } from "./expose-dialog"

const ED_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"
const ED_FORM = "Expose container via tunnel"

const edContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }
const edTunnels = [
  { id: "t1", name: "darkpanel-a" },
  { id: "t2", name: "darkpanel-b" },
]

function edNetworks(): DockerNetwork[] {
  return ["bridge", "backend", "alpha"].map((name) => dkNetwork({ id: "id-" + name, name, driver: "bridge" }))
}

function edRender(endpoints: string[] = ["bridge", "backend", "alpha"], onDone: () => void = () => {}) {
  stubFetch({
    [ED_PLAN]: (call: FetchCall) => ({
      body: { planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h", diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null },
    }),
    "GET /api/v1/changes/ch1": { body: { id: "ch1", state: "completed", class: "composite", error: null, output: null, revertDeadline: null } },
  })
  return renderWithProviders(<ExposeTunnelForm container={edContainer} tunnels={edTunnels} endpoints={endpoints} networks={edNetworks()} ports={[9000, 80]} onDone={onDone} />)
}

test.each([
  { state: "the form", endpoints: ["bridge", "backend", "alpha"] },
  { state: "the form of a container on no user-defined network", endpoints: ["bridge"] },
])("$state has no axe violations", async ({ endpoints }) => {
  const { container } = edRender(endpoints)
  await screen.findByRole("form", { name: ED_FORM })
  await expectNoAxeViolations(container)
})

test("the form with an invalid port has no axe violations and marks the port invalid", async () => {
  const { container } = edRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "http")
  await expectNoAxeViolations(container)
  expect(within(form).getByLabelText("Port").getAttribute("aria-invalid")).toBe("true")
})

test("Tab goes through the tunnel, the network, the port and the hostname, then Plan once it is enabled", async () => {
  edRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  expect(await tabSequence(user, 4)).toEqual(["darkpanel-a", "alpha", "Port", "Hostname"])
  await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
  expect(await tabSequence(user, 1)).toEqual(["Plan"])
})

test("the plan dialog keeps Tab inside it and Escape gives focus back to Plan, with onDone called once", async () => {
  const onDone = vi.fn()
  edRender(["bridge", "backend", "alpha"], onDone)
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: ED_FORM })
  await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(onDone).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: ED_FORM })).getByRole("button", { name: "Plan" }))
})
