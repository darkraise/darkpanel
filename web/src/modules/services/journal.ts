// JournalEntry is one entry of services.journal.query and the
// services.journal-follow stream.
export interface JournalEntry {
  time: number // microseconds since the epoch
  priority: number | null
  unit?: string
  pid?: number
  message: string
  truncated?: boolean
}

export const MAX_LINES = 2000

// reconnectDelay is the wait before the n-th reconnect of a dropped follow:
// 1 s, doubling to at most 30 s.
export function reconnectDelay(n: number): number {
  return Math.min(1000 * 2 ** n, 30_000)
}
