import type { MetricsHistory, MetricsPoint, MetricsRange, PollStatus } from "@/api/types"

export const METRICS_RANGES: MetricsRange[] = ["1h", "24h", "7d", "30d", "90d"]

export const RANGE_TEXT: Record<MetricsRange, string> = {
  "1h": "last hour",
  "24h": "last 24 hours",
  "7d": "last 7 days",
  "30d": "last 30 days",
  "90d": "last 90 days",
}

// MAX_SERIES is how many lines one chart draws: the kit defines eight chart
// colour tokens. Mounts and interfaces past it are left out of the drawing,
// fullest or busiest first kept, and the chart says how many it shows.
export const MAX_SERIES = 8

export function historyKey(hostId: string, range: MetricsRange) {
  return ["metrics-history", hostId, range] as const
}

export interface Series {
  key: string
  label: string
  values: (number | null)[]
}

export interface ChartData {
  times: number[]
  series: Series[]
  // hidden says how many series are drawn of how many when some were left
  // out, else null.
  hidden: { shown: number; of: number; noun: string } | null
}

const GIB = 1024 ** 3

// slots is the start time of every minute or hour from the first point to
// the last, so a missing one becomes a null row: a break in the line, never
// a zero.
function slots(points: MetricsPoint[], resolution: "1m" | "1h"): { times: number[]; at: Map<number, MetricsPoint> } {
  const at = new Map<number, MetricsPoint>()
  for (const p of points) at.set(Date.parse(p.ts), p)
  if (points.length === 0) return { times: [], at }
  const step = resolution === "1h" ? 3_600_000 : 60_000
  const first = Date.parse(points[0]!.ts)
  const last = Date.parse(points[points.length - 1]!.ts)
  const times: number[] = []
  for (let t = first; t <= last; t += step) times.push(t)
  return { times, at }
}

function column(times: number[], at: Map<number, MetricsPoint>, pick: (p: MetricsPoint) => number | null): (number | null)[] {
  return times.map((t) => {
    const p = at.get(t)
    return p === undefined ? null : pick(p)
  })
}

function latest(values: (number | null)[]): number {
  for (let i = values.length - 1; i >= 0; i--) if (values[i] !== null) return values[i]!
  return -Infinity
}

// keep orders groups of series (a mount, or an interface's in and out) by
// their latest values, highest first, and keeps as many as MAX_SERIES lines
// allow.
function keep(groups: Series[][], noun: string): Pick<ChartData, "series" | "hidden"> {
  const total = (g: Series[]) => g.reduce((n, s) => n + latest(s.values), 0)
  const ranked = [...groups].sort((a, b) => total(b) - total(a))
  const per = Math.max(1, groups[0]?.length ?? 1)
  const room = Math.floor(MAX_SERIES / per)
  return { series: ranked.slice(0, room).flat(), hidden: ranked.length > room ? { shown: room, of: ranked.length, noun } : null }
}

export interface MetricsCharts {
  cpu: ChartData
  memory: ChartData
  disk: ChartData
  network: ChartData
  load: ChartData
  // perCpu says the load chart is divided by the host's logical CPUs.
  perCpu: boolean
}

// charts turns the history into the five charts' series. cpus is the host's
// logical CPU count when known; the history carries the raw load only.
export function charts(history: MetricsHistory, cpus: number | null): MetricsCharts {
  const { times, at } = slots(history.points, history.resolution)
  const col = (pick: (p: MetricsPoint) => number | null) => column(times, at, pick)
  const mounts = [...new Set(history.points.flatMap((p) => p.fs.map((f) => f.mount)))].sort()
  const ifaces = [...new Set(history.points.flatMap((p) => p.net.map((n) => n.iface)))].sort()
  const disk = keep(
    mounts.map((m, i) => [{
      key: `fs${i}`,
      label: m,
      values: col((p) => {
        const f = p.fs.find((x) => x.mount === m)
        return f === undefined || f.total <= 0 ? null : (f.used / f.total) * 100
      }),
    }]),
    "mounts",
  )
  const rate = (iface: string, dir: "rxBps" | "txBps") =>
    col((p) => {
      const n = p.net.find((x) => x.iface === iface)
      return n === undefined ? null : n[dir] / 1_000_000
    })
  const network = keep(
    ifaces.map((i, n) => [
      { key: `rx${n}`, label: `${i} in`, values: rate(i, "rxBps") },
      { key: `tx${n}`, label: `${i} out`, values: rate(i, "txBps") },
    ]),
    "interfaces",
  )
  const divisor = cpus !== null && cpus > 0 ? cpus : null
  return {
    cpu: { times, series: [{ key: "cpu", label: "CPU", values: col((p) => p.cpu) }], hidden: null },
    memory: { times, series: [{ key: "mem", label: "Used", values: col((p) => p.memUsed / GIB) }], hidden: null },
    disk: { times, ...disk },
    network: { times, ...network },
    load: { times, series: [{ key: "load", label: "1-minute load", values: col((p) => (divisor === null ? p.load1 : p.load1 / divisor)) }], hidden: null },
    perCpu: divisor !== null,
  }
}

export function summary(values: (number | null)[], unit: string): string {
  const v = values.filter((x): x is number => x !== null)
  if (v.length === 0) return "no samples"
  const f = (x: number) => `${x.toFixed(1)}${unit}`
  return `latest ${f(v[v.length - 1]!)}, min ${f(Math.min(...v))}, max ${f(Math.max(...v))}`
}

// pollReason says in words why the last poll failed.
export function pollReason(status: PollStatus): string {
  switch (status.lastErrorCode) {
    case "metrics.clock_skew": {
      const s = status.clockSkewS ?? 0
      return `the host's clock is ${Math.abs(s)} s ${s >= 0 ? "ahead" : "behind"}`
    }
    case "metrics.bad_answer":
      return "the host's answer could not be read"
    case "core.deadline_exceeded":
      return "the host did not answer"
    case "module.unavailable":
      return "the metrics module is unavailable on the host"
    default:
      return `the read failed (${status.lastErrorCode ?? "unknown"})`
  }
}
