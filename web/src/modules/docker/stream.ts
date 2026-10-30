import { useEffect, useState } from "react"
import type { Problem } from "@/api/types"

export interface StreamEnd {
  reason: "done" | "overflow" | "expired" | "revoked" | "error"
  error?: Problem
}

export function streamUrl(hostId: string, module: string, name: string, params: Record<string, string>): string {
  const path = `/api/v1/hosts/${encodeURIComponent(hostId)}/streams/${encodeURIComponent(module)}/${encodeURIComponent(name)}`
  const query = new URLSearchParams(params).toString()
  return query === "" ? path : `${path}?${query}`
}

interface StreamState<E> {
  key: string
  events: E[]
  end: StreamEnd | null
}

function parseData(event: Event): unknown {
  try {
    return JSON.parse((event as MessageEvent<string>).data)
  } catch {
    return undefined
  }
}

// useStream collects the events of one module stream. The state is keyed on
// the url and max, and is reset during render when that key changes, so a
// reopened stream shows no events and no end until its own connection sends
// one, even when its url was used before.
export function useStream<E>(url: string | null, max = 1000): { events: E[]; end: StreamEnd | null } {
  const key = `${max} ${url}`
  const [state, setState] = useState<StreamState<E>>({ key, events: [], end: null })
  if (state.key !== key) setState({ key, events: [], end: null })

  useEffect(() => {
    if (url === null) return
    let live = true
    let ended = false
    const source = new EventSource(url)
    const update = (change: (s: StreamState<E>) => StreamState<E>) => setState((prev) => (prev.key === key ? change(prev) : prev))

    source.addEventListener("data", (event) => {
      if (!live || ended) return
      const value = parseData(event)
      if (value === undefined) return
      update((s) => ({ ...s, events: [...s.events, value as E].slice(-max) }))
    })
    source.addEventListener("end", (event) => {
      if (!live) return
      ended = true
      source.close()
      const value = parseData(event)
      const end: StreamEnd = typeof value === "object" && value !== null ? (value as StreamEnd) : { reason: "error" }
      update((s) => ({ ...s, end }))
    })
    // A reconnect replays a module stream from its start, which would
    // duplicate the events already collected.
    source.addEventListener("error", () => {
      if (!live || ended) return
      ended = true
      source.close()
      update((s) => ({ ...s, end: { reason: "error" } }))
    })

    return () => {
      live = false
      source.close()
    }
  }, [url, max, key])

  return state.key === key ? { events: state.events, end: state.end } : { events: [], end: null }
}
