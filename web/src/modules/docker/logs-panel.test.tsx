import { act, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { EvtSource } from "@/test/evt-source"
import { renderWithProviders } from "@/test/utils"
import { LogsPanel } from "./logs-panel"

function lpSource(n: number): EvtSource {
  const source = EvtSource.instances[n]
  if (source === undefined) throw new Error(`no stream ${n} was opened`)
  return source
}

function lpParams(source: EvtSource): { path: string; query: Record<string, string> } {
  const url = new URL(source.url, "http://localhost")
  return { path: url.pathname, query: Object.fromEntries(url.searchParams) }
}

function lpLines(): (string | null)[] {
  return Array.from(screen.getByRole("log", { name: "Container logs" }).children).map((line) => line.textContent)
}

function lpRender() {
  return renderWithProviders(<LogsPanel hostId="h1" containerId="web" />)
}

test("follows the container's log with the last 200 lines", () => {
  lpRender()
  expect(EvtSource.instances).toHaveLength(1)
  expect(lpParams(lpSource(0))).toEqual({ path: "/api/v1/hosts/h1/streams/docker/logs", query: { container: "web", live: "true", tail: "200" } })
  expect(screen.getByRole("checkbox", { name: "Follow" }).getAttribute("aria-checked")).toBe("true")
  expect(screen.getByText("Waiting for output").textContent).toBe("Waiting for output")
})

test("shows each line in order, marks stderr and renders text literally", () => {
  lpRender()
  act(() => {
    lpSource(0).emit("data", { stream: "stdout", text: "starting\n" })
    lpSource(0).emit("data", { stream: "stderr", text: "boom\r\n" })
    lpSource(0).emit("data", { stream: "stdout", text: "<b>bold</b>\n" })
  })
  expect(lpLines()).toEqual(["starting", "boom", "<b>bold</b>"])
  const log = screen.getByRole("log", { name: "Container logs" })
  expect(Array.from(log.children).map((l) => l.getAttribute("data-stream"))).toEqual(["stdout", "stderr", "stdout"])
  expect(log.querySelector("b")).toBeNull()
  expect(screen.queryByText("Waiting for output")).toBeNull()
})

test("unticking Follow reads the tail once: it reopens the stream without follow", async () => {
  lpRender()
  act(() => lpSource(0).emit("data", { stream: "stdout", text: "old\n" }))
  await userEvent.setup().click(screen.getByRole("checkbox", { name: "Follow" }))
  expect(EvtSource.instances).toHaveLength(2)
  expect(lpSource(0).closed).toBe(true)
  expect(lpParams(lpSource(1)).query).toEqual({ container: "web", tail: "200" })
  expect(lpLines()).toEqual([])
  expect(screen.getByRole("checkbox", { name: "Follow" }).getAttribute("aria-checked")).toBe("false")
})

test("choosing another tail reopens the stream with it", async () => {
  lpRender()
  await userEvent.setup().selectOptions(screen.getByLabelText("Tail"), "all")
  expect(EvtSource.instances).toHaveLength(2)
  expect(lpParams(lpSource(1)).query).toEqual({ container: "web", live: "true", tail: "all" })
})

test.each([
  ["done", undefined, "The log ended."],
  ["overflow", undefined, "The stream fell behind and was closed."],
  ["expired", undefined, "The stream reached its time limit."],
  ["revoked", undefined, "Access to the stream was revoked."],
  ["error", { type: "about:blank", title: "Not found", status: 404, detail: "container web was not found", code: "not_found" }, "container web was not found"],
  ["error", undefined, "The stream failed."],
] as const)("an %s end says why, and Reconnect reads the log again", async (reason, error, message) => {
  lpRender()
  act(() => {
    lpSource(0).emit("data", { stream: "stdout", text: "line\n" })
    lpSource(0).emit("end", error === undefined ? { reason } : { reason, error })
  })
  expect(screen.getByText(message).textContent).toBe(message)
  await userEvent.setup().click(screen.getByRole("button", { name: "Reconnect" }))
  expect(EvtSource.instances).toHaveLength(2)
  expect(lpParams(lpSource(1)).query).toEqual({ container: "web", live: "true", tail: "200" })
  expect(screen.queryByText(message)).toBeNull()
  expect(lpLines()).toEqual([])
})

test("a log that ended with no output says so", () => {
  lpRender()
  act(() => lpSource(0).emit("end", { reason: "done" }))
  expect(screen.getByText("No log output.").textContent).toBe("No log output.")
  expect(screen.queryByText("Waiting for output")).toBeNull()
})

test("closes the stream when the panel goes away", () => {
  const { unmount } = lpRender()
  expect(lpSource(0).closed).toBe(false)
  unmount()
  expect(lpSource(0).closed).toBe(true)
})

test("the panel has no accessibility violations", async () => {
  lpRender()
  act(() => {
    lpSource(0).emit("data", { stream: "stdout", text: "starting\n" })
    lpSource(0).emit("end", { reason: "expired" })
  })
  expect(await axe(screen.getByRole("log", { name: "Container logs" }).parentElement ?? document.body)).toHaveNoViolations()
})
