import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useQueryClient } from "@tanstack/react-query"
import { describe, expect, test } from "vitest"
import { cn } from "darkraise-ui/lib"
import type { TailnetDevice } from "@/api/types"
import { formatTime } from "@/lib/format"
import { STACKED_CELL, STACKED_ROW } from "@/lib/table"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { TAILNET_DEVICES_QUERY_KEY } from "@/lib/tailnet-devices"
import { TailnetDevicesPage } from "./tailnet-devices"

const LIST = "POST /api/v1/actions/core.tailnetdevice.list/v1/read"
const PLAN = "POST /api/v1/actions/core.tailnetdevice.forget/v1/plan"
const APPLY = "POST /api/v1/actions/core.tailnetdevice.forget/v1/apply"

function device(stableId: string, nodeName: string, day = 1): TailnetDevice {
  const d = String(day).padStart(2, "0")
  return { stableId, nodeName, rememberedAt: `2026-10-${d}T09:00:00Z`, lastUsedAt: `2026-11-${d}T09:30:00Z` }
}

const laptop = device("nL", "laptop.tail1234.ts.net", 2)
const unnamed = device("nE", "", 1)
const listed = (devices: TailnetDevice[]): StubResponse => ({ body: { output: { devices } } })
const problem = (status: number, code: string): StubResponse => ({ status, body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code } })

// The kit merges a cell's classes, which drops the earlier of two that
// conflict, so the stacked classes are compared in their merged form.
const hasClasses = (el: Element, classes: string) => cn(classes).split(" ").every((c) => el.classList.contains(c))
const rowOf = (text: string) => screen.getByText(text).closest("tr") as HTMLElement

describe("the Tailscale devices page", () => {
  test("A20 UI-2 UX-8 lists the caller's devices", async () => {
    const calls = stubFetch({ [LIST]: listed([laptop, unnamed]) })
    renderWithProviders(<TailnetDevicesPage />)
    await screen.findByText("laptop")
    expect(screen.getByRole("heading", { level: 1, name: "Tailscale devices" })).toBeTruthy()
    expect(screen.getByText("Devices that may sign you in with Continue as without a code.")).toBeTruthy()
    const row = rowOf("laptop")
    expect(within(row).getByText("laptop").className).toContain("font-medium")
    expect(within(row).getByText("laptop.tail1234.ts.net")).toBeTruthy()
    expect(within(row).getByText("nL").className).toContain("font-mono")
    expect(within(row).getByText(formatTime(laptop.rememberedAt))).toBeTruthy()
    expect(within(row).getByText(formatTime(laptop.lastUsedAt))).toBeTruthy()
    expect(within(row).getByRole("button", { name: "Forget device laptop" })).toBeTruthy()
    const empty = rowOf("Unnamed device")
    expect(within(empty).getByText("nE")).toBeTruthy()
    expect(within(empty).getByRole("button", { name: "Forget device nE" })).toBeTruthy()
    expect(within(empty).getAllByText(/./, { selector: "div > span" }).map((s) => s.textContent)).toEqual(["Unnamed device", "nE"])
    expect(calls.find((c) => c.path.endsWith("core.tailnetdevice.list/v1/read"))?.body).toEqual({ input: {} })
  })

  test("UX-5 the first load shows the spinner", async () => {
    stubFetch({ [LIST]: () => new Promise<StubResponse>(() => undefined) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByLabelText("Loading the devices")).toBeTruthy()
  })

  test("UX-5 a refresh keeps the old rows", async () => {
    let first = true
    stubFetch({
      [LIST]: () => {
        if (first) {
          first = false
          return listed([laptop])
        }
        return new Promise<StubResponse>(() => undefined)
      },
    })
    let refresh = () => undefined as unknown
    function Refresher() {
      const client = useQueryClient()
      refresh = () => client.invalidateQueries({ queryKey: TAILNET_DEVICES_QUERY_KEY })
      return null
    }
    renderWithProviders(
      <>
        <TailnetDevicesPage />
        <Refresher />
      </>,
    )
    await screen.findByText("laptop")
    void refresh()
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.getByText("laptop")).toBeTruthy()
    expect(screen.queryByLabelText("Loading the devices")).toBeNull()
  })

  test("UX-5 an empty list says how a device is remembered", async () => {
    stubFetch({ [LIST]: listed([]) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByText("No remembered devices")).toBeTruthy()
    expect(screen.getByText("A device is remembered when you continue as yourself from it with your code.")).toBeTruthy()
    expect(screen.getByRole("heading", { level: 2, name: "Device list" })).toBeTruthy()
  })

  test("UX-5 an error shows the destructive alert with its message", async () => {
    stubFetch({ [LIST]: problem(500, "internal") })
    renderWithProviders(<TailnetDevicesPage />)
    expect((await screen.findByRole("alert")).textContent).toBe("server detail for internal")
  })

  test("UX-5 the worst case stacks 40 rows and wraps a 253-character name", async () => {
    const long = `${"a".repeat(63)}.${"b".repeat(63)}.${"c".repeat(63)}.${"d".repeat(54)}.ts.net`
    expect(long).toHaveLength(253)
    const devices = [device("n1", long), device("n2", ""), ...Array.from({ length: 38 }, (_, i) => device(`n${i + 3}`, `host-${i + 3}.tail1234.ts.net`, 1 + (i % 28)))]
    stubFetch({ [LIST]: listed(devices) })
    renderWithProviders(<TailnetDevicesPage />)
    await screen.findByText(long)
    const rows = screen.getAllByRole("row").slice(1)
    expect(rows).toHaveLength(40)
    for (const row of rows) {
      expect(hasClasses(row, STACKED_ROW)).toBe(true)
      for (const cell of within(row).getAllByRole("cell")) expect(hasClasses(cell, STACKED_CELL)).toBe(true)
    }
    const labels = within(rows[0] as HTMLElement).getAllByRole("cell").map((c) => c.getAttribute("data-label"))
    expect(labels).toEqual(["Device", "Remembered", "Last used", null])
    expect(screen.getByText(long).closest("td")?.className).toContain("wrap-anywhere")
  })
})

test("UX-7 Forget completes by keyboard alone", async () => {
  let current = [laptop, device("n3", "gamma.ts.net")]
  stubFetch({
    [LIST]: () => listed(current),
    [PLAN]: { body: makePlan("core.tailnetdevice.forget", { planId: "p9", confirmation: "nL" }) },
    [APPLY]: () => {
      current = current.filter((d) => d.stableId !== "nL")
      return { body: makeChange("core.tailnetdevice.forget", null) }
    },
  })
  renderWithProviders(<TailnetDevicesPage />)
  const user = userEvent.setup()
  const tabTo = async (target: HTMLElement) => {
    for (let i = 0; i < 40 && document.activeElement !== target; i++) await user.tab()
    expect(document.activeElement).toBe(target)
  }
  await tabTo(await screen.findByRole("button", { name: "Forget device laptop" }))
  await user.keyboard("{Enter}")
  const dialog = await screen.findByRole("dialog", { name: "Forget device laptop" })
  await tabTo(await within(dialog).findByLabelText("Type nL to confirm"))
  await user.keyboard("nL")
  await tabTo(within(dialog).getByRole("button", { name: "Apply" }))
  await user.keyboard("{Enter}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(screen.queryByText("nL")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Forget device gamma" })))
})

