import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { bansDefault, bansWorst, fail2banCrowdsec, fail2banInstalled, fail2banNotInstalled } from "@/modules/m11-fixtures"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { Fail2banPage } from "./fail2ban-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const STATUS = `${BASE}/fail2ban.status.get/v1/read`
const BANS = `${BASE}/fail2ban.ban.list/v1/read`

function render(status: unknown, hostScope: Scope = "admin", extra: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [STATUS]: { body: { output: status } },
    [BANS]: { body: { output: { bans: bansDefault } } },
    "GET /api/v1/hosts/h1/modules/fail2ban/availability": { body: { module: "fail2ban", available: true, reason: null, actions: [] } },
    ...extra,
  })
  renderWithProviders(<Fail2banPage hostId="h1" />, { hostScope })
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("states", () => {
  test("not installed shows Install fail2ban, which plans the install", async () => {
    const calls = render(fail2banNotInstalled, "admin", { [`${BASE}/fail2ban.package.install/v1/plan`]: { body: makePlan("fail2ban.package.install", { hostId: "h1" }) } })
    expect(await screen.findByText("fail2ban is not installed")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Install fail2ban" }))
    expect(await screen.findByRole("dialog", { name: "Install fail2ban" })).toBeTruthy()
    await screen.findByRole("button", { name: "Apply" })
    expect(calls.find((c) => c.path.endsWith("fail2ban.package.install/v1/plan"))?.body).toEqual({ input: {} })
  })

  test("CrowdSec present shows its notice and no install button", async () => {
    render({ ...fail2banNotInstalled, crowdsec: true })
    expect(await screen.findByText("CrowdSec runs on this host")).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Install fail2ban" })).toBeNull()
    expect(screen.getByText("fail2ban is not installed")).toBeTruthy()
  })

  test("installed shows jails with banned and failed counts and the banned table", async () => {
    render(fail2banInstalled)
    expect(await screen.findByText("3 banned now · 2 failing now · 418 failed logins in total")).toBeTruthy()
    const table = await screen.findByRole("table", { name: "Banned addresses" })
    expect(within(table).getAllByRole("row")).toHaveLength(bansDefault.length + 1)
    expect(within(table).getByText("2001:db8:85a3::8a2e:370:7334")).toBeTruthy()
  })

  test("CrowdSec beside an installed fail2ban shows the notice and the jails", async () => {
    render(fail2banCrowdsec)
    expect(await screen.findByText("CrowdSec runs on this host")).toBeTruthy()
    expect(screen.getByRole("heading", { name: "Jails" })).toBeTruthy()
  })

  test("an operator sees Unban and no settings form; Unban asks no typed phrase", async () => {
    const plans: unknown[] = []
    render(fail2banInstalled, "operate", {
      [`${BASE}/fail2ban.ban.remove/v1/plan`]: (call) => {
        plans.push(call.body)
        return { body: makePlan("fail2ban.ban.remove", { hostId: "h1" }) }
      },
      [`${BASE}/fail2ban.ban.remove/v1/apply`]: { body: makeChange("fail2ban.ban.remove", null, { class: "compensable" }) },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Unban 192.0.2.44 from sshd" }))
    expect(screen.queryByRole("heading", { name: "Settings" })).toBeNull()
    const apply = await screen.findByRole("button", { name: "Apply" })
    expect(screen.queryByLabelText(/to confirm$/)).toBeNull()
    expect(plans).toEqual([{ input: { jail: "sshd", address: "192.0.2.44" } }])
    await user.click(apply)
    await screen.findByTestId("change-done")
  })

  test("a viewer sees the bans without Unban", async () => {
    render(fail2banInstalled, "read")
    await screen.findByRole("table", { name: "Banned addresses" })
    expect(screen.queryByRole("button", { name: /^Unban/ })).toBeNull()
  })

  test("fixed ignore entries render as read-only chips; the host's own can be removed", async () => {
    render(fail2banInstalled)
    const chips = within(await screen.findByRole("list", { name: "Never banned" }))
    for (const ip of fail2banInstalled.ignoreFixed) {
      const chip = chips.getByText(ip)
      expect(within(chip).queryByRole("button")).toBeNull()
    }
    expect(chips.getByRole("button", { name: "Remove 198.51.100.0/24" })).toBeTruthy()
  })

  test("300.1.1.1 in the ignore field is rejected inline; IPv4, IPv6 and CIDR are accepted", async () => {
    const calls = render(fail2banInstalled, "admin", { [`${BASE}/fail2ban.settings.apply/v1/plan`]: { body: makePlan("fail2ban.settings.apply", { hostId: "h1" }) } })
    const user = userEvent.setup()
    const field = await screen.findByLabelText("Add an address or network")
    await user.type(field, "300.1.1.1{Enter}")
    expect(screen.getByText("Enter an IPv4 or IPv6 address or network")).toBeTruthy()
    expect(field.getAttribute("aria-invalid")).toBe("true")
    for (const bad of ["10.0.0.0/33", "2001:db8::/129", "fe80::1::2", "example.com"]) {
      await user.clear(field)
      await user.type(field, `${bad}{Enter}`)
      expect(screen.getByText("Enter an IPv4 or IPv6 address or network")).toBeTruthy()
    }
    for (const good of ["203.0.113.7", "10.0.0.0/8", "2001:db8::/32"]) {
      await user.clear(field)
      await user.type(field, good)
      await user.click(screen.getByRole("button", { name: "Add" }))
      expect(screen.queryByText("Enter an IPv4 or IPv6 address or network")).toBeNull()
    }
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    const body = calls.find((c) => c.path.endsWith("fail2ban.settings.apply/v1/plan"))?.body as { input: { ignoreIp: string[] } }
    expect(body.input).toEqual({
      bantimeMinutes: 10,
      findtimeMinutes: 10,
      maxRetry: 5,
      recidive: true,
      ignoreIp: [...fail2banInstalled.ignoreFixed, "198.51.100.0/24", "203.0.113.7", "10.0.0.0/8", "2001:db8::/32"],
    })
  })

  test("an address typed but not added goes with the change; a bad one stops it, and a range error shows under its field", async () => {
    const calls = render(fail2banInstalled, "admin", { [`${BASE}/fail2ban.settings.apply/v1/plan`]: { body: makePlan("fail2ban.settings.apply", { hostId: "h1" }) } })
    const user = userEvent.setup()
    const field = await screen.findByLabelText("Add an address or network")
    await user.type(field, "example.com")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(screen.getByText("Enter an IPv4 or IPv6 address or network")).toBeTruthy()
    const retries = screen.getByLabelText("Failures before a ban")
    await user.clear(retries)
    await user.type(retries, "99")
    await user.clear(field)
    await user.type(field, "192.0.2.9")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(retries.getAttribute("aria-invalid")).toBe("true")
    expect(document.getElementById(retries.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Failures before a ban: enter a whole number from 1 to 20")
    await user.clear(retries)
    await user.type(retries, "5")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    const body = calls.find((c) => c.path.endsWith("fail2ban.settings.apply/v1/plan"))?.body as { input: { ignoreIp: string[] } }
    expect(body.input.ignoreIp).toEqual([...fail2banInstalled.ignoreFixed, "198.51.100.0/24", "192.0.2.9"])
  })

  test("loading, error with Retry, and 500 bans", async () => {
    let n = 0
    render(fail2banInstalled, "admin", {
      [STATUS]: () => (++n === 1 ? { status: 500, body: { code: "internal", detail: "boom", status: 500 } } : { body: { output: fail2banInstalled } }),
      [BANS]: { body: { output: { bans: bansWorst } } },
    })
    expect(screen.getByRole("status", { name: "Loading intrusion prevention" })).toBeTruthy()
    expect(await screen.findByText("Couldn't load intrusion prevention")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
    const table = await screen.findByRole("table", { name: "Banned addresses" })
    expect(within(table).getAllByRole("row")).toHaveLength(501)
    expect(within(table).getAllByText("Until unbanned").length).toBeGreaterThan(0)
  }, 20_000)
})
