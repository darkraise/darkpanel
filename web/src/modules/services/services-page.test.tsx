import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { unitsDefault, unitsWorst } from "@/modules/m11-fixtures"
import { makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ServicesPage } from "./services-page"

const LIST = "POST /api/v1/hosts/h1/actions/services.unit.list/v1/read"
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/services/availability"

function render(list: StubHandler, hostScope: Scope = "admin", extra: Record<string, StubHandler> = {}) {
  stubFetch({ [LIST]: list, [AVAILABILITY]: { body: { module: "services", available: true, reason: null, actions: [] } }, ...extra })
  return renderWithProviders(<ServicesPage hostId="h1" />, { hostScope })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("state matrix", () => {
  test("loading shows a skeleton", () => {
    render(() => new Promise(() => {}))
    expect(screen.getByRole("status", { name: "Loading the services" })).toBeTruthy()
  })

  test("error shows Couldn't load services and Retry", async () => {
    let calls = 0
    render(() => (++calls === 1 ? { status: 500, body: { code: "internal", detail: "boom", status: 500 } } : { body: { output: { units: unitsDefault } } }))
    expect(await screen.findByText("Couldn't load services")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByText("nginx.service")).toBeTruthy()
  })

  test("a filter with no match shows No units match and Clear filter", async () => {
    render({ body: { output: { units: unitsDefault } } })
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.type(screen.getByLabelText("Search units"), "zzz")
    expect(screen.getByText("No units match 'zzz'")).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Clear filter" }))
    expect(screen.getByText("nginx.service")).toBeTruthy()
  })

  test("the state filter shows the failed units", async () => {
    render({ body: { output: { units: unitsDefault } } })
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.selectOptions(screen.getByLabelText("State"), "failed")
    const rows = screen.getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["fail2ban.service", "postgresql@16-main.service"])
    expect(within(rows[0] as HTMLElement).getByText("Failed")).toBeTruthy()
  })

  test("400 units render and a 120-character name is truncated with its full name in a tooltip", async () => {
    render({ body: { output: { units: unitsWorst } } })
    const name = unitsWorst[0]?.name ?? ""
    expect(name).toHaveLength(120)
    const cell = await screen.findByText(name)
    expect(screen.getAllByRole("row")).toHaveLength(401)
    expect(cell.className).toContain("truncate")
    expect(cell.getAttribute("title")).toBe(name)
  }, 20_000)
})

describe("menus by unit and role", () => {
  test("ssh.service Stop opens ChangeDialog asking the typed phrase ssh.service", async () => {
    const plans: unknown[] = []
    render({ body: { output: { units: unitsDefault } } }, "admin", {
      "POST /api/v1/hosts/h1/actions/services.unit.stop/v1/plan": (call) => {
        plans.push(call.body)
        return { body: makePlan("services.unit.stop", { hostId: "h1", confirmation: "ssh.service" }) }
      },
    })
    const user = userEvent.setup()
    await screen.findByText("ssh.service")
    await user.click(screen.getByRole("button", { name: "Actions for ssh.service" }))
    await user.click(await screen.findByRole("menuitem", { name: "Stop" }))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("Stop ssh.service")).toBeTruthy()
    expect(await within(dialog).findByLabelText("Type ssh.service to confirm")).toBeTruthy()
    expect(plans).toEqual([{ input: { unit: "ssh.service" } }])
  })

  test("darkpanel-agent.service shows its menu items disabled with the reason", async () => {
    render({ body: { output: { units: unitsDefault } } })
    const user = userEvent.setup()
    await screen.findByText("darkpanel-agent.service")
    await user.click(screen.getByRole("button", { name: "Actions for darkpanel-agent.service" }))
    const items = await screen.findAllByRole("menuitem")
    const ops = items.filter((i) => i.textContent.startsWith("Start") || i.textContent.startsWith("Stop") || i.textContent.startsWith("Restart"))
    expect(ops).toHaveLength(3)
    for (const item of ops) {
      expect(item.getAttribute("aria-disabled") ?? item.getAttribute("data-disabled")).not.toBeNull()
      expect(within(item).getByText("Darkpanel manages its own units")).toBeTruthy()
    }
  })

  test("a viewer sees no menu", async () => {
    render({ body: { output: { units: unitsDefault } } }, "read")
    await screen.findByText("nginx.service")
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull()
  })

  test("an operator sees Start, Stop, Restart, Reload and not Enable", async () => {
    render({ body: { output: { units: unitsDefault } } }, "operate")
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.click(screen.getByRole("button", { name: "Actions for nginx.service" }))
    const items = (await screen.findAllByRole("menuitem")).map((i) => i.textContent)
    expect(items).toEqual(["Start", "Stop", "Restart", "Reload", "View logs"])
  })

  test("View logs opens the journal filtered to the unit, with focus on its unit field", async () => {
    render({ body: { output: { units: unitsDefault } } })
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.click(screen.getByRole("button", { name: "Actions for nginx.service" }))
    await user.click(await screen.findByRole("menuitem", { name: "View logs" }))
    await waitFor(() => expect((screen.getByLabelText("Unit") as HTMLInputElement).value).toBe("nginx.service"))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Unit")))
    // The closing menu does not take focus back after its exit animation.
    await new Promise((r) => setTimeout(r, 300))
    expect(document.activeElement).toBe(screen.getByLabelText("Unit"))
  })
})
