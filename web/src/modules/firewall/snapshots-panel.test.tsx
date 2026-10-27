import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwSnapshot } from "./fixtures"
import { SnapshotsPanel } from "./snapshots-panel"

const SNAPSHOTS = "GET /api/v1/hosts/h1/resources/firewall/snapshots"
const SP_PLAN = "POST /api/v1/hosts/h1/actions/firewall.snapshot.restore/v1/plan"

const spList = {
  snapshots: [
    fwSnapshot({ id: "s-base", createdAt: "2026-10-20T08:00:00Z", changeId: "c0", baseline: true, pinned: true }),
    fwSnapshot({ id: "s-new", createdAt: "2026-10-24T08:00:00Z", changeId: "c2" }),
    fwSnapshot({ id: "s-sys", createdAt: "2026-10-22T08:00:00Z", changeId: undefined, actor: "system:firewall.reconcile", system: true }),
  ],
}

function spRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [SNAPSHOTS]: { body: spList },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    [SP_PLAN]: {
      body: { planId: "p1", hostId: "h1", action: "firewall.snapshot.restore", version: 1, inputHash: "h", diff: [{ op: "-", text: "allow in tcp to any port 8080 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
    ...routes,
  })
  renderWithProviders(<SnapshotsPanel hostId="h1" />)
  return calls
}

test("lists the snapshots newest first with the change each was taken before", async () => {
  spRender()
  const table = await screen.findByRole("table", { name: "Firewall snapshots" })
  const rows = within(table).getAllByRole("row").slice(1).map((r) => within(r).getAllByRole("cell").map((c) => c.textContent))
  expect(rows).toEqual([
    [formatTime("2026-10-24T08:00:00Z"), "c2", "user:u1", "", "Restore"],
    [formatTime("2026-10-22T08:00:00Z"), "—", "system:firewall.reconcile", "system", "Restore"],
    [formatTime("2026-10-20T08:00:00Z"), "c0", "user:u1", "baselinepinned", "Restore"],
  ])
  expect(within(table).getByRole("link", { name: "c2" }).getAttribute("href")).toBe("/changes/c2")
})

test("a restore warns about manual rules and plans without manual by default", async () => {
  const calls = spRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Restore the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}` }))
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  expect(within(ask).getByText(/Rules added by hand since then are removed/).textContent).toContain("lists every manual rule")
  await user.click(within(ask).getByRole("button", { name: "Plan the restore" }))
  const dialog = await screen.findByRole("dialog", { name: "Restore a snapshot" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("- allow in tcp to any port 8080 (manual)")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "s-new" } })
})

test("a restore allowed to change manual rules sends manual: true", async () => {
  const calls = spRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Restore the snapshot taken ${formatTime("2026-10-20T08:00:00Z")}` }))
  const ask = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  await user.click(within(ask).getByRole("checkbox", { name: "Allow the restore to add or remove manual rules" }))
  await user.click(within(ask).getByRole("button", { name: "Plan the restore" }))
  await within(await screen.findByRole("dialog", { name: "Restore a snapshot" })).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "s-base", manual: true } })
})

test("cancelling a restore clears the manual-rules consent", async () => {
  const calls = spRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Restore the snapshot taken ${formatTime("2026-10-20T08:00:00Z")}` }))
  const first = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  await user.click(within(first).getByRole("checkbox", { name: "Allow the restore to add or remove manual rules" }))
  await user.click(within(first).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Restore this snapshot?" })).toBeNull())
  await user.click(screen.getByRole("button", { name: `Restore the snapshot taken ${formatTime("2026-10-24T08:00:00Z")}` }))
  const again = await screen.findByRole("dialog", { name: "Restore this snapshot?" })
  expect(within(again).getByRole("checkbox", { name: "Allow the restore to add or remove manual rules" }).getAttribute("aria-checked")).toBe("false")
  await user.click(within(again).getByRole("button", { name: "Plan the restore" }))
  await within(await screen.findByRole("dialog", { name: "Restore a snapshot" })).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "s-new" } })
})

test("no snapshots is an empty state", async () => {
  spRender({ [SNAPSHOTS]: { body: { snapshots: [] } } })
  expect((await screen.findByText("No snapshots yet")).textContent).toBe("No snapshots yet")
})

test("a failed read shows the server's reason", async () => {
  spRender({ [SNAPSHOTS]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})

test("the snapshots table has no accessibility violations", async () => {
  spRender()
  expect(await axe(await screen.findByRole("table", { name: "Firewall snapshots" }))).toHaveNoViolations()
})
