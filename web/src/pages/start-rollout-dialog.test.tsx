import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { ROLLOUTS_QUERY_KEY } from "@/api/client"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { RolloutsPage } from "./rollouts"
import { StartRolloutButton } from "./start-rollout-dialog"

const ROLLOUTS = "POST /api/v1/actions/core.update.rollouts/v1/read"
const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PLAN = "POST /api/v1/actions/core.update.rollout/v1/plan"
const APPLY = "POST /api/v1/actions/core.update.rollout/v1/apply"
const fleet = [
  makeHost({ id: "c", name: "controller", isLocal: true }),
  makeHost({ id: "a", name: "web-1" }),
  makeHost({ id: "p", name: "new-1", state: "pending" }),
  makeHost({ id: "b", name: "web-2" }),
]

test("UX-9: lists enrolled hosts, marks the controller and plans the checked ones in list order", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: fleet } } },
    [PLAN]: { body: makePlan("core.update.rollout") },
    [APPLY]: { body: makeChange("core.update.rollout", { id: "r9" }) },
  })
  const { client } = renderWithProviders(<StartRolloutButton />)
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  const group = within(await screen.findByRole("group", { name: "Hosts" }))
  const boxes = group.getAllByRole("checkbox")
  expect(boxes).toHaveLength(3)
  ;["controller (updates first)", "web-1", "web-2"].forEach((name, i) => expect(boxes[i]).toBe(group.getByRole("checkbox", { name })))
  await waitFor(() => expect(document.activeElement).toBe(boxes[0]))
  await user.click(screen.getByRole("checkbox", { name: "web-2" }))
  await user.click(screen.getByRole("checkbox", { name: "controller (updates first)" }))
  await user.type(screen.getByLabelText("Version"), "1.3.0")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { hosts: ["c", "b"], version: "1.3.0", channel: "stable" } })
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ROLLOUTS_QUERY_KEY })
})

test("UX-9, UX-11: no host chosen is refused and focus moves to the first host", async () => {
  const calls = stubFetch({ [HOSTS]: { body: { output: { hosts: fleet } } } })
  renderWithProviders(<StartRolloutButton />)
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  await user.type(await screen.findByLabelText("Version"), "1.3.0")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  screen.getByText("Choose at least one host.")
  expect(document.activeElement).toBe(screen.getByRole("checkbox", { name: "controller (updates first)" }))
  expect(calls.filter((c) => c.path.endsWith("/plan"))).toHaveLength(0)
})

test("UX-8, UX-9: the Rollouts page offers Start rollout in its header and its empty state", async () => {
  stubFetch({ [ROLLOUTS]: { body: { output: { rollouts: [] } } }, [HOSTS]: { body: { output: { hosts: fleet } } } })
  renderWithProviders(<RolloutsPage />)
  await screen.findByText("No rollouts yet")
  expect(screen.getAllByRole("button", { name: "Start rollout" })).toHaveLength(2)
})

test("UX-11: Escape closes the form and returns focus to Start rollout", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: fleet } } } })
  renderWithProviders(<StartRolloutButton />)
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("checkbox", { name: "controller (updates first)" })))
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(start))
})

test("UX-9, UX-11: a bad version with a host chosen moves focus to Version", async () => {
  const calls = stubFetch({ [HOSTS]: { body: { output: { hosts: fleet } } } })
  renderWithProviders(<StartRolloutButton />)
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  await user.click(await screen.findByRole("checkbox", { name: "web-1" }))
  await user.type(screen.getByLabelText("Version"), "v2")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  screen.getByText("Enter a version such as 1.2.3.")
  expect(document.activeElement).toBe(screen.getByLabelText("Version"))
  expect(calls.filter((c) => c.path.endsWith("/plan"))).toHaveLength(0)
})

test("UX-9, UX-11: with no enrolled host a refused Continue focuses the reason", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [makeHost({ id: "p", name: "new-1", state: "pending" })] } } } })
  renderWithProviders(<StartRolloutButton />)
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  await user.type(await screen.findByLabelText("Version"), "1.3.0")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const reason = screen.getByText("No host is enrolled yet, so no rollout can start.")
  await waitFor(() => expect(document.activeElement).toBe(reason))
})

test("UX-9: a failed start does not refresh the rollouts", async () => {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: fleet } } },
    [PLAN]: { body: makePlan("core.update.rollout") },
    [APPLY]: { body: makeChange("core.update.rollout", null, { state: "failed", error: { code: "change.failed", message: "The controller refused the change." } }) },
  })
  const { client } = renderWithProviders(<StartRolloutButton />)
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  const start = screen.getByRole("button", { name: "Start rollout" })
  await waitFor(() => expect(start.hasAttribute("disabled")).toBe(false))
  await user.click(start)
  await user.click(await screen.findByRole("checkbox", { name: "web-1" }))
  await user.type(screen.getByLabelText("Version"), "1.3.0")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("failed")
  expect(invalidate).not.toHaveBeenCalled()
})

test("UX-9: a host list that cannot be read says so beside Start rollout", async () => {
  stubFetch({ [HOSTS]: { status: 500, body: { type: "about:blank", title: "Down", status: 500, detail: "The controller could not read its database.", code: "internal" } } })
  renderWithProviders(<StartRolloutButton />)
  const message = await screen.findByText(/^The host list could not be read: /)
  const start = screen.getByRole("button", { name: "Start rollout" })
  expect(start.hasAttribute("disabled")).toBe(true)
  expect(start.getAttribute("aria-describedby")).toBe(message.id)
})

test("UX-9, UX-11: a rollout started from the empty state keeps its result and returns focus to the header", async () => {
  let started = false
  stubFetch({
    [ROLLOUTS]: () => ({ body: { output: { rollouts: started ? [{ id: "r9", version: "1.4.0", channel: "stable", state: "running", error: null, createdAt: "2026-11-11T08:00:00Z", updatedAt: "2026-11-11T08:00:00Z", hosts: [] }] : [] } } }),
    [HOSTS]: { body: { output: { hosts: fleet } } },
    [PLAN]: { body: makePlan("core.update.rollout") },
    [APPLY]: () => {
      started = true
      return { body: makeChange("core.update.rollout", { id: "r9" }) }
    },
  })
  renderWithProviders(<RolloutsPage />)
  const user = userEvent.setup()
  await screen.findByText("No rollouts yet")
  const [header, empty] = screen.getAllByRole("button", { name: "Start rollout" })
  if (header === undefined || empty === undefined) throw new Error("two Start rollout buttons expected")
  await waitFor(() => expect(empty.hasAttribute("disabled")).toBe(false))
  await user.click(empty)
  await user.click(await screen.findByRole("checkbox", { name: "web-1" }))
  await user.type(screen.getByLabelText("Version"), "1.4.0")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  await screen.findByRole("region", { name: "1.4.0 (stable)" })
  within(screen.getByRole("dialog")).getByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(header))
})
