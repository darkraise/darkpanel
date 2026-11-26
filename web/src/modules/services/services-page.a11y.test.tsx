import { act, fireEvent, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, test, vi } from "vitest"
import { journalEntries, unitsDefault, unitsWorst } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { EvtSource } from "@/test/evt-source"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ServicesPage } from "./services-page"

const LIST = "POST /api/v1/hosts/h1/actions/services.unit.list/v1/read"
const THEMES = ["light", "dark"] as const

function journal(): EvtSource | undefined {
  return EvtSource.instances.filter((s) => s.url.includes("journal-follow")).slice(-1)[0]
}

const STATES: [string, StubHandler, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), async () => {
    await screen.findByRole("status", { name: "Loading the services" })
  }],
  ["error", { status: 500, body: { code: "internal", detail: "boom", status: 500 } }, async () => {
    await screen.findByText("Couldn't load services")
  }],
  ["default, following", { body: { output: { units: unitsDefault } } }, async () => {
    await screen.findByText("nginx.service")
    act(() => {
      for (const e of journalEntries(5)) journal()?.emit("data", e)
    })
  }],
  ["filtered empty", { body: { output: { units: unitsDefault } } }, async () => {
    await screen.findByText("nginx.service")
    fireEvent.change(screen.getByLabelText("Search units"), { target: { value: "zzz" } })
    await screen.findByText("No units match 'zzz'")
  }],
  ["worst case, reconnecting", { body: { output: { units: unitsWorst.slice(0, 40) } } }, async () => {
    await screen.findByText(unitsWorst[0]?.name ?? "")
    act(() => journal()?.fail())
    await screen.findByText("Reconnecting…")
  }],
]

beforeEach(() => {
  EvtSource.instances = []
})

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, list, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({ [LIST]: list })
      const { container } = renderWithProviders(<ServicesPage hostId="h1" />, { hostScope: "admin" })
      await reach()
      await expectNoAxeViolations(container)
    }, 20_000)
  }
})
