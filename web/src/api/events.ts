import type { QueryClient, QueryKey } from "@tanstack/react-query"
import { HOSTS_QUERY_KEY } from "./client"

export type EventSourceFactory = (url: string) => EventSource

interface ChangeEventData {
  changeId: string
}

interface StepEventData {
  changeId: string
}

interface ModuleEventData {
  hostId: string
}

// EventSource.CLOSED, spelled out because the factory may not build a real
// EventSource.
const CLOSED = 2
const MIN_RETRY_MS = 1000
const MAX_RETRY_MS = 30_000

function parse<T>(event: MessageEvent): T | null {
  if (typeof event.data !== "string") return null
  try {
    return JSON.parse(event.data) as T
  } catch {
    return null
  }
}

// connectEvents keeps the controller's event stream open and refetches the
// queries each event names (spec 1.6). While the browser reconnects on its
// own it sends the last id it saw, so the controller replays what was missed
// or sends a reset. A refused reconnect (429, a proxy's 502) leaves the
// EventSource closed for good, so a closed source is reopened with a growing
// delay, and a reopened stream refetches like a reset because its first
// request carries no last id. Open queries are refetched, but a plan is not:
// refetching one posts a new plan. The returned function closes the stream.
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
    function on<T>(type: string, keys: (data: T) => QueryKey[]) {
      next.addEventListener(type, (event: MessageEvent) => {
        const data = parse<T>(event)
        if (data === null) return
        for (const queryKey of keys(data)) void client.invalidateQueries({ queryKey })
      })
    }
    on<ChangeEventData>("change", (d) => [["changes"], ["change", d.changeId], ["audit"]])
    on<StepEventData>("composite.step", (d) => [["change", d.changeId], ["steps", d.changeId]])
    on<object>("host", () => [HOSTS_QUERY_KEY])
    on<ModuleEventData>("module", (d) => [HOSTS_QUERY_KEY, ["resource", d.hostId]])
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
