import type { TunnelView } from "./types"

export interface Connector {
  tunnel: string
  container: string
  containerId: string
  // The Engine's container state; "missing" when the container is gone.
  state: string
}

export interface ConnectorList {
  connectors: Connector[]
}

// It sits under ["cloudflare"], the prefix CloudflareChange invalidates when a dialog closes.
export function connectorListKey(hostId: string) {
  return ["cloudflare", "connectors", hostId] as const
}

export function tunnelLabel(id: string, tunnels: Pick<TunnelView, "id" | "name">[]): string {
  return tunnels.find((t) => t.id === id)?.name ?? id
}
