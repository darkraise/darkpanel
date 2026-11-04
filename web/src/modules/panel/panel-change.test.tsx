import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { PanelChange } from "./panel-change"
import { panelStatusKey } from "./types"

const PC_INPUT = { via: "npm", hostname: "panel.example.org", hostId: "h1" }
const PC_DONE = { id: "c1", hostId: null, state: "applying", class: "composite", error: null, output: null, revertDeadline: null }

function pcPlan(action: string): Plan {
  return {
    planId: "p1", hostId: null, action, version: 1, inputHash: "h",
    diff: [{ op: "+", text: `${action} panel.example.org` }], guards: [], expiresAt: "2026-11-04T13:00:00Z", confirmation: null,
  }
}

function pcRender(input: Record<string, unknown> | null, action = "panel.exposure.add") {
  const calls = stubFetch({
    [`POST /api/v1/actions/${action}/v1/plan`]: { body: pcPlan(action) },
    [`POST /api/v1/actions/${action}/v1/apply`]: { body: PC_DONE },
  })
  const onClose = vi.fn()
  const result = renderWithProviders(<PanelChange hostId="h1" action={action} title="Expose the panel" input={input} onClose={onClose} />)
  return { calls, onClose, ...result }
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = pcRender(null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the composite on the controller at version 1, with no host and no availability check", async () => {
  const { calls } = pcRender(PC_INPUT)
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  expect(within(dialog).getByText("panel.exposure.add/v1 on the controller")).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ panel.exposure.add panel.example.org")
  expect(calls.map((c) => [c.method, c.path, c.body])).toEqual([["POST", "/api/v1/actions/panel.exposure.add/v1/plan", { input: PC_INPUT }]])
})

test("applies the plan it showed", async () => {
  const { calls } = pcRender({}, "panel.exposure.remove")
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.path).toBe("/api/v1/actions/panel.exposure.remove/v1/apply")
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("closing reports the close and refetches the panel reads and that host's resources only", async () => {
  const { client, onClose } = pcRender(PC_INPUT)
  client.setQueryData(panelStatusKey, {})
  client.setQueryData(["resource", "h1", "npm.instance.status"], {})
  client.setQueryData(["resource", "h2", "npm.instance.status"], {})
  client.setQueryData(["npm", "instances"], {})
  client.setQueryData(["tailnet", "policy"], {})
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(panelStatusKey)?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h1", "npm.instance.status"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h2", "npm.instance.status"])?.isInvalidated).toBe(false)
  expect(client.getQueryState(["npm", "instances"])?.isInvalidated).toBe(false)
  expect(client.getQueryState(["tailnet", "policy"])?.isInvalidated).toBe(false)
})
