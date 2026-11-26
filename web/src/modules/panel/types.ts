import type { Status } from "@/components/visual/status"

export const PANEL_MODULE = "panel"
export const PANEL_ACTION_VERSION = 1

// panelStatusKey is the one query key the banner and the panel page share
// for panel.exposure.status.
export const panelStatusKey = [PANEL_MODULE, "exposure"]

export type ExposureVia = "npm" | "tunnel"

export type ExposureState = "open" | "revoked" | "needs_repair"

export interface ExposureView {
  id: string
  via: ExposureVia
  hostname: string
  hostId: string
  tunnel?: string
  subnet: string
  gateway: string
  proxy: string
  port: number
  state: ExposureState
  listenerId: string
  createdAt: string
  // The proxy passes WebSocket upgrades, which the console terminal needs;
  // false on an NPM exposure opened before they were passed.
  websockets?: boolean
}

export interface StatusOutput {
  exposed: boolean
  exposure?: ExposureView | null
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isExposureView(v: unknown): v is ExposureView {
  return (
    isRecord(v) &&
    typeof v.id === "string" &&
    (v.via === "npm" || v.via === "tunnel") &&
    typeof v.hostname === "string" &&
    typeof v.hostId === "string" &&
    (v.tunnel === undefined || typeof v.tunnel === "string") &&
    typeof v.subnet === "string" &&
    typeof v.gateway === "string" &&
    typeof v.proxy === "string" &&
    typeof v.port === "number" &&
    (v.state === "open" || v.state === "revoked" || v.state === "needs_repair") &&
    typeof v.listenerId === "string" &&
    typeof v.createdAt === "string"
  )
}

export function isStatusOutput(v: unknown): v is StatusOutput {
  if (!isRecord(v) || typeof v.exposed !== "boolean") return false
  return v.exposed ? isExposureView(v.exposure) : v.exposure === undefined || v.exposure === null
}

const EXPOSURE_STATE: Record<ExposureState, { status: Status; label: string }> = {
  open: { status: "ok", label: "Open" },
  revoked: { status: "warn", label: "Revoked" },
  needs_repair: { status: "error", label: "Needs repair" },
}

export function exposureState(state: ExposureState): { status: Status; label: string } {
  return EXPOSURE_STATE[state]
}
