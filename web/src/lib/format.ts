const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"]

const TIME_PARTS = { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" } as const
const timeFormat = new Intl.DateTimeFormat(undefined, TIME_PARTS)
const yearTimeFormat = new Intl.DateTimeFormat(undefined, { ...TIME_PARTS, year: "numeric" })

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—"
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024
    unit++
  }
  const digits = unit === 0 || value >= 100 ? 0 : 1
  return `${value.toFixed(digits)} ${BYTE_UNITS[unit]}`
}

export function formatDuration(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 0) return "—"
  const seconds = Math.floor(totalSeconds)
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} d ${hours} h ${minutes} min`
  if (hours > 0) return `${hours} h ${minutes} min`
  if (minutes > 0) return `${minutes} min`
  return `${seconds} s`
}

// formatUptime fits a tile's figure on one line: past a day, minutes are dropped.
export function formatUptime(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 86400) return formatDuration(totalSeconds)
  const seconds = Math.floor(totalSeconds)
  return `${Math.floor(seconds / 86400)} d ${Math.floor((seconds % 86400) / 3600)} h`
}

export function formatTime(iso: string | null): string {
  if (iso === null) return "—"
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return (date.getFullYear() === new Date().getFullYear() ? timeFormat : yearTimeFormat).format(date)
}
