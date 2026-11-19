import { figureLabel, percent } from "./figures"

const R = 40
const ARC = 2 * Math.PI * R * 0.75

export function Gauge({ value, max, label, unit }: { value: number; max: number; label: string; unit?: string }) {
  const pct = percent(value, max)
  return (
    <div role="img" aria-label={figureLabel(label, value, max, unit)} className="flex flex-col items-center gap-1">
      <div className="relative size-24">
        <svg viewBox="0 0 100 100" className="size-full rotate-[135deg]" aria-hidden>
          <circle cx="50" cy="50" r={R} fill="none" strokeWidth="10" strokeLinecap="round" strokeDasharray={`${ARC} 1000`} className="stroke-muted" />
          <circle
            cx="50"
            cy="50"
            r={R}
            fill="none"
            strokeWidth="10"
            strokeLinecap="round"
            strokeDasharray={`${(ARC * pct) / 100} 1000`}
            className={pct === 0 ? "hidden" : "stroke-primary"}
          />
        </svg>
        <span className="absolute inset-0 flex items-center justify-center font-mono text-lg font-semibold tabular-nums">{pct}%</span>
      </div>
      <span className="text-muted-foreground text-sm">{label}</span>
      <span className="font-mono text-xs tabular-nums">{`${value} / ${max}${unit ? ` ${unit}` : ""}`}</span>
    </div>
  )
}
