import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Plan } from "@/api/types"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeActions } from "./change-actions"
import { ChangePage } from "./change"

function caaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

const caaPlan: Plan = {
  planId: "rp1", hostId: "h1", action: "cdx.rule.fix", version: 1, inputHash: "h", diff: [{ op: "+", text: "restore rule 443" }],
  guards: [], expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function caaRenderActions(change: Change, routes: Record<string, StubHandler> = {}) {
  stubFetch(routes)
  return renderWithProviders(<ChangeActions change={change} />)
}

// caaRenderPage renders the change page, whose answer follows the change
// the test's handlers set, as the page does when an action moves it on.
function caaRenderPage(read: () => Change, routes: Record<string, StubHandler>) {
  stubFetch({ "GET /api/v1/changes/c1": () => ({ body: read() }), "GET /api/v1/changes/c1/effects": { body: { effects: [] } }, ...routes })
  return renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path: "/changes/c1" },
  )
}

test("the undo dialog has no violations and Escape returns focus to Undo", async () => {
  caaRenderActions(caaChange())
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  const dialog = await screen.findByRole("dialog", { name: "Undo this change?" })
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Undo")
})

test("the release dialog has no violations, with its reason field named and its refusal announced", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }))
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = await screen.findByRole("dialog", { name: "Release this change?" })
  expect(within(dialog).getByRole("textbox", { name: "Reason" }).hasAttribute("required")).toBe(true)
  await user.type(within(dialog).getByRole("textbox", { name: "Reason" }), "   ")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("Enter why the change is given up.")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Release")
})

test("the repair dialog has no violations", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/repair": { body: { plan: caaPlan } } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
})

test("the actions of a change that needs repair, with the step decision, have no violations", async () => {
  const steps = [
    { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const { container } = caaRenderActions(caaChange({ class: "composite", state: "needs_repair" }), { "GET /api/v1/changes/c1/steps": { body: { steps } } })
  await screen.findByRole("group", { name: "Decide a step" })
  await expectNoAxeViolations(container)
})

test("an undo that moves the change on leaves focus on its notice, not on a button that is gone", async () => {
  let current = caaChange()
  caaRenderPage(() => current, {
    "POST /api/v1/changes/c1/undo": () => {
      current = caaChange({ state: "undoing", owner: "undo" })
      return { status: 202, body: current }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Undo" }))
  const dialog = await screen.findByRole("dialog", { name: "Undo this change?" })
  await user.click(within(dialog).getByRole("button", { name: "Undo" }))
  const notice = await screen.findByText("Undo started. The change is now undoing.")
  expect(notice.getAttribute("role")).toBe("status")
  await waitFor(() => expect(screen.queryByRole("button", { name: "Undo" })).toBeNull())
  expect(document.activeElement).toBe(notice)
})

test("a release by keyboard leaves focus on its notice", async () => {
  let current = caaChange({ state: "needs_repair" })
  caaRenderPage(() => current, {
    "POST /api/v1/changes/c1/release": () => {
      current = caaChange({ state: "abandoned", updatedAt: "2026-10-23T08:10:00Z" })
      return { body: { change: current, abandoned: [{ seq: 1 }] } }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Release" }))
  const dialog = await screen.findByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByRole("textbox", { name: "Reason" }), "stuck")
  await user.tab()
  expect(focusedName()).toBe("Release")
  await user.keyboard("{Enter}")
  const notice = await screen.findByText("Released. 1 effect abandoned.")
  expect(notice.getAttribute("role")).toBe("status")
  await waitFor(() => expect(screen.queryByRole("button", { name: "Release" })).toBeNull())
  expect(document.activeElement).toBe(notice)
})

test("closing a repair that was applied leaves focus on a notice of it", async () => {
  caaRenderActions(caaChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/repair": { body: { plan: caaPlan } },
    "POST /api/v1/hosts/h1/actions/cdx.rule.fix/v1/apply": { body: caaChange({ id: "r1", action: "cdx.rule.fix" }) },
    "GET /api/v1/changes/r1": { body: caaChange({ id: "r1", action: "cdx.rule.fix" }) },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  const notice = await screen.findByText("The repair was applied.")
  expect(notice.getAttribute("role")).toBe("status")
  expect(document.activeElement).toBe(notice)
})
