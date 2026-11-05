import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StatusBanners, StatusSummary } from "./status-panel"
import type { FirewallStatus } from "./types"

function spaRender(status: FirewallStatus) {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.state.repair", version: 1, inputHash: "h", diff: [{ op: "~", text: "reload ufw" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(
    <>
      <StatusBanners hostId="h1" status={status} />
      <StatusSummary status={status} />
    </>,
  )
}

test.each([
  ["an unsupported configuration", { supported: false, unsupported: ["/etc/ufw/before.init is executable"] }],
  ["chains that differ", { inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] }],
  ["a sync check that failed", { inSync: false, outOfSync: [], syncError: "iptables-save failed" }],
  ["an inactive firewall", { active: false }],
] as Array<[string, Partial<FirewallStatus>]>)("the banners and summary for %s have no violations", async (_name, over) => {
  const { container } = spaRender(fwStatus(over))
  await screen.findByLabelText("Configured state")
  await expectNoAxeViolations(container)
})

test("the repair dialog is opened and closed by keyboard and Escape returns focus to Repair", async () => {
  spaRender(fwStatus({ inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] }))
  const user = userEvent.setup()
  const repair = await screen.findByRole("button", { name: "Repair" })
  repair.focus()
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Repair the firewall" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Repair")
})
