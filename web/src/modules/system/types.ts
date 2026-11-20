import type { Status } from "@/components/visual/status"
export const SYSTEM_MODULE = "system"
export const SYSTEM_ACTION_VERSION = 1

// The host's latest changes, from which the overview takes the last reboot
// and the last failed package change. "changes" first, so change events
// invalidate it.
export const systemChangesKey = (hostId: string) => ["changes", hostId, "system"] as const

// The system module's resource outputs (schemas/system.*).
export interface Upgradable {
  name: string
  from: string
  to: string
  origin: string
  security: boolean
}

export interface Held {
  name: string
  version: string
  reason: "held" | "phased"
}

export interface Updates {
  upgradable: Upgradable[]
  removals: string[]
  heldBack: Held[]
  refreshedAt: string | null
  failedSources: string[]
  dpkgInterrupted: boolean
}

export interface InstalledPackage {
  name: string
  version: string
  arch: string
  description: string
  protected: string | null
}

export interface RebootState {
  required: boolean
  packages: string[]
  kernel: { running: string; newest: string } | null
  services: string[]
}

export interface AutoUpdates {
  installed: boolean
  enabled: boolean
}

export interface DockerState {
  state: "absent" | "docker-ce" | "distro"
  version?: string
  updateAvailable?: boolean
  running?: boolean
  conflicts?: string[]
  containers?: number
  images?: number
  volumes?: number
  labelled?: { name: string; module: string }[]
}

// The output of a completed system.host.reboot change.
export interface RebootOutput {
  scheduledAt: string
  bootTimeBefore: string
}

// An offline host's tiles carry this pill, which is also why their actions
// are disabled (UX-7).
export const HOST_OFFLINE: { status: Status; label: string } = { status: "off", label: "Host offline" }
