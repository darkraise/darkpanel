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

function parse<T>(event: MessageEvent): T | null {
  if (typeof event.data !== "string") return null
  try {
    return JSON.parse(event.data) as T
  } catch {
    return null
  }
}

// connectEvents keeps the controller's event stream open and refetches the
// queries each event names (spec 1.6). EventSource reconnects on its own with
// the last id it saw, so the controller replays what was missed or sends a
// reset. The returned function closes the stream.
export function connectEvents(client: QueryClient, factory: EventSourceFactory = (url) => new EventSource(url)): () => void {
  const source = factory("/api/v1/events")
  function on<T>(type: string, keys: (data: T) => QueryKey[]) {
    source.addEventListener(type, (event: MessageEvent) => {
      const data = parse<T>(event)
      if (data === null) return
      for (const queryKey of keys(data)) void client.invalidateQueries({ queryKey })
    })
  }
  on<ChangeEventData>("change", (d) => [["changes"], ["change", d.changeId], ["audit"]])
  on<StepEventData>("composite.step", (d) => [["change", d.changeId], ["steps", d.changeId]])
  on<object>("host", () => [HOSTS_QUERY_KEY])
  on<ModuleEventData>("module", (d) => [HOSTS_QUERY_KEY, ["resource", d.hostId]])
  source.addEventListener("reset", () => void client.invalidateQueries())
  return () => source.close()
}
