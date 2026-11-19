import { screen, within } from "@testing-library/react"
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
  tile.getByRole("group", { name: `Upgradable: ${updatesFixture.upgradable.length}` })
  tile.getByRole("button", { name: "Repair packages" })
  tile.getByText("https://bad.example stable")
})

test("UX-7: no updates shows Up to date", async () => {
  stubFetch({ [R("updates")]: { body: { ...updatesFixture, upgradable: [], removals: [], heldBack: [] } }, [R("reboot")]: { body: { required: false, packages: [], kernel: null, services: [] } }, [R("autoupdates")]: { body: { installed: false, enabled: false } }, [R("docker")]: { body: { state: "absent" } } })
  renderWithProviders(<OverviewTab host={makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} />)
  expect(await screen.findByText("Up to date")).toBeTruthy()
  screen.getByRole("button", { name: "Install unattended-upgrades" })
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
  screen.getByText("Host offline")
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
