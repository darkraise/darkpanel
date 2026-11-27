import { act, fireEvent, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import { journalEntries } from "@/modules/m11-fixtures"
import { EvtSource } from "@/test/evt-source"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { MAX_LINES, reconnectDelay } from "./journal"
import { JournalPanel } from "./journal-panel"

function streams(): EvtSource[] {
  return EvtSource.instances.filter((s) => s.url.includes("/streams/services/journal-follow"))
}

function latest(): EvtSource {
  const all = streams()
  const s = all[all.length - 1]
  if (s === undefined) throw new Error("no journal stream opened")
  return s
}

function render(unit = "nginx.service") {
  stubFetch({})
  return renderWithProviders(<JournalPanel hostId="h1" unit={unit} onUnitChange={() => {}} />)
}

beforeEach(() => {
  EvtSource.instances = []
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe("follow behaviour", () => {
  test("follows the unit's journal, loading until the stream answers", () => {
    render()
    expect(screen.getByRole("status", { name: "Connecting to the journal" })).toBeTruthy()
    expect(screen.queryByRole("log")).toBeNull()
    const url = new URL(latest().url, "http://localhost")
    expect(url.pathname).toBe("/api/v1/hosts/h1/streams/services/journal-follow")
    expect(url.searchParams.get("unit")).toBe("nginx.service")
    expect(url.searchParams.get("priority")).toBe("6")
  })

  test("scrolling up pauses following and shows Following paused with Jump to latest", async () => {
    render()
    act(() => {
      for (const e of journalEntries(20)) latest().emit("data", e)
    })
    const log = screen.getByRole("log", { name: "Journal entries" })
    expect(screen.getByText("Following")).toBeTruthy()
    expect(screen.getByText("The last 2000 lines stay in view.")).toBeTruthy()
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 1000 })
    Object.defineProperty(log, "clientHeight", { configurable: true, value: 200 })
    log.scrollTop = 100
    fireEvent.scroll(log)
    expect(screen.getByText("Following paused")).toBeTruthy()
    expect(screen.queryByText("Following")).toBeNull()
    await userEvent.setup().click(screen.getByRole("button", { name: "Jump to latest" }))
    expect(screen.queryByText("Following paused")).toBeNull()
  })

  test("a dropped stream shows Reconnecting… and reconnects with a growing delay", () => {
    vi.useFakeTimers()
    render()
    act(() => latest().emit("data", journalEntries(1)[0]))
    act(() => latest().fail())
    expect(screen.getByText("Reconnecting…")).toBeTruthy()
    expect(streams()).toHaveLength(1)
    act(() => vi.advanceTimersByTime(reconnectDelay(0)))
    expect(streams()).toHaveLength(2)
    expect(new URL(latest().url, "http://localhost").searchParams.get("since")).not.toBeNull()
    act(() => latest().fail())
    act(() => vi.advanceTimersByTime(reconnectDelay(0)))
    expect(streams()).toHaveLength(2)
    act(() => vi.advanceTimersByTime(reconnectDelay(1) - reconnectDelay(0)))
    expect(streams()).toHaveLength(3)
    expect([reconnectDelay(0), reconnectDelay(1), reconnectDelay(2), reconnectDelay(10)]).toEqual([1000, 2000, 4000, 30_000])
  })

  test("a reconnect skips the entries it already holds", () => {
    vi.useFakeTimers()
    render()
    const [a, b] = journalEntries(2)
    act(() => latest().emit("data", a))
    act(() => latest().fail())
    act(() => vi.advanceTimersByTime(reconnectDelay(0)))
    act(() => {
      latest().emit("data", a)
      latest().emit("data", b)
    })
    expect(screen.getAllByRole("listitem")).toHaveLength(2)
  })

  test("a truncated message ends with … (truncated)", () => {
    render()
    act(() => latest().emit("data", { time: 1, priority: 3, unit: "nginx.service", message: "upstream timed out", truncated: true }))
    expect(screen.getByText("upstream timed out… (truncated)")).toBeTruthy()
  })

  test("at most 2000 lines stay rendered", () => {
    render()
    act(() => {
      for (const e of journalEntries(MAX_LINES + 50)) latest().emit("data", e)
    })
    expect(screen.getAllByRole("listitem")).toHaveLength(MAX_LINES)
  }, 30_000)

  test("turning Follow off reads a page of the journal, oldest first", async () => {
    const calls = stubFetch({
      "POST /api/v1/hosts/h1/actions/services.journal.query/v1/read": {
        body: { output: { entries: [{ time: 2_000_000, priority: 6, unit: "nginx.service", message: "second" }, { time: 1_000_000, priority: 6, unit: "nginx.service", message: "first" }], more: false } },
      },
    })
    renderWithProviders(<JournalPanel hostId="h1" unit="nginx.service" onUnitChange={() => {}} />)
    await userEvent.setup().click(screen.getByRole("switch", { name: "Follow" }))
    await screen.findByText("first")
    expect(screen.getAllByRole("listitem").map((li) => li.textContent.includes("first"))).toEqual([true, false])
    const body = calls.find((c) => c.path.includes("journal.query"))?.body as { input: Record<string, unknown> }
    expect(body.input).toMatchObject({ unit: "nginx.service", priority: 6 })
    expect(typeof body.input.since).toBe("number")
  })
})
