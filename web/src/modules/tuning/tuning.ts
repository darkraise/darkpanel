// SysctlKey is one kernel parameter as tuning.sysctl.get answers it. Min and
// max are decimal strings: fs.file-max exceeds a JavaScript number.
export interface SysctlKey {
  key: string
  help?: string
  kind?: "integer" | "choice" | "port-range" | ""
  min?: string
  max?: string
  choices?: string[]
  live?: string
  set?: string
  laterFiles?: string[]
  owner?: string
  unavailable?: string
}

export interface SysctlState {
  file: string
  keys: SysctlKey[]
}

export interface SwapEntry {
  path: string
  type: string
  size: number
  used: number
  darkpanel: boolean
}

export interface SwapState {
  swaps: SwapEntry[]
  memoryTotal: number
  memoryAvailable: number
  filesystem: string
  free: number
  unavailable?: string
}

export const MIB = 1024 * 1024

function within(v: string, min?: string, max?: string): boolean {
  const n = BigInt(v)
  return (min === undefined || min === "" || n >= BigInt(min)) && (max === undefined || max === "" || n <= BigInt(max))
}

// valueError is why value cannot be k's value at boot, null when it can; ""
// leaves the key out of Darkpanel's file.
export function valueError(k: SysctlKey, value: string): string | null {
  const v = value.trim()
  if (v === "") return null
  switch (k.kind) {
    case "integer":
      return /^-?\d+$/.test(v) && within(v, k.min, k.max) ? null : `Use ${k.min ?? ""} to ${k.max ?? ""}`
    case "choice":
      return (k.choices ?? []).includes(v) ? null : `Use one of ${(k.choices ?? []).join(", ")}`
    case "port-range": {
      const m = /^(\d+)\s+(\d+)$/.exec(v)
      const ok = m !== null && within(m[1] ?? "", k.min, k.max) && within(m[2] ?? "", k.min, k.max) && BigInt(m[1] ?? "0") < BigInt(m[2] ?? "0")
      return ok ? null : `Use two different ports from ${k.min ?? ""} to ${k.max ?? ""}, the lower first`
    }
    default:
      return null
  }
}

// normalValue is value as the file writes it: a port range with one space.
export function normalValue(value: string): string {
  return value.trim().split(/\s+/).join(" ")
}

// swapLimit is the largest swap file size in MiB the host has room for:
// the free space on / less 1 GiB, counting the file's own size when it is
// rewritten, and at most 64 GiB.
export function swapLimit(state: SwapState): number {
  const own = state.swaps.find((s) => s.darkpanel)?.size ?? 0
  return Math.min(65536, Math.floor((state.free + own) / MIB) - 1024)
}
