import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { OverviewTab } from "./overview-tab"
import { RebootTile } from "./reboot-tile"
import { updatesFixture, rebootFixture } from "./fixtures"

const R = (name: string) => `GET /api/v1/hosts/h1/resources/system/${name}`

test("UX-7: the updates tile shows counts, the repair alert and failed sources", async () => {
  stubFetch({ [R("updates")]: { body: { ...updatesFixture, dpkgInterrupted: true, failedSources: ["https://bad.example stable"] } }, [R("reboot")]: { body: rebootFixture }, [R("autoupdates")]: { body: { installed: true, enabled: false } }, [R("docker")]: { body: { state: "absent" } } })
  renderWithProviders(<OverviewTab host={makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} />)
  const tile = within(await screen.findByRole("region", { name: "Updates" }))
  tile.getByText(String(updatesFixture.upgradable.length))
  tile.getByText("pending")
  tile.getByText(String(updatesFixture.upgradable.filter((u) => u.security).length))
  tile.getByText("security")
  tile.getByRole("button", { name: "Repair packages" })
  tile.getByText("https://bad.example stable")
})

test("UX-7: no updates shows Up to date", async () => {
  stubFetch({ [R("updates")]: { body: { ...updatesFixture, upgradable: [], removals: [], heldBack: [] } }, [R("reboot")]: { body: { required: false, packages: [], kernel: null, services: [] } }, [R("autoupdates")]: { body: { installed: false, enabled: false } }, [R("docker")]: { body: { state: "absent" } } })
  renderWithProviders(<OverviewTab host={makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} />)
  const tile = within(await screen.findByRole("region", { name: "Updates" }))
  await tile.findByText("Up to date")
  tile.getByRole("img", { name: "Up to date" })
  expect(tile.queryByRole("button", { name: "Upgrade all" })).toBeNull()
  screen.getByRole("button", { name: "Install unattended-upgrades" })
})

const ON = makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })
const failedChange = (action: string, code: string, message: string) => ({
  id: "c9", hostId: "h1", action, version: 1, class: "agent", state: "failed", actor: "admin", createdAt: "2026-11-19T11:00:00Z", updatedAt: "2026-11-19T11:01:00Z",
  revertDeadline: null, output: null, error: { code, message }, compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
})
const reads = { [R("updates")]: { body: updatesFixture }, [R("reboot")]: { body: rebootFixture }, [R("autoupdates")]: { body: { installed: true, enabled: true } }, [R("docker")]: { body: { state: "absent" } } }

test("UX-7: apt busy marks the updates tile Busy, and Retry on an upgrade goes back to the Updates tab", async () => {
  stubFetch({ ...reads, "GET /api/v1/changes": { body: { changes: [failedChange("system.packages.upgrade", "system.apt_busy", "another package manager holds the lock")] } } })
  renderWithProviders(<OverviewTab host={ON} />)
  const tile = within(await screen.findByRole("region", { name: "Updates" }))
  await tile.findByText("Busy")
  tile.getByText("Another package manager is running")
  expect(tile.getByRole("link", { name: "Retry" }).getAttribute("href")).toBe("/hosts/h1/system/updates")
  expect(screen.queryByRole("region", { name: "Last change" })).toBeNull()
})

test("UX-7: a failed change gets its own Last change tile with the message, the package, apt's lines in a tip and Repair packages for broken dependencies", async () => {
  stubFetch({ ...reads, "GET /api/v1/changes": { body: { changes: [failedChange("system.packages.repair", "system.broken_packages", "apt failed on nginx-common:\ndpkg: error processing package nginx-common (--configure)")] } } })
  renderWithProviders(<OverviewTab host={ON} />)
  const tile = within(await screen.findByRole("region", { name: "Last change" }))
  tile.getByText("Failed")
  tile.getByText("Packages have broken dependencies")
  tile.getByText("nginx-common")
  tile.getByRole("button", { name: "Repair packages" })
  expect(tile.queryByRole("button", { name: "Retry" })).toBeNull()
  await userEvent.click(tile.getByRole("button", { name: "apt output" }))
  expect((await screen.findAllByText(/dpkg: error processing package/)).length).toBeGreaterThan(0)
})

test("UX-7: while the reads load, Refresh and Reboot wait for them", async () => {
  stubFetch({ [R("updates")]: () => new Promise(() => undefined), [R("reboot")]: () => new Promise(() => undefined), [R("autoupdates")]: { body: { installed: true, enabled: true } }, [R("docker")]: { body: { state: "absent" } } })
  renderWithProviders(<OverviewTab host={ON} />)
  expect((await screen.findByRole("button", { name: "Refresh" })).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Reboot" }).hasAttribute("disabled")).toBe(true)
})

test("UX-7: one failed read shows its error with a retry while the other tiles stay usable", async () => {
  stubFetch({
    ...reads,
    [R("updates")]: { status: 502, body: { type: "about:blank", title: "Bad gateway", status: 502, detail: "The agent did not answer within 10 seconds.", code: "agent.unreachable" } },
  })
  renderWithProviders(<OverviewTab host={ON} />)
  const updates = within(await screen.findByRole("region", { name: "Updates" }))
  await updates.findByText("The agent did not answer within 10 seconds.")
  updates.getByRole("button", { name: "Retry" })
  expect(within(screen.getByRole("region", { name: "Automatic updates" })).getByRole("switch").hasAttribute("disabled")).toBe(false)
  expect(within(screen.getByRole("region", { name: "Reboot" })).getByRole("button", { name: "Reboot" }).hasAttribute("disabled")).toBe(false)
})

test("A50: an agent without the system module shows Agent update needed", () => {
  stubFetch({})
  renderWithProviders(<OverviewTab host={makeHost({ id: "h1", modules: [] })} />)
  screen.getByText("Agent update needed")
  screen.getByRole("button", { name: "Update agent" })
})

test("UX-7: an offline host greys the tiles and disables actions with the reason", () => {
  stubFetch({})
  renderWithProviders(<OverviewTab host={makeHost({ id: "h1", online: false, modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} />)
  expect(screen.getAllByText("Host offline")).toHaveLength(4)
  expect(screen.getAllByText(/^Last seen /)).toHaveLength(4)
  for (const b of screen.getAllByRole("button")) expect(b.getAttribute("aria-disabled") ?? String(b.hasAttribute("disabled"))).toMatch(/true/)
})

test("UX-4: reboot states derive from the change output and boot time, so a reload keeps them", () => {
  const change = { id: "c1", action: "system.host.reboot", version: 1, state: "completed", output: { scheduledAt: "2026-11-19T12:00:15Z", bootTimeBefore: "2026-11-01T00:00:00Z" } }
  const cases = [
    { host: makeHost({ id: "h1", online: false }), boot: "2026-11-01T00:00:00Z", now: "2026-11-19T12:01:00Z", text: "Rebooting" },
    { host: makeHost({ id: "h1", online: true }), boot: "2026-11-01T00:00:00Z", now: "2026-11-19T12:02:16Z", text: "Reboot did not start" },
    { host: makeHost({ id: "h1", online: false }), boot: "2026-11-01T00:00:00Z", now: "2026-11-19T12:10:16Z", text: "Not back after 10 minutes" },
    { host: makeHost({ id: "h1", online: true }), boot: "2026-11-19T12:01:30Z", now: "2026-11-19T12:03:00Z", text: "Back online" },
  ]
  for (const c of cases) {
    const { unmount } = renderWithProviders(<RebootTile host={c.host} bootTime={c.boot} lastReboot={change} now={new Date(c.now)} state={rebootFixture} />)
    screen.getByText(c.text)
    unmount()
  }
})
