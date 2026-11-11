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