describe("UX-4 forget moves focus", () => {
  const three = [device("n1", "alpha.ts.net"), device("nL", "laptop.ts.net"), device("n3", "gamma.ts.net")]

  test.each([
    ["row 2 of 3", three, "nL", "Forget device gamma"],
    ["the last row", three, "n3", "Forget device laptop"],
    ["the only row", [device("nL", "laptop.ts.net")], "nL", null],
  ])("A21 forgetting %s", async (_, devices, gone, focus) => {
    let current = devices
    const calls = stubFetch({
      [LIST]: () => listed(current),
      [PLAN]: { body: makePlan("core.tailnetdevice.forget", { planId: "p9", confirmation: gone, diff: [{ op: "-", text: `remembered device ${gone}` }] }) },
      [APPLY]: () => {
        current = current.filter((d) => d.stableId !== gone)
        return { body: makeChange("core.tailnetdevice.forget", null) }
      },
    })
    renderWithProviders(<TailnetDevicesPage />)
    const user = userEvent.setup()
    const name = devices.find((d) => d.stableId === gone)!.nodeName.split(".")[0]
    await user.click(await screen.findByRole("button", { name: `Forget device ${name}` }))
    const dialog = await screen.findByRole("dialog", { name: `Forget device ${name}` })
    await user.type(await within(dialog).findByLabelText(`Type ${gone} to confirm`), gone)
    await user.click(within(dialog).getByRole("button", { name: "Apply" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(screen.queryByText(gone)).toBeNull())
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { stableId: gone } })
    expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p9", confirmation: gone })
    if (focus === null) {
      const h1 = screen.getByRole("heading", { level: 1, name: "Tailscale devices" })
      await waitFor(() => expect(document.activeElement).toBe(h1))
      expect(h1.getAttribute("tabindex")).toBe("-1")
    } else {
      await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: focus })))
    }
  })
})
