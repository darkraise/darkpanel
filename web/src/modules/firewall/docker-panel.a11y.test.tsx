import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Change } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { DockerPanel } from "./docker-panel"
import { fwDocker } from "./fixtures"

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
