import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, test, vi } from "vitest"
import { bansDefault, bansWorst, fail2banCrowdsec, fail2banInstalled, fail2banNotInstalled } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { Fail2banPage } from "./fail2ban-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const THEMES = ["light", "dark"] as const
const ok = (output: unknown): StubHandler => ({ body: { output } })

const STATES: [string, StubHandler, StubHandler, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), ok({ bans: [] }), async () => {
    await screen.findByRole("status", { name: "Loading intrusion prevention" })
  }],
  ["error", { status: 500, body: { code: "internal", detail: "boom", status: 500 } }, ok({ bans: [] }), async () => {
    await screen.findByText("Couldn't load intrusion prevention")
  }],
  ["not installed", ok(fail2banNotInstalled), ok({ bans: [] }), async () => {
    await screen.findByRole("button", { name: "Install fail2ban" })
  }],
  ["CrowdSec present", ok(fail2banCrowdsec), ok({ bans: bansDefault }), async () => {
    await screen.findByText("CrowdSec runs on this host")
    await screen.findByRole("table", { name: "Banned addresses" })
  }],
  ["installed with an ignore error", ok(fail2banInstalled), ok({ bans: bansDefault }), async () => {
    await screen.findByRole("table", { name: "Banned addresses" })
    await userEvent.setup().type(screen.getByLabelText("Add an address or network"), "300.1.1.1{Enter}")
    await screen.findByText("Enter an IPv4 or IPv6 address or network")
  }],
  ["worst case", ok(fail2banInstalled), ok({ bans: bansWorst.slice(0, 40) }), async () => {
    await screen.findByRole("table", { name: "Banned addresses" })
  }],
  ["unban dialog", ok(fail2banInstalled), ok({ bans: bansDefault }), async () => {
    await userEvent.setup().click(await screen.findByRole("button", { name: "Unban 192.0.2.44 from sshd" }))
    await screen.findByRole("button", { name: "Apply" })
  }],
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, status, bans, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({
        [`${BASE}/fail2ban.status.get/v1/read`]: status,
        [`${BASE}/fail2ban.ban.list/v1/read`]: bans,
        [`${BASE}/fail2ban.ban.remove/v1/plan`]: { body: makePlan("fail2ban.ban.remove", { hostId: "h1" }) },
        "GET /api/v1/hosts/h1/modules/fail2ban/availability": { body: { module: "fail2ban", available: true, reason: null, actions: [] } },
      })
      const { container } = renderWithProviders(<Fail2banPage hostId="h1" />, { hostScope: "admin" })
      await reach()
      await expectNoAxeViolations(container)
      for (const dialog of document.querySelectorAll("[role=dialog]")) await expectNoAxeViolations(dialog)
    }, 20_000)
  }
})
