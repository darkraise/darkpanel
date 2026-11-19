import { Cell, Pie, PieChart } from "recharts"
import { ChartContainer, type ChartConfig } from "darkraise-ui/components/chart"
import { StatusDot } from "./status-dot"
import type { Status } from "./status"

const fill: Record<Status, string> = {
  ok: "var(--color-success-solid)",
  warn: "var(--color-warning-solid)",
  error: "var(--color-destructive-solid)",
  info: "var(--color-info-solid)",
  off: "var(--color-muted-foreground)",
}

export function StatusDonut({ segments, label }: { segments: { status: Status; label: string; value: number }[]; label: string }) {
  const description = `${label}: ${segments.map((s) => `${s.label} ${s.value}`).join(", ")}`
  const config: ChartConfig = Object.fromEntries(segments.map((s) => [s.label, { label: s.label, color: fill[s.status] }]))
  const total = segments.reduce((n, s) => n + s.value, 0)
  return (
    <div className="flex items-center gap-4">
      {/* The kit names a chart only by description; aria-label gives the img its name, and the description is not read twice. */}
      <ChartContainer config={config} description={description} aria-label={description} aria-describedby={undefined} empty={total === 0} className="aspect-square size-28">
        <PieChart>
          <Pie data={segments} dataKey="value" nameKey="label" innerRadius="60%" outerRadius="100%" strokeWidth={2} isAnimationActive={false}>
            {segments.map((s) => (
              <Cell key={s.label} fill={fill[s.status]} />
            ))}
          </Pie>
        </PieChart>
      </ChartContainer>
      <ul className="flex flex-col gap-1 text-sm" aria-hidden>
        {segments.map((s) => (
          <li key={s.label} className="flex items-center gap-2">
            <StatusDot status={s.status} label={s.label} />
            <span>{s.label}</span>
            <span className="font-mono tabular-nums">{s.value}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
