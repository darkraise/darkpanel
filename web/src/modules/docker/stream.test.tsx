import { act, renderHook } from "@testing-library/react"
import { expect, test } from "vitest"
import { EvtSource } from "@/test/evt-source"
import { streamUrl, useStream, type StreamEnd } from "./stream"

interface StEvent {
  n: number
}

function stSource(index = 0): EvtSource {
  const source = EvtSource.instances[index]
  if (source === undefined) throw new Error(`no stream ${index} was opened`)
  return source
}

test("streamUrl addresses a module stream of a host with its parameters", () => {
  expect(streamUrl("h1", "docker", "logs", { container: "web", tail: "200" })).toBe("/api/v1/hosts/h1/streams/docker/logs?container=web&tail=200")
  expect(streamUrl("h1", "docker", "stats", {})).toBe("/api/v1/hosts/h1/streams/docker/stats")
})

test("streamUrl escapes the host id and the parameter values", () => {
  expect(streamUrl("h 1", "docker", "logs", { container: "a&b=c d" })).toBe("/api/v1/hosts/h%201/streams/docker/logs?container=a%26b%3Dc+d")
})

test("a null url opens no stream", () => {
  const { result } = renderHook(() => useStream<StEvent>(null))
  expect(EvtSource.instances).toHaveLength(0)
  expect(result.current).toEqual({ events: [], end: null })
})

test("data events are collected in order from one EventSource", () => {
  const { result, rerender } = renderHook(() => useStream<StEvent>("/s/1"))
  expect(EvtSource.instances).toHaveLength(1)
  expect(stSource().url).toBe("/s/1")
  act(() => {
    stSource().emit("data", { n: 1 })
    stSource().emit("data", { n: 2 })
  })
  expect(result.current.events).toEqual([{ n: 1 }, { n: 2 }])
  expect(result.current.end).toBeNull()
  rerender()
  expect(EvtSource.instances).toHaveLength(1)
  expect(result.current.events).toHaveLength(2)
})

test("only the last max events are kept", () => {
  const { result } = renderHook(() => useStream<StEvent>("/s/1", 3))
  act(() => {
    for (let n = 1; n <= 5; n++) stSource().emit("data", { n })
  })
  expect(result.current.events).toEqual([{ n: 3 }, { n: 4 }, { n: 5 }])
})

test("an end event is recorded and closes the stream", () => {
  const { result } = renderHook(() => useStream<StEvent>("/s/1"))
  act(() => {
    stSource().emit("data", { n: 1 })
    stSource().emit("end", { reason: "done" })
  })
  expect(result.current.end).toEqual({ reason: "done" })
  expect(stSource().closed).toBe(true)
  act(() => stSource().emit("data", { n: 2 }))
  expect(result.current.events).toEqual([{ n: 1 }])
})

test("an end event keeps the problem the agent sent", () => {
  const { result } = renderHook(() => useStream<StEvent>("/s/1"))
  const end: StreamEnd = { reason: "error", error: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker daemon is not running", code: "docker.engine_unavailable" } }
  act(() => stSource().emit("end", end))
  expect(result.current.end).toEqual(end)
})

test("a failed connection ends the stream with the error reason instead of reconnecting", () => {
  const { result } = renderHook(() => useStream<StEvent>("/s/1"))
  act(() => stSource().fail())
  expect(result.current.end).toEqual({ reason: "error" })
  expect(stSource().closed).toBe(true)
  expect(EvtSource.instances).toHaveLength(1)
})

test("an event whose data is not JSON is dropped", () => {
  const { result } = renderHook(() => useStream<StEvent>("/s/1"))
  act(() => {
    stSource().emit("data", "not json")
    stSource().emit("data", { n: 1 })
  })
  expect(result.current.events).toEqual([{ n: 1 }])
})

test("a new url closes the old stream and starts again with no events and no end", () => {
  const { result, rerender } = renderHook(({ url }: { url: string | null }) => useStream<StEvent>(url), { initialProps: { url: "/s/1" as string | null } })
  act(() => {
    stSource(0).emit("data", { n: 1 })
    stSource(0).emit("end", { reason: "done" })
  })
  rerender({ url: "/s/2" })
  expect(stSource(0).closed).toBe(true)
  expect(EvtSource.instances).toHaveLength(2)
  expect(stSource(1).url).toBe("/s/2")
  expect(result.current).toEqual({ events: [], end: null })
  act(() => stSource(1).emit("data", { n: 7 }))
  expect(result.current.events).toEqual([{ n: 7 }])
  act(() => stSource(0).emit("data", { n: 99 }))
  expect(result.current.events).toEqual([{ n: 7 }])
  rerender({ url: null })
  expect(stSource(1).closed).toBe(true)
  expect(result.current).toEqual({ events: [], end: null })
})

test("unmounting closes the stream", () => {
  const { unmount } = renderHook(() => useStream<StEvent>("/s/1"))
  unmount()
  expect(stSource().closed).toBe(true)
})
