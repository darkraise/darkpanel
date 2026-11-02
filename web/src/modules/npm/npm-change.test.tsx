import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Change, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { NpmChange, NpmControllerChange } from "./npm-change"
import { npmFleetKey, npmKey } from "./types"

const NC_ACTION = "npm.proxyhost.add"
const NC_INPUT = { domains: ["app.example.com"], forwardScheme: "http", forwardHost: "web", forwardPort: 8080 }
const NC_DONE = { id: "c1", hostId: "h1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function ncPlan(action: string, hostId: string | null, over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId, action, version: 1, inputHash: "h",
    diff: [{ op: "+", text: `${action} app.example.com` }], guards: [], expiresAt: "2026-11-03T09:00:00Z", confirmation: null, ...over,
  }
}

function ncAvailable(action: string): StubHandler {
  return { body: { module: "npm", available: true, reason: null, actions: [{ name: action, available: true, reason: null }] } }
}

function ncRender(input: Record<string, unknown> | null, routes: Record<string, StubHandler> = {}, onApplied?: (change: Change) => void) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/npm/availability": ncAvailable(NC_ACTION),
    [`POST /api/v1/hosts/h1/actions/${NC_ACTION}/v1/plan`]: { body: ncPlan(NC_ACTION, "h1") },
    [`POST /api/v1/hosts/h1/actions/${NC_ACTION}/v1/apply`]: { body: NC_DONE },
    ...routes,
  })
  const onClose = vi.fn()
  const result = renderWithProviders(
    <NpmChange hostId="h1" action={NC_ACTION} title="Add a proxy host" input={input} onClose={onClose} onApplied={onApplied} />,
  )
  return { calls, onClose, ...result }
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = ncRender(null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the action on the host at version 1 and applies it without a revert timeout", async () => {
  const { calls } = ncRender(NC_INPUT)
  const dialog = await screen.findByRole("dialog", { name: "Add a proxy host" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ npm.proxyhost.add app.example.com")
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/hosts/h1/actions/npm.proxyhost.add/v1/plan")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: NC_INPUT })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("a module the host reports unavailable cannot be applied", async () => {
  ncRender(NC_INPUT, {
    "GET /api/v1/hosts/h1/modules/npm/availability": {
      body: { module: "npm", available: false, reason: "Docker 28.0 or later with raw-table protection is required", actions: [] },
    },
  })
  const dialog = await screen.findByRole("dialog", { name: "Add a proxy host" })
  expect(await within(dialog).findByText("Docker 28.0 or later with raw-table protection is required")).toBeTruthy()
  await within(dialog).findByLabelText("Planned changes")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("onApplied receives the change the apply answered, its output included", async () => {
  const onApplied = vi.fn()
  const { calls } = ncRender(
    NC_INPUT,
    { [`POST /api/v1/hosts/h1/actions/${NC_ACTION}/v1/apply`]: { body: { ...NC_DONE, output: { id: 7 } } } },
    onApplied,
  )
  const dialog = await screen.findByRole("dialog", { name: "Add a proxy host" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(1))
  expect(onApplied.mock.calls[0]?.[0]).toMatchObject({ id: "c1", output: { id: 7 } })
  expect(calls.filter((c) => c.path.endsWith("/apply"))).toHaveLength(1)
})

test("closing the dialog reports the close and refetches that host's resources and the fleet read only", async () => {
  const { client, onClose } = ncRender(NC_INPUT)
  client.setQueryData(npmKey("h1", "instance.status"), {})
  client.setQueryData(npmKey("h1", "proxyhost.list"), {})
  client.setQueryData(npmKey("h2", "instance.status"), {})
  client.setQueryData(npmFleetKey, {})
  client.setQueryData(["tailnet", "policy"], {})
  const dialog = await screen.findByRole("dialog", { name: "Add a proxy host" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(npmKey("h1", "instance.status"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(npmKey("h1", "proxyhost.list"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(npmFleetKey)?.isInvalidated).toBe(true)
  expect(client.getQueryState(npmKey("h2", "instance.status"))?.isInvalidated).toBe(false)
  expect(client.getQueryState(["tailnet", "policy"])?.isInvalidated).toBe(false)
})

test("a controller change plans on the controller without a host or an availability check", async () => {
  const action = "npm.expose.container"
  const input = { hostId: "h1", container: "web", port: 8080, network: "app", domains: ["app.example.com"] }
  const calls = stubFetch({ [`POST /api/v1/actions/${action}/v1/plan`]: { body: ncPlan(action, null) } })
  renderWithProviders(<NpmControllerChange action={action} title="Expose web" input={input} onClose={vi.fn()} />)
  const dialog = await screen.findByRole("dialog", { name: "Expose web" })
  expect(within(dialog).getByText("npm.expose.container/v1 on the controller")).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ npm.expose.container app.example.com")
  expect(calls.map((c) => [c.method, c.path, c.body])).toEqual([["POST", "/api/v1/actions/npm.expose.container/v1/plan", { input }]])
})

test("a controller change stays closed for a null input", () => {
  const calls = stubFetch({})
  renderWithProviders(<NpmControllerChange action="npm.expose.container" title="Expose web" input={null} onClose={vi.fn()} />)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("closing a controller change refetches the npm reads and the input's host, not another host or module", async () => {
  const action = "npm.admin.unexpose"
  stubFetch({ [`POST /api/v1/actions/${action}/v1/plan`]: { body: ncPlan(action, null) } })
  const onClose = vi.fn()
  const { client } = renderWithProviders(
    <NpmControllerChange action={action} title="Remove the admin exposure" input={{ hostId: "h1", hostname: "npm.example.com" }} onClose={onClose} />,
  )
  client.setQueryData(npmFleetKey, {})
  client.setQueryData(npmKey("h1", "instance.status"), {})
  client.setQueryData(npmKey("h2", "instance.status"), {})
  client.setQueryData(["tailnet", "policy"], {})
  const dialog = await screen.findByRole("dialog", { name: "Remove the admin exposure" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(npmFleetKey)?.isInvalidated).toBe(true)
  expect(client.getQueryState(npmKey("h1", "instance.status"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(npmKey("h2", "instance.status"))?.isInvalidated).toBe(false)
  expect(client.getQueryState(["tailnet", "policy"])?.isInvalidated).toBe(false)
})
