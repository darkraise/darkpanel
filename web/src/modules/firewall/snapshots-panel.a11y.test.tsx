import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { formatTime } from "@/lib/format"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwSnapshot } from "./fixtures"
import { SnapshotsPanel } from "./snapshots-panel"

const SPA_NEW = `Restore the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}`

function spaRender() {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/snapshots": {
      body: {
        snapshots: [
          fwSnapshot({ id: "s-base", createdAt: "2026-10-20T08:00:00Z", changeId: "c0", baseline: true, pinned: true }),
          fwSnapshot({ id: "s-new", createdAt: "2026-10-24T08:00:00Z", changeId: "c2" }),
        ],
      },
    },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.snapshot.restore/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.snapshot.restore", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 8080 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  return renderWithProviders(<SnapshotsPanel hostId="h1" />)
}

test("the snapshots list, with its select boxes and Restore buttons named, has no violations", async () => {
  const { container } = spaRender()
  await screen.findByRole("table", { name: "Firewall snapshots" })
  expect(screen.getAllByRole("checkbox").map((c) => c.getAttribute("aria-label"))).toEqual([
    `Select the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}`,
    `Select the snapshot taken ${formatTime("2026-10-20T08:00:00Z")}`,
  ])
  await expectNoAxeViolations(container)
})

test("the restore question has no violations, and Escape returns focus to the snapshot's Restore button", async () => {
  spaRender()
  const user = userEvent.setup()
  const restore = await screen.findByRole("button", { name: SPA_NEW })
  restore.focus()
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  expect(within(ask).getByRole("checkbox", { name: "Allow the restore to add or remove manual rules" })).toBeTruthy()
  await expectNoAxeViolations(ask)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe(SPA_NEW)
})

test("the change dialog that follows the restore question returns focus to the snapshot's Restore button", async () => {
  spaRender()
  const user = userEvent.setup()
  const restore = await screen.findByRole("button", { name: SPA_NEW })
  restore.focus()
  await user.keyboard("{Enter}")
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  await user.click(within(ask).getByRole("button", { name: "Plan the restore" }))
  const dialog = await screen.findByRole("dialog", { name: "Restore a snapshot" })
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe(SPA_NEW)
})
