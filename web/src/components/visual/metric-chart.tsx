import { useMemo } from "react"
import { Area, AreaChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { ChartContainer, type ChartConfig } from "darkraise-ui/components/chart"
import { summary, type ChartData } from "@/lib/metrics"

// Series after the first are dashed, each its own way, so lines differ by
// more than colour.
const DASHES = [undefined, "6 3", "2 2", "8 3 2 3"]

// colour is the kit token of the i-th series. The kit defines --chart-N on
// the root; its --color-chart-N alias exists only where a utility uses it.
function colour(i: number): string {
  return `var(--chart-${(i % 8) + 1})`
}

function dash(i: number): string | undefined {
  return DASHES[i % DASHES.length]
}

const clock = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" })
const day = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" })

// MetricChart draws one metric over the range. Its accessible name is its
// text summary (latest, min and max of each series), which also shows below
// it; missing minutes are null, so the line breaks there.
export function MetricChart({ title, unit, data, area = false, daily, rangeText }: { title: string; unit: string; data: ChartData; area?: boolean; daily: boolean; rangeText: string }) {
  const config: ChartConfig = useMemo(() => Object.fromEntries(data.series.map((s, i) => [s.key, { label: s.label, color: colour(i) }])), [data.series])
  const rows = useMemo(
    () => data.times.map((t, i) => Object.fromEntries([["ts", t], ...data.series.map((s) => [s.key, s.values[i] ?? null])])),
    [data.times, data.series],
  )
  const lines = data.series.map((s) => ({ label: s.label, text: summary(s.values, unit) }))
  const hidden = data.hidden === null ? "" : ` Showing the ${data.hidden.shown} highest of ${data.hidden.of} ${data.hidden.noun}.`
  const label = `${title}, ${rangeText}. ${lines.map((l) => `${l.label}: ${l.text}`).join("; ")}.${hidden}`
  const Chart = area ? AreaChart : LineChart
  const tick = (t: number) => (daily ? day : clock).format(new Date(t))
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle>
          <h3 className="text-sm font-medium">{title}</h3>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <ChartContainer config={config} description={label} aria-label={label} aria-describedby={undefined} tabIndex={0} empty={rows.length === 0} className="h-40 w-full">
          <Chart data={rows} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="ts" type="number" scale="time" domain={["dataMin", "dataMax"]} tickFormatter={tick} tickLine={false} axisLine={false} minTickGap={32} />
            <YAxis tickLine={false} axisLine={false} width={40} />
            {data.series.map((s, i) =>
              area ? (
                <Area key={s.key} dataKey={s.key} type="monotone" stroke={colour(i)} fill={colour(i)} fillOpacity={0.2} connectNulls={false} isAnimationActive={false} />
              ) : (
                <Line key={s.key} dataKey={s.key} type="monotone" stroke={colour(i)} strokeWidth={2} strokeDasharray={dash(i)} dot={false} connectNulls={false} isAnimationActive={false} />
              ),
            )}
          </Chart>
        </ChartContainer>
        <ul className="flex flex-col gap-0.5 text-xs text-muted-foreground" aria-hidden>
          {lines.map((l, i) => (
            <li key={data.series[i]!.key} className="flex min-w-0 items-center gap-2">
              <svg width="20" height="6" className="shrink-0">
                <line x1="0" y1="3" x2="20" y2="3" stroke={colour(i)} strokeWidth="2" strokeDasharray={dash(i)} />
              </svg>
              {/* A no-break space keeps each value on the line of its unit when the key wraps. */}
              <span className="min-w-0 wrap-anywhere">{`${l.label}: ${l.text.replace(/(\d) (%|MB\/s|GiB)/g, "$1\u00a0$2")}`}</span>
            </li>
          ))}
          {hidden !== "" && <li>{hidden.trim()}</li>}
        </ul>
      </CardContent>
    </Card>
  )
}
