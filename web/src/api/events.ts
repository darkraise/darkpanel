import type { QueryClient, QueryKey } from "@tanstack/react-query"
import { HOSTS_QUERY_KEY } from "./client"

export type EventSourceFactory = (url: string) => EventSource

// EventSource.CLOSED, spelled out because the factory may not build a real
// EventSource.
const CLOSED = 2
const MIN_RETRY_MS = 1000
const MAX_RETRY_MS = 30_000

// parse returns an event's data when it is a JSON object whose fields are
// strings, and null otherwise.
function parse<K extends string>(event: MessageEvent, fields: readonly K[]): Record<K, string> | null {
  if (typeof event.data !== "string") return null
  let data: unknown
  try {
    data = JSON.parse(event.data)
  } catch {
    return null
  }
  if (typeof data !== "object" || data === null || Array.isArray(data)) return null
  const record = data as Record<string, unknown>
  return fields.every((field) => typeof record[field] === "string") ? (record as Record<K, string>) : null
}

// connectEvents keeps the controller's event stream open and refetches the
// queries each event names (spec 1.6). While the browser reconnects on its
// own it sends the last id it saw, so the controller replays what was missed
// or sends a reset. A refused reconnect (429, a proxy's 502) leaves the
// EventSource closed for good, so a closed source is reopened with a growing
// delay, and a reopened stream refetches like a reset because its first
// request carries no last id. An event whose data cannot be read names
// nothing, so it refetches like a reset too. Open queries are refetched, but
// a plan is not: refetching one posts a new plan. The returned function
// closes the stream.
export function connectEvents(client: QueryClient, factory: EventSourceFactory = (url) => new EventSource(url)): () => void {
  let source: EventSource
  let timer: ReturnType<typeof setTimeout> | undefined
  let delay = MIN_RETRY_MS
  let reopened = false
  let stopped = false

  function refetchAll() {
    void client.invalidateQueries({ predicate: (query) => query.queryKey[0] !== "plan" })
  }

  function attach(next: EventSource) {
    source = next
    function on<K extends string>(type: string, fields: readonly K[], keys: (data: Record<K, string>) => QueryKey[]) {
      next.addEventListener(type, (event: MessageEvent) => {
        const data = parse(event, fields)
        if (data === null) {
          refetchAll()
          return
        }
        for (const queryKey of keys(data)) void client.invalidateQueries({ queryKey })
      })
    }
    on("change", ["changeId"], (d) => [["changes"], ["change", d.changeId], ["audit"]])
    on("composite.step", ["changeId"], (d) => [["change", d.changeId], ["steps", d.changeId]])
    on("host", [], () => [HOSTS_QUERY_KEY])
    on("module", ["hostId"], (d) => [HOSTS_QUERY_KEY, ["resource", d.hostId]])
    next.addEventListener("reset", refetchAll)
    next.addEventListener("open", () => {
      delay = MIN_RETRY_MS
      if (reopened) refetchAll()
    })
    next.addEventListener("error", () => {
      if (stopped || next.readyState !== CLOSED) return
      timer = setTimeout(() => {
        timer = undefined
        reopened = true
        attach(factory("/api/v1/events"))
      }, delay)
      delay = Math.min(delay * 2, MAX_RETRY_MS)
    })
  }

  attach(factory("/api/v1/events"))
  return () => {
    stopped = true
    if (timer !== undefined) clearTimeout(timer)
    source.close()
  }
}
