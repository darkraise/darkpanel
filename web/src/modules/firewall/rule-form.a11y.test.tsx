import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { AddRule } from "./rule-form"

const RFA_PLAN = "POST /api/v1/hosts/h1/actions/firewall.rule.add/v1/plan"
const RFA_APPLY = "POST /api/v1/hosts/h1/actions/firewall.rule.add/v1/apply"

const rfaPending = {
  id: "c1", hostId: "h1", action: "firewall.rule.add", version: 1, class: "revertible", state: "pending", actor: "user:u1",
  createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: "2099-01-01T00:00:00Z", output: null, error: null,
  compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function rfaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.add", available: true, reason: null }] },
    },
    [RFA_PLAN]: {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.add", version: 1, inputHash: "h", diff: [{ op: "+", text: "allow in tcp to any port 443" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    [RFA_APPLY]: { body: rfaPending },
    "GET /api/v1/changes/c1": { body: rfaPending },
    "POST /api/v1/changes/c1/confirm": { body: { ...rfaPending, state: "confirmed", revertDeadline: null } },
  })
  return renderWithProviders(<AddRule hostId="h1" />)
}

// rfaPlanned opens the form with the keyboard, fills the port and submits it
// with Enter, which closes the form and opens the change dialog.
async function rfaPlanned() {
  const user = userEvent.setup()
  const [first] = await tabSequence(user, 1)
  expect(first).toBe("Add rule")
  await user.keyboard("{Enter}")
  const form = await screen.findByRole("form", { name: "Rule" })
  await user.type(within(form).getByLabelText("To port"), "443{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Add a rule" })
  await within(dialog).findByLabelText("Planned changes")
  expect(screen.queryByRole("form", { name: "Rule" })).toBeNull()
  return { user, dialog }
}

test("the rule form has no violations and keeps Tab inside it", async () => {
  rfaRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Add rule" }))
  const dialog = await screen.findByRole("dialog", { name: "Add a rule" })
  await expectNoAxeViolations(dialog)
  for (let i = 0; i < 40; i++) {
    await tabSequence(user, 1)
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
})

test("Escape in the rule form returns focus to Add rule", async () => {
  rfaRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Add rule" }))
  await screen.findByRole("form", { name: "Rule" })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})

test("the change dialog that replaces the form has no violations", async () => {
  rfaRender()
  const { dialog } = await rfaPlanned()
  await expectNoAxeViolations(dialog)
})

test("closing the change dialog of an added rule with Escape returns focus to Add rule", async () => {
  rfaRender()
  const { user } = await rfaPlanned()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})

test("closing the change dialog after Apply and Confirm returns focus to Add rule", async () => {
  rfaRender()
  const { user, dialog } = await rfaPlanned()
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  apply.focus()
  await user.keyboard("{Enter}")
  const confirm = await within(dialog).findByRole("button", { name: "Confirm" })
  confirm.focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("confirmed")
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add rule")
})
