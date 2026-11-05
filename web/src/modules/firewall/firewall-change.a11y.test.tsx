import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { FirewallChange } from "./firewall-change"

const FCA_DETAIL = 'The rule "allow in tcp from any to any port 22" was added by hand; pass manual to delete it.'

function fcaPlans(call: FetchCall): StubResponse {
  const input = (call.body as { input: { manual?: boolean } }).input
  if (input.manual === true) {
    return {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-28T09:00:00Z", confirmation: null },
    }
  }
  return { status: 409, body: { type: "about:blank", title: "Manual action required", status: 409, detail: FCA_DETAIL, code: "firewall.manual_required" } }
}

function FcaHost() {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  return (
    <>
      <button type="button" onClick={() => setInput({ id: "manual-0123456789abcdef" })}>
        Open the change
      </button>
      <FirewallChange hostId="h1" action="firewall.rule.delete" title="Delete rule 2" input={input} onClose={() => setInput(null)} />
    </>
  )
}

async function fcaOpen(): Promise<{ user: UserEvent; ask: HTMLElement }> {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.delete", available: true, reason: null }] },
    },
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": fcaPlans,
  })
  renderWithProviders(<FcaHost />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open the change" }))
  return { user, ask: await screen.findByRole("dialog", { name: "Change a manual rule?" }) }
}

test("Tab stays inside the manual-rule question", async () => {
  const { user, ask } = await fcaOpen()
  await expectNoAxeViolations(ask)
  for (let i = 0; i < 6; i++) {
    await tabSequence(user, 1)
    expect(ask.contains(document.activeElement)).toBe(true)
  }
})

test("Cancel on the manual-rule question returns focus to the button that opened the change", async () => {
  const { user, ask } = await fcaOpen()
  await user.click(within(ask).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("Escape on the change dialog that follows the question returns focus to the button that opened the change", async () => {
  const { user, ask } = await fcaOpen()
  await user.click(within(ask).getByRole("button", { name: "Change it anyway" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  await within(dialog).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})
