import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { UpdatePolicyButton } from "./update-policy-dialog"

const PLAN = "POST /api/v1/actions/core.host.update-policy/v1/plan"
const APPLY = "POST /api/v1/actions/core.host.update-policy/v1/apply"

test("UX-6: a scheduled beta policy is planned with its days in the order shown", async () => {
  const calls = stubFetch({
    [PLAN]: { body: makePlan("core.host.update-policy", { diff: [{ op: "-", text: "web-1 follows stable, on demand" }, { op: "+", text: "web-1 follows beta, mon,fri 03:00" }] }) },
    [APPLY]: { body: makeChange("core.host.update-policy", null) },
  })
  renderWithProviders(<UpdatePolicyButton host={makeHost({ id: "h1" })} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Change update policy" }))
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("radio", { name: "Stable" })))
  await user.click(screen.getByRole("radio", { name: "Beta" }))
  await user.click(screen.getByRole("checkbox", { name: "Update on a schedule" }))
  await user.click(screen.getByRole("checkbox", { name: "Friday" }))
  await user.click(screen.getByRole("checkbox", { name: "Monday" }))
  await user.type(screen.getByLabelText("Time (UTC)"), "03:00")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toContain("web-1 follows stable, on demand")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
    input: { hostId: "h1", channel: "beta", schedule: { days: ["mon", "fri"], time: "03:00" } },
  })
})

test("UX-6: on-demand stable is planned with a null schedule", async () => {
  const calls = stubFetch({ [PLAN]: { body: makePlan("core.host.update-policy") } })
  renderWithProviders(<UpdatePolicyButton host={makeHost({ id: "h1" })} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Change update policy" }))
  await user.click(screen.getByRole("button", { name: "Continue" }))
  await screen.findByRole("dialog")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { hostId: "h1", channel: "stable", schedule: null } })
})

test("UX-6, UX-11: a schedule with no day or a bad time is refused by the field", async () => {
  const calls = stubFetch({})
  renderWithProviders(<UpdatePolicyButton host={makeHost()} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Change update policy" }))
  await user.click(screen.getByRole("checkbox", { name: "Update on a schedule" }))
  await user.type(screen.getByLabelText("Time (UTC)"), "25:00")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  screen.getByText("Choose at least one day.")
  screen.getByText("Enter a UTC time such as 03:00.")
  expect(document.activeElement).toBe(screen.getByRole("checkbox", { name: "Monday" }))
  expect(calls).toHaveLength(0)
})

test("UX-6, UX-11: with a day chosen, a bad time moves focus to the time field", async () => {
  const calls = stubFetch({})
  renderWithProviders(<UpdatePolicyButton host={makeHost()} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Change update policy" }))
  await user.click(screen.getByRole("checkbox", { name: "Update on a schedule" }))
  await user.click(screen.getByRole("checkbox", { name: "Monday" }))
  await user.type(screen.getByLabelText("Time (UTC)"), "25:00")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  screen.getByText("Enter a UTC time such as 03:00.")
  expect(document.activeElement).toBe(screen.getByLabelText("Time (UTC)"))
  expect(calls).toHaveLength(0)
})
