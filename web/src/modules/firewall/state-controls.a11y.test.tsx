import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StateControls } from "./state-controls"

function scaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.defaults.set/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.defaults.set", version: 1, inputHash: "h", diff: [{ op: "~", text: "incoming reject" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<StateControls hostId="h1" status={fwStatus()} />)
}

test("the default policy is set by keyboard, and Escape returns focus to the button that planned it", async () => {
  const { container } = scaRender()
  await expectNoAxeViolations(container)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Default policy" })
  within(form).getByRole("radio", { name: "deny", checked: true }).focus()
  await user.keyboard("{ArrowDown}")
  expect(within(form).getByRole("radio", { name: "reject", checked: true })).toBeTruthy()
  const submit = within(form).getByRole("button", { name: "Set default policy" })
  expect(submit.hasAttribute("disabled")).toBe(false)
  submit.focus()
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Set the default incoming policy to reject" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Set default policy")
})

function ScaHarness() {
  const [level, setLevel] = useState("low")
  return (
    <>
      <button type="button" onClick={() => setLevel("medium")}>
        The host changed
      </button>
      <StateControls hostId="h1" status={fwStatus({ configured: { ...fwStatus().configured, logLevel: level } })} />
    </>
  )
}

test("the drafts start again from the host's settings when those change, in the same card", async () => {
  stubFetch({})
  renderWithProviders(<ScaHarness />)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Logging" })
  await user.click(within(form).getByLabelText("high"))
  expect(within(form).getByRole("radio", { name: "high", checked: true })).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "The host changed" }))
  expect(within(screen.getByRole("form", { name: "Logging" })).getByRole("radio", { name: "medium", checked: true })).toBeTruthy()
  expect(within(screen.getByRole("form", { name: "Logging" })).getByRole("button", { name: "Set logging" }).hasAttribute("disabled")).toBe(true)
})
