import { figureLabel, percent } from "./figures"

const SEGMENTS = 10

export function UsageBar({ used, total, label, unit }: { used: number; total: number; label: string; unit?: string }) {
  const pct = percent(used, total)
  return (
    <div role="img" aria-label={figureLabel(label, used, total, unit)} className="flex min-w-0 flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="text-muted-foreground truncate">{label}</span>
        <span className="font-mono tabular-nums">{`${used} / ${total}${unit ? ` ${unit}` : ""}`}</span>
      </div>
      <div className="flex gap-0.5" aria-hidden>
        {Array.from({ length: SEGMENTS }, (_, i) => {
          const fill = Math.min(100, Math.max(0, (pct - i * (100 / SEGMENTS)) * SEGMENTS))
          return (
            <div key={i} className="bg-muted h-2 flex-1 overflow-hidden rounded-sm">
              <div className="bg-primary h-full" style={{ width: `${fill}%` }} />
            </div>
          )
        })}
      </div>
    </div>
  )
}
