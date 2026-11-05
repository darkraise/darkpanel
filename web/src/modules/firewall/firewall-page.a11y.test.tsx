import { screen, waitFor, within } from "@testing-library/react"
import userEvent, { type UserEvent } from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwDocker, fwRule, fwSnapshot, fwStatus } from "./fixtures"
import FirewallRoutes from "./routes"
import type { FirewallStatus } from "./types"

const FPA_STATUS = "GET /api/v1/hosts/h1/resources/firewall/status"

function fpaChange(action: string, over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action, version: 1, class: "revertible", state: "pending", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: "2099-01-01T00:00:00Z", output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

// fpaRender serves a firewall whose status follows the status the test holds,
// so a change applied through a stub moves what the next read answers.
function fpaRender(read: () => FirewallStatus, routes: Record<string, StubHandler> = {}) {
  stubFetch({
    [FPA_STATUS]: () => ({ body: read() }),
    "GET /api/v1/hosts/h1/resources/firewall/snapshots": { body: { snapshots: [fwSnapshot()] } },
    "GET /api/v1/hosts/h1/resources/firewall/docker": { body: fwDocker({ available: false, reason: "the Docker socket is not present", enabled: false, health: "off" }) },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    ...routes,
  })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/*" element={<FirewallRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/firewall" },
  )
}

function fpaPlan(action: string): StubHandler {
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
}

// fpaTabTo presses Tab until the focused element has the given name.
async function fpaTabTo(user: UserEvent, name: string, limit = 30): Promise<void> {
  for (let i = 0; i < limit; i++) {
    const [focused] = await tabSequence(user, 1)
    if (focused === name) return
  }
  throw new Error(`Tab never reached ${name}; focus is on ${focusedName()}`)
}

test("the firewall page has no violations with its banners, rules and Docker panel", async () => {
  const { container } = fpaRender(() =>
    fwStatus({ inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] as FirewallStatus["outOfSync"] }),
  )
  await screen.findByRole("table", { name: "Firewall rules" })
  await screen.findByRole("button", { name: "Repair" })
  await expectNoAxeViolations(container)
})

test("a state change applied from the page keeps its countdown, and Confirm and Close work by keyboard", async () => {
  let active = false
  const status = () => fwStatus({ active, configured: { enabled: active, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } } })
  const pending = fpaChange("firewall.state.enable")
  fpaRender(status, {
    "POST /api/v1/hosts/h1/actions/firewall.state.enable/v1/plan": fpaPlan("firewall.state.enable"),
    "POST /api/v1/hosts/h1/actions/firewall.state.enable/v1/apply": () => {
      active = true
      return { body: pending }
    },
    "GET /api/v1/changes/c1": () => ({ body: active ? pending : fpaChange("firewall.state.enable", { state: "confirmed", revertDeadline: null }) }),
    "POST /api/v1/changes/c1/confirm": () => ({ body: fpaChange("firewall.state.enable", { state: "confirmed", revertDeadline: null }) }),
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Enable")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Enable the firewall" })
  await within(dialog).findByLabelText("Planned changes")
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByRole("timer")
  // The status read that follows the apply says the firewall is active.
  await screen.findByText("UFW is active.")
  expect(screen.getByRole("dialog", { name: "Enable the firewall" })).toBe(dialog)
  expect(within(dialog).getByRole("timer")).toBeTruthy()
  await fpaTabTo(user, "Confirm")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("confirmed")
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Disable")
})

test("a state change can be reverted by keyboard from the dialog that applied it", async () => {
  let active = true
  const status = () => fwStatus({ active, configured: { enabled: active, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } } })
  const pending = fpaChange("firewall.state.disable")
  const reverted = fpaChange("firewall.state.disable", { state: "reverted", revertDeadline: null })
  fpaRender(status, {
    "POST /api/v1/hosts/h1/actions/firewall.state.disable/v1/plan": fpaPlan("firewall.state.disable"),
    "POST /api/v1/hosts/h1/actions/firewall.state.disable/v1/apply": () => {
      active = false
      return { body: pending }
    },
    "GET /api/v1/changes/c1": () => ({ body: active ? reverted : pending }),
    "POST /api/v1/changes/c1/revert": () => {
      active = true
      return { body: reverted }
    },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Disable")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Disable the firewall" })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByRole("timer")
  await screen.findByText("UFW is inactive.")
  await fpaTabTo(user, "Revert")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("reverted")
  expect(within(dialog).queryByRole("timer")).toBeNull()
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await screen.findByText("UFW is active.")
  expect(focusedName()).toBe("Disable")
})

test("closing the change dialog of a deleted rule leaves focus on the rules, not on the page", async () => {
  let rules = [fwRule({ id: "01JD0000000000000000000001" }), fwRule({ id: "01JD0000000000000000000002", toPort: "8080", comment: "alt" })]
  const applied = fpaChange("firewall.rule.delete", { state: "completed", class: "revertible", revertDeadline: null })
  fpaRender(() => fwStatus({ rules }), {
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan": fpaPlan("firewall.rule.delete"),
    "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/apply": () => {
      rules = rules.slice(1)
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Delete rule 1")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: /^Delete rule:/ })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(() => expect(within(screen.getByRole("table", { name: "Firewall rules" })).getAllByRole("row")).toHaveLength(2))
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Rules" }))
})

test("closing the change dialog of a repair leaves focus on the status, not on the page", async () => {
  let inSync = false
  const applied = fpaChange("firewall.state.repair", { state: "completed", revertDeadline: null })
  fpaRender(() => fwStatus({ inSync, outOfSync: inSync ? [] : ([{ family: "v4", table: "filter", chain: "ufw-user-input" }] as FirewallStatus["outOfSync"]) }), {
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/plan": fpaPlan("firewall.state.repair"),
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/apply": () => {
      inSync = true
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Firewall rules" })
  await fpaTabTo(user, "Repair")
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Repair the firewall" })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await fpaTabTo(user, "Apply")
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(() => expect(screen.queryByText("Out of sync")).toBeNull())
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Firewall state" }))
})
