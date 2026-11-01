import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { TailscaleChange } from "./tailscale-change"

function tchPlan(action: string, over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
    diff: [{ op: "~", text: `${action} web-1` }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null, ...over,
  }
}

const TCH_APPLIED = { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function tchAvailability(actions: Record<string, string | null>): StubHandler {
  return {
    body: {
      module: "tailscale", available: true, reason: null,
      actions: Object.entries(actions).map(([name, reason]) => ({ name, available: reason === null, reason })),
    },
  }
}

function tchRender(action: string, input: Record<string, unknown> | null, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": tchAvailability({ [action]: null }),
    [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: tchPlan(action) },
    [`POST /api/v1/hosts/h1/actions/${action}/v1/apply`]: { body: TCH_APPLIED },
    ...routes,
  })
  const onClose = vi.fn()
  const result = renderWithProviders(<TailscaleChange hostId="h1" action={action} title="Change the node" input={input} onClose={onClose} />)
  return { calls, onClose, ...result }
}

async function tchApply(calls: FetchCall[]) {
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  return dialog
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = tchRender("tailscale.package.install", null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the action on the host and applies it without a revert timeout", async () => {
  const { calls } = tchRender("tailscale.package.install", {})
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ tailscale.package.install web-1")
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  await tchApply(calls)
  const plan = calls.find((c) => c.path.endsWith("/plan"))
  expect(plan?.path).toBe("/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan")
  expect(plan?.body).toEqual({ input: {} })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("tailscale.prefs.set asks for the revert timeout and sends it with the apply", async () => {
  const { calls } = tchRender("tailscale.prefs.set", { shieldsUp: true })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const field = await within(dialog).findByLabelText("Revert after (seconds)")
  expect((field as HTMLInputElement).value).toBe("60")
  const user = userEvent.setup()
  await user.clear(field)
  await user.type(field, "30")
  await tchApply(calls)
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { shieldsUp: true } })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmTimeoutSeconds: 30 })
})

test("an action that asks for the typed name applies only after it is typed", async () => {
  const action = "tailscale.node.logout"
  const { calls } = tchRender(action, {}, { [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: tchPlan(action, { confirmation: "web-1" }) } })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const user = userEvent.setup()
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  expect(apply.hasAttribute("disabled")).toBe(true)
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  await user.type(within(dialog).getByLabelText("Type web-1 to confirm"), "web-1")
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmation: "web-1" })
})

const TCH_DOWN = "tailscaled does not answer on its socket"

test.each([
  ["tailscale.node.logout", true],
  ["tailscale.package.install", false],
])("%s is applied only when the host reports it available (blocked: %s)", async (action, blocked) => {
  tchRender(action, {}, {
    "GET /api/v1/hosts/h1/modules/tailscale/availability": tchAvailability({ "tailscale.node.logout": TCH_DOWN, "tailscale.package.install": null }),
  })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  await within(dialog).findByLabelText("Planned changes")
  if (blocked) expect((await within(dialog).findByText(TCH_DOWN)).textContent).toBe(TCH_DOWN)
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(blocked))
})

test("closing the dialog reports the close and refetches only that host's resources", async () => {
  const { client, onClose } = tchRender("tailscale.package.install", {})
  client.setQueryData(["resource", "h1", "tailscale.status"], { backendState: "Running" })
  client.setQueryData(["resource", "h1", "tailscale.latency"], { regions: [] })
  client.setQueryData(["resource", "h2", "tailscale.status"], { backendState: "Running" })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(["resource", "h1", "tailscale.status"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h1", "tailscale.latency"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h2", "tailscale.status"])?.isInvalidated).toBe(false)
})

test("an applied change refetches the host's resources before the dialog closes", async () => {
  const { calls, client, onClose } = tchRender("tailscale.prefs.set", { shieldsUp: true })
  client.setQueryData(["resource", "h1", "tailscale.status"], { backendState: "Running" })
  client.setQueryData(["resource", "h2", "tailscale.status"], { backendState: "Running" })
  await tchApply(calls)
  await waitFor(() => expect(client.getQueryState(["resource", "h1", "tailscale.status"])?.isInvalidated).toBe(true))
  expect(client.getQueryState(["resource", "h2", "tailscale.status"])?.isInvalidated).toBe(false)
  expect(onClose).not.toHaveBeenCalled()
})
