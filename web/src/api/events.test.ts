import { QueryClient, type QueryKey } from "@tanstack/react-query"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import { EvtSource } from "@/test/evt-source"
import { connectEvents } from "./events"

const cached: QueryKey[] = [
  ["hosts"],
  ["audit"],
  ["changes"],
  ["changes", { hostId: "h1" }],
  ["change", "c1"],
  ["change", "c2"],
  ["steps", "c1"],
  ["resource", "h1", "hostinfo.summary"],
  ["resource", "h2", "hostinfo.summary"],
  ["tokens"],
  ["plan", "core.token.revoke", "t1"],
]

const refetched = cached.filter((key) => key[0] !== "plan")

// evtConnect connects a client whose cache holds every key of cached, and
// returns it with the fake stream it opened.
function evtConnect() {
  const client = new QueryClient()
  for (const key of cached) client.setQueryData(key, {})
  let source: EvtSource | undefined
  const close = connectEvents(client, (url) => {
    source = new EvtSource(url)
    return source as unknown as EventSource
  })
  if (source === undefined) throw new Error("connectEvents opened no stream")
  return { client, source, close }
}

// evtInvalidated lists the cached keys whose queries are invalidated.
function evtInvalidated(client: QueryClient): QueryKey[] {
  return cached.filter((key) => client.getQueryState(key)?.isInvalidated === true)
}

describe("connectEvents", () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  test("opens one stream at /api/v1/events and closes it", () => {
    const { source, close } = evtConnect()
    expect(EvtSource.instances).toEqual([source])
    expect(source.url).toBe("/api/v1/events")
    expect(source.closed).toBe(false)
    close()
    expect(source.closed).toBe(true)
  })

  test.each<[string, unknown, QueryKey[]]>([
    [
      "change",
      { hostId: "h1", changeId: "c1", action: "demo.item.add", state: "completed", updatedAt: "2026-10-22T12:00:00Z" },
      [["audit"], ["changes"], ["changes", { hostId: "h1" }], ["change", "c1"]],
    ],
    ["composite.step", { changeId: "c1", stepId: "s1", state: "confirmed", childChangeId: "k1" }, [["change", "c1"], ["steps", "c1"]]],
    ["host", { hostId: "h1", online: false }, [["hosts"]]],
    [
      "module",
      { hostId: "h1", moduleId: "hostinfo", enabled: false, available: true },
      [["hosts"], ["resource", "h1", "hostinfo.summary"]],
    ],
    ["reset", {}, refetched],
  ])("a %s event invalidates the queries it names", (type, data, want) => {
    const { client, source } = evtConnect()
    source.emit(type, data)
    expect(evtInvalidated(client)).toEqual(cached.filter((key) => want.some((w) => JSON.stringify(w) === JSON.stringify(key))))
  })

  test.each<[string, string]>([
    ["change", "{not json"],
    ["change", "{}"],
    ["change", "5"],
    ["change", '["c1"]'],
    ["composite.step", "null"],
    ["composite.step", '{"changeId":7}'],
    ["module", '{"hostId":1}'],
    ["host", '"h1"'],
  ])("a %s event with data %s refetches everything", (type, data) => {
    const { client, source } = evtConnect()
    source.emit(type, data)
    expect(evtInvalidated(client)).toEqual(refetched)
  })

  test("uses the browser's EventSource without a factory", () => {
    const close = connectEvents(new QueryClient())
    expect(EvtSource.instances.map((s) => s.url)).toEqual(["/api/v1/events"])
    close()
    expect(EvtSource.instances[0]?.closed).toBe(true)
  })

  test("a reset leaves plan queries alone", () => {
    const { client, source } = evtConnect()
    source.emit("reset", {})
    expect(client.getQueryState(["plan", "core.token.revoke", "t1"])?.isInvalidated).toBe(false)
    expect(evtInvalidated(client)).toEqual(refetched)
  })

  test("reopens a stream the browser closed after one second", () => {
    const { source } = evtConnect()
    source.fail()
    expect(EvtSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(EvtSource.instances).toHaveLength(2)
    expect(EvtSource.instances[1]?.url).toBe("/api/v1/events")
  })

  test("a reopened stream refetches when it opens", () => {
    const { client, source } = evtConnect()
    source.fail()
    vi.advanceTimersByTime(1000)
    EvtSource.instances[1]?.emit("open", {})
    expect(evtInvalidated(client)).toEqual(refetched)
  })

  test("a reopened stream refetches on its first open only", () => {
    const { client, source } = evtConnect()
    source.fail()
    vi.advanceTimersByTime(1000)
    const reopened = EvtSource.instances[1]
    if (reopened === undefined) throw new Error("no stream was reopened")
    reopened.emit("open", {})
    expect(evtInvalidated(client)).toEqual(refetched)
    for (const key of cached) client.setQueryData(key, {})
    // The browser's own reconnect sends the last id, so nothing is missed.
    reopened.emit("open", {})
    expect(evtInvalidated(client)).toEqual([])
  })

  test("backs off from one second to thirty, and from one again after an open", () => {
    const { source } = evtConnect()
    let current = source
    const reopen = (wait: number) => {
      current.fail()
      const n = EvtSource.instances.length
      vi.advanceTimersByTime(wait - 1)
      expect(EvtSource.instances).toHaveLength(n)
      vi.advanceTimersByTime(1)
      expect(EvtSource.instances).toHaveLength(n + 1)
      const next = EvtSource.instances[n]
      if (next === undefined) throw new Error("no stream was reopened")
      current = next
    }
    for (const wait of [1000, 2000, 4000, 8000, 16_000, 30_000, 30_000]) reopen(wait)
    current.emit("open", {})
    reopen(1000)
    reopen(2000)
  })

  test("closing before the reopen timer fires opens nothing more", () => {
    const { source, close } = evtConnect()
    source.fail()
    close()
    vi.runAllTimers()
    expect(EvtSource.instances).toHaveLength(1)
  })
})
