import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StateControls } from "./state-controls"
import type { FirewallStatus } from "./types"

function scRender(status: FirewallStatus): { calls: FetchCall[]; container: HTMLElement } {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
  }
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.state.enable/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.state.disable/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.defaults.set/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.logging.set/v1/plan": plan,
  })
  const { container } = renderWithProviders(<StateControls hostId="h1" status={status} />)
  return { calls, container }
}

async function scPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("an active firewall offers Disable, which plans firewall.state.disable", async () => {
  const { calls } = scRender(fwStatus())
  expect(screen.getByText("UFW is active.").textContent).toBe("UFW is active.")
  expect(screen.queryByRole("button", { name: "Enable" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Disable" }))
  expect(await scPlanned(calls, "Disable the firewall")).toEqual({ path: "/api/v1/hosts/h1/actions/firewall.state.disable/v1/plan", body: { input: {} } })
})

test("an inactive firewall offers Enable, which plans firewall.state.enable", async () => {
  const { calls } = scRender(fwStatus({ active: false }))
  await userEvent.setup().click(screen.getByRole("button", { name: "Enable" }))
  expect(await scPlanned(calls, "Enable the firewall")).toEqual({ path: "/api/v1/hosts/h1/actions/firewall.state.enable/v1/plan", body: { input: {} } })
})

test("sets a default policy for one direction, starting from its current value", async () => {
  const { calls } = scRender(fwStatus())
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Default policy" })
  const submit = within(form).getByRole("button", { name: "Set default policy" })
  expect(submit.hasAttribute("disabled")).toBe(true)
  await user.click(within(form).getByLabelText("outgoing"))
  expect(within(form).getByRole("radio", { name: "allow", checked: true })).toBeTruthy()
  await user.click(within(form).getByLabelText("reject"))
  await user.click(submit)
  expect(await scPlanned(calls, "Set the default outgoing policy to reject")).toEqual({
    path: "/api/v1/hosts/h1/actions/firewall.defaults.set/v1/plan",
    body: { input: { outgoing: "REJECT" } },
  })
})

test("sets the logging level", async () => {
  const { calls } = scRender(fwStatus())
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Logging" })
  expect(within(form).getByRole("button", { name: "Set logging" }).hasAttribute("disabled")).toBe(true)
  await user.click(within(form).getByLabelText("medium"))
  await user.click(within(form).getByRole("button", { name: "Set logging" }))
  expect(await scPlanned(calls, "Set logging to medium")).toEqual({
    path: "/api/v1/hosts/h1/actions/firewall.logging.set/v1/plan",
    body: { input: { level: "medium" } },
  })
})

test("the settings have no accessibility violations", async () => {
  const { container } = scRender(fwStatus())
  expect(await axe(container)).toHaveNoViolations()
})
