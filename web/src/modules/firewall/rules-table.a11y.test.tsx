import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { RulesTable } from "./rules-table"

async function rtaTabTo(user: UserEvent, name: string, limit = 30): Promise<void> {
  for (let i = 0; i < limit; i++) {
    const [focused] = await tabSequence(user, 1)
    if (focused === name) return
  }
  throw new Error(`Tab never reached ${name}; focus is on ${focusedName()}`)
}

function rtaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<RulesTable hostId="h1" rules={fwStatus().rules} />)
}

test("the rules table with an origin on every row and a read-only row has no violations", async () => {
  const { container } = rtaRender()
  await screen.findByRole("table", { name: "Firewall rules" })
  await expectNoAxeViolations(container)
})

test("every Delete button has a name that says which rule it deletes", async () => {
  rtaRender()
  const names = (await screen.findAllByRole("button", { name: /^Delete rule/ })).map((b) => b.getAttribute("aria-label"))
  expect(names).toEqual(["Delete rule 1", "Delete rule 2"])
})

test("the manual-rule question has no violations and Escape returns focus to the rule's Delete button", async () => {
  rtaRender()
  const user = userEvent.setup()
  await rtaTabTo(user, "Delete rule 2")
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Delete a manual rule?" })
  await expectNoAxeViolations(ask)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Delete rule 2")
})

test("the change dialog that follows the manual-rule question returns focus to the rule's Delete button", async () => {
  rtaRender()
  const user = userEvent.setup()
  await rtaTabTo(user, "Delete rule 2")
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Delete a manual rule?" })
  await rtaTabTo(user, "Delete it anyway")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: /^Delete rule:/ })
  await within(dialog).findByLabelText("Planned changes")
  expect(ask.isConnected).toBe(false)
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Delete rule 2")
})
