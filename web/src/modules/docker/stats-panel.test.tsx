import { act, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { EvtSource } from "@/test/evt-source"
import { renderWithProviders } from "@/test/utils"
import { StatsPanel } from "./stats-panel"
import type { StatsEvent } from "./types"

function spSource(n: number): EvtSource {
  const source = EvtSource.instances[n]
  if (source === undefined) throw new Error(`no stream ${n} was opened`)
  return source
}

function spSample(over: Partial<StatsEvent> = {}): StatsEvent {
  return { read: "2026-10-30T09:00:00Z", cpuPercent: 12.345, memUsage: 268435456, memLimit: 1073741824, netRx: 5242880, netTx: 1024, pids: 12, ...over }
}

function spValue(label: string): string | null | undefined {
  return within(screen.getByLabelText("Container usage")).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

function spRender() {
  return renderWithProviders(<StatsPanel hostId="h1" containerId="web" />)
}

test("opens the stats stream of the container and waits for the first sample", () => {
  spRender()
  expect(EvtSource.instances).toHaveLength(1)
  const url = new URL(spSource(0).url, "http://localhost")
  expect(url.pathname).toBe("/api/v1/hosts/h1/streams/docker/stats")
  expect(Object.fromEntries(url.searchParams)).toEqual({ container: "web" })
  expect(screen.getByText("Waiting for the first sample").textContent).toBe("Waiting for the first sample")
  expect(screen.queryByLabelText("Container usage")).toBeNull()
})

test("shows the latest sample and replaces it with the next", () => {
  spRender()
  act(() => spSource(0).emit("data", spSample()))
  expect(spValue("CPU")).toBe("12.3 %")
  expect(spValue("Memory")).toBe("256 MiB of 1.0 GiB (25.0 %)")
  expect(spValue("Received")).toBe("5.0 MiB")
  expect(spValue("Sent")).toBe("1.0 KiB")
  expect(spValue("Processes")).toBe("12")
  expect(screen.queryByText("Waiting for the first sample")).toBeNull()
  act(() => spSource(0).emit("data", spSample({ cpuPercent: 0, memUsage: 536870912, pids: 3 })))
  expect(spValue("CPU")).toBe("0.0 %")
  expect(spValue("Memory")).toBe("512 MiB of 1.0 GiB (50.0 %)")
  expect(spValue("Processes")).toBe("3")
})

test("a container without a memory limit shows only its usage", () => {
  spRender()
  act(() => spSource(0).emit("data", spSample({ memLimit: 0 })))
  expect(spValue("Memory")).toBe("256 MiB")
})

test("an end keeps the last sample, says why and Reconnect opens the stream again", async () => {
  spRender()
  act(() => {
    spSource(0).emit("data", spSample())
    spSource(0).emit("end", { reason: "error", error: { type: "about:blank", title: "Conflict", status: 409, detail: "container web is not running", code: "request.invalid_input" } })
  })
  expect(screen.getByText("container web is not running").textContent).toBe("container web is not running")
  expect(spValue("CPU")).toBe("12.3 %")
  await userEvent.setup().click(screen.getByRole("button", { name: "Reconnect" }))
  expect(EvtSource.instances).toHaveLength(2)
  expect(screen.queryByText("container web is not running")).toBeNull()
  expect(screen.getByText("Waiting for the first sample").textContent).toBe("Waiting for the first sample")
})

test("a done end says the stream ended", () => {
  spRender()
  act(() => spSource(0).emit("end", { reason: "done" }))
  expect(screen.getByText("The stats stream ended.").textContent).toBe("The stats stream ended.")
})

test("closes the stream when the panel goes away", () => {
  const { unmount } = spRender()
  expect(spSource(0).closed).toBe(false)
  unmount()
  expect(spSource(0).closed).toBe(true)
})

test("the panel has no accessibility violations", async () => {
  const { container } = spRender()
  act(() => spSource(0).emit("data", spSample()))
  expect(await axe(container)).toHaveNoViolations()
})
