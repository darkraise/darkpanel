import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { ROLLOUTS_QUERY_KEY } from "@/api/client"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { UpdateNowButton, releaseError } from "./update-now-dialog"

const PLAN = "POST /api/v1/actions/core.update.rollout/v1/plan"
const APPLY = "POST /api/v1/actions/core.update.rollout/v1/apply"

test("UX-7: releaseError accepts semantic versions and refuses the rest", () => {
  for (const ok of ["1.2.3", "0.0.1", "1.0.0-beta.1"]) expect(releaseError(ok)).toBeUndefined()
  for (const bad of ["", "v1.2.3", "1.2", "01.2.3", "1.2.3-"]) expect(releaseError(bad)).toBe("Enter a version such as 1.2.3.")
})

test("UX-7: update now plans a one-host rollout and links to the rollouts page", async () => {
  const calls = stubFetch({
    [PLAN]: { body: makePlan("core.update.rollout") },
    [APPLY]: { body: makeChange("core.update.rollout", { id: "r1" }) },
  })
  const { client } = renderWithProviders(<UpdateNowButton host={makeHost({ id: "h1" })} />)
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Update now" }))
  await user.type(screen.getByLabelText("Version"), "1.2.3")
  await user.click(screen.getByRole("radio", { name: "Beta" }))
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  expect((await screen.findByRole("link", { name: "Follow the rollout" })).getAttribute("href")).toBe("/rollouts")
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ROLLOUTS_QUERY_KEY })
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { hosts: ["h1"], version: "1.2.3", channel: "beta" } })
})

test("UX-7, UX-11: a bad version is refused by the field and keeps focus there", async () => {
  const calls = stubFetch({})
  renderWithProviders(<UpdateNowButton host={makeHost()} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Update now" }))
  await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Version")))
  await user.type(screen.getByLabelText("Version"), "v2{Enter}")
  screen.getByText("Enter a version such as 1.2.3.")
  expect(document.activeElement).toBe(screen.getByLabelText("Version"))
  expect(calls).toHaveLength(0)
})

test("UX-7: update now shows only for an enrolled host", () => {
  renderWithProviders(<UpdateNowButton host={makeHost({ state: "pending" })} />)
  expect(screen.queryByRole("button", { name: "Update now" })).toBeNull()
})

test("UX-11: Escape closes the form and returns focus to Update now", async () => {
  stubFetch({})
  renderWithProviders(<UpdateNowButton host={makeHost({ id: "h1" })} />)
  const user = userEvent.setup()
  const button = screen.getByRole("button", { name: "Update now" })
  await user.click(button)
  await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Version")))
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(button))
})
