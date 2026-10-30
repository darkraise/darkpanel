import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { DockerChange } from "./docker-change"
import { dockerKey } from "./types"

function dcPlan(action: string, over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
    diff: [{ op: "~", text: `${action} web` }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null, ...over,
  }
}

const DC_DONE = { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function dcAvailable(action: string): StubHandler {
  return { body: { module: "docker", available: true, reason: null, actions: [{ name: action, available: true, reason: null }] } }
}

function dcRender(action: string, input: Record<string, unknown> | null, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/docker/availability": dcAvailable(action),
    [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: dcPlan(action) },
    [`POST /api/v1/hosts/h1/actions/${action}/v1/apply`]: { body: DC_DONE },
    ...routes,
  })
  const onClose = vi.fn()
  const result = renderWithProviders(<DockerChange hostId="h1" action={action} title="Start web" input={input} onClose={onClose} />)
  return { calls, onClose, ...result }
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = dcRender("docker.container.start", null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the action on the host and applies it without a revert timeout", async () => {
  const { calls } = dcRender("docker.container.start", { container: "web" })
  const dialog = await screen.findByRole("dialog", { name: "Start web" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ docker.container.start web")
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/hosts/h1/actions/docker.container.start/v1/plan")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { container: "web" } })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("an action that asks for the typed name applies only after it is typed", async () => {
  const action = "docker.compose.down"
  const { calls } = dcRender(action, { project: "blog" }, { [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: dcPlan(action, { confirmation: "blog" }) } })
  const dialog = await screen.findByRole("dialog", { name: "Start web" })
  const user = userEvent.setup()
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  expect(apply.hasAttribute("disabled")).toBe(true)
  await user.type(within(dialog).getByLabelText("Type blog to confirm"), "blog")
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmation: "blog" })
})

test("a module the host reports unavailable cannot be applied", async () => {
  const action = "docker.container.start"
  dcRender(action, { container: "web" }, {
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: false, reason: "the Docker socket is missing", actions: [] } },
  })
  const dialog = await screen.findByRole("dialog", { name: "Start web" })
  expect((await within(dialog).findByText("the Docker socket is missing")).textContent).toBe("the Docker socket is missing")
  await within(dialog).findByLabelText("Planned changes")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("closing the dialog reports the close and refetches only that host's resources", async () => {
  const { client, onClose } = dcRender("docker.container.start", { container: "web" })
  client.setQueryData(dockerKey("h1", "containers"), { containers: [] })
  client.setQueryData(dockerKey("h1", "networks"), { networks: [] })
  client.setQueryData(dockerKey("h2", "containers"), { containers: [] })
  const dialog = await screen.findByRole("dialog", { name: "Start web" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(dockerKey("h1", "containers"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(dockerKey("h1", "networks"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(dockerKey("h2", "containers"))?.isInvalidated).toBe(false)
})
