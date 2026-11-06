import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Change } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { DockerPanel } from "./docker-panel"
import { fwDocker } from "./fixtures"
import type { DockerStatus, Exemption } from "./types"

test("closing the change dialog of an integration just enabled leaves focus on the Docker integration, not on the page", async () => {
  let enabled = false
  const applied: Change = {
    id: "c1", hostId: "h1", action: "firewall.docker.enable", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/docker": () => ({ body: fwDocker({ enabled }) }),
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.docker.enable/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.docker.enable", version: 1, inputHash: "h", diff: [{ op: "~", text: "firewall.docker.enable" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    "POST /api/v1/hosts/h1/actions/firewall.docker.enable/v1/apply": () => {
      enabled = true
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  renderWithProviders(<DockerPanel hostId="h1" />)
  const user = userEvent.setup()
  const enable = await screen.findByRole("button", { name: "Enable integration" })
  enable.focus()
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Enable the Docker integration" })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  within(dialog).getByRole("button", { name: "Apply" }).focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await screen.findByRole("button", { name: "Disable integration" })
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})

const dpaNat: Exemption = { origDestination: "203.0.113.5", origPort: 8443, destination: "192.168.50.10", proto: "tcp", port: 443 }
const dpaProposed: Exemption = { origDestination: "203.0.113.7", origPort: 2222, destination: "192.168.50.20", proto: "tcp", port: 22 }

// dpaRender serves the Docker resource as first until an apply, and as second afterwards, which is what the panel
// reads again when the change dialog applies and closes.
function dpaRender(first: DockerStatus, second: DockerStatus) {
  let current = first
  const applied: Change = {
    id: "c1", hostId: "h1", action: "firewall.docker.set-sources", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/docker": () => ({ body: current }),
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.docker.set-sources", version: 1, inputHash: "h", diff: [{ op: "~", text: "sources" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    "POST /api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/apply": () => {
      current = second
      return { body: applied }
    },
    "GET /api/v1/changes/c1": { body: applied },
  })
  renderWithProviders(<DockerPanel hostId="h1" />)
}

// dpaApplyAndClose applies the open change dialog titled title, waits until settled holds (the panel has read the
// host again and the control that opened the dialog has changed or gone), and closes the dialog from its footer.
async function dpaApplyAndClose(user: ReturnType<typeof userEvent.setup>, title: string, settled: () => void) {
  const dialog = await screen.findByRole("dialog", { name: title })
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  within(dialog).getByRole("button", { name: "Apply" }).focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  await waitFor(settled)
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  close.focus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
}

test("closing the change dialog of a removed exemption leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [dpaNat] }, foreignForwards: [] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const remove = await screen.findByRole("button", { name: "Remove exemption 1" })
  remove.focus()
  await user.keyboard("{Enter}")
  await dpaApplyAndClose(user, "Remove a forward exemption", () => expect(screen.queryByRole("button", { name: "Remove exemption 1" })).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})

test("closing the change dialog of an added proposal leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [dpaProposed] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [dpaProposed] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const add = await screen.findByRole("button", { name: "Add proposed exemption 1" })
  add.focus()
  await user.keyboard("{Enter}")
  await dpaApplyAndClose(user, "Add a forward exemption", () => expect(screen.queryByRole("button", { name: "Add proposed exemption 1" })).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})

test("closing the change dialog of new private sources leaves focus on the Docker integration, not on the page", async () => {
  dpaRender(
    fwDocker({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] }, foreignForwards: [] }),
    fwDocker({ settings: { privateSources: ["10.0.0.0/8", "192.168.0.0/16"], forwardExemptions: [] }, foreignForwards: [] }),
  )
  const user = userEvent.setup()
  const box = await screen.findByLabelText("Private source ranges")
  await user.type(box, "{Enter}192.168.0.0/16")
  await user.click(screen.getByRole("button", { name: "Set private sources" }))
  await dpaApplyAndClose(user, "Set the private sources", () =>
    expect(screen.getByRole("button", { name: "Set private sources" }).hasAttribute("disabled")).toBe(true),
  )
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "Docker integration" }))
})
